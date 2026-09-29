/******************************************************************************
 *
 *  Description :
 *    Message reactions (emoji): {note what="react"} handling, persistence
 *    and fan-out, plus history enrichment.
 *
 *  Wire protocol (see org/docs/main/contracts/reactions.md):
 *    send:    {note topic seq=<msgSeq> what="react" event="add"|"remove"
 *              payload={"emoji":"❤️"}}
 *    live:    {info topic what="react" from=<uid> seq=<msgSeq>
 *              event="add"|"remove" payload={"emoji":"❤️"}}
 *    history: {data head={"reactions": {<uid>: <emoji>, ...}}}
 *
 *  Notes create no messages, so reactions never bump unread counters and
 *  never trigger push notifications.
 *
 *****************************************************************************/
package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Reaction wire constants.
const (
	constReactEventAdd    = "add"
	constReactEventRemove = "remove"
)

// Max emoji payload length in runes (one grapheme cluster is usually <= 8
// runes with modifiers/ZWJ; anything longer is garbage).
const maxReactionRunes = 16

type reactionPayload struct {
	Emoji string `json:"emoji"`
}

// handleReactionEvent processes {note what="react"}: validates, persists and
// fans the reaction out to topic sessions as {info what="react"}.
func (t *Topic) handleReactionEvent(msg *ClientComMessage) {
	asUid := types.ParseUserId(msg.AsUser)
	if asUid.IsZero() {
		return
	}

	event := msg.Note.Event
	if event == "" {
		event = constReactEventAdd
	}
	if event != constReactEventAdd && event != constReactEventRemove {
		logs.Warn.Printf("topic[%s]: reaction with unexpected event '%s'", t.name, event)
		return
	}

	emoji := ""
	if event == constReactEventAdd {
		var payload reactionPayload
		if len(msg.Note.Payload) > 0 {
			if err := json.Unmarshal(msg.Note.Payload, &payload); err != nil {
				logs.Warn.Printf("topic[%s]: malformed reaction payload: %v", t.name, err)
				return
			}
		}
		emoji = payload.Emoji
		if !isValidReactionEmoji(emoji) {
			logs.Warn.Printf("topic[%s]: invalid reaction emoji from %s", t.name, asUid.UserId())
			return
		}
	}

	t.applyReaction(msg, asUid, msg.Note.SeqId, event, emoji)
}

// parseReactionCarrier detects a reaction sent as a regular {pub}:
//
//	{pub head={"react": ":<seq>", "react-op": "add"|"remove"} content={"txt": "<emoji>"}}
//
// Carrier exists for SDK-limited clients (web tinode-sdk cannot send custom
// {note} payloads): it is processed exactly like {note what="react"} but no
// message is saved — no seq consumed, no unread bump, no push.
func parseReactionCarrier(msg *ClientComMessage) (seq int, op string, ok bool) {
	if msg.Pub == nil || msg.Pub.Head == nil {
		return 0, "", false
	}
	react, _ := msg.Pub.Head["react"].(string)
	if react == "" || !strings.HasPrefix(react, ":") {
		return 0, "", false
	}
	seq, err := strconv.Atoi(strings.TrimPrefix(react, ":"))
	if err != nil || seq <= 0 {
		return 0, "", false
	}
	op, _ = msg.Pub.Head["react-op"].(string)
	if op == "" {
		op = constReactEventAdd
	}
	if op != constReactEventAdd && op != constReactEventRemove {
		return 0, "", false
	}
	return seq, op, true
}

// handleReactionCarrier processes a {pub} reaction carrier (see
// parseReactionCarrier) and acks it with plain 200 (no seq assigned).
func (t *Topic) handleReactionCarrier(msg *ClientComMessage, asUid types.Uid, seq int, op string) {
	emoji := ""
	if op == constReactEventAdd {
		emoji = reactionEmojiFromContent(msg.Pub.Content)
		if !isValidReactionEmoji(emoji) {
			msg.sess.queueOut(ErrMalformedReply(msg, types.TimeNow()))
			return
		}
	}
	if !t.applyReaction(msg, asUid, seq, op, emoji) {
		return
	}
	msg.sess.queueOut(NoErrReply(msg, types.TimeNow()))
}

// reactionEmojiFromContent extracts the emoji from carrier content
// (plain string or Drafty {"txt": ...}).
func reactionEmojiFromContent(content any) string {
	switch c := content.(type) {
	case string:
		return strings.TrimSpace(c)
	case map[string]any:
		if txt, _ := c["txt"].(string); txt != "" {
			return strings.TrimSpace(txt)
		}
	}
	return ""
}

func isValidReactionEmoji(emoji string) bool {
	if emoji == "" {
		return false
	}
	return utf8.RuneCountInString(emoji) <= maxReactionRunes
}

// applyReaction validates membership/access/seq, persists the reaction and
// fans it out. Returns false when the request must be dropped.
func (t *Topic) applyReaction(msg *ClientComMessage, asUid types.Uid, seq int, event, emoji string) bool {
	pud, found := t.perUser[asUid]
	if !found || pud.deleted {
		logs.Warn.Printf("topic[%s]: reaction from non-member %s", t.name, asUid.UserId())
		return false
	}
	if !((pud.modeGiven & pud.modeWant).IsReader()) {
		logs.Warn.Printf("topic[%s]: reaction without read access %s", t.name, asUid.UserId())
		return false
	}

	if seq <= 0 || seq > t.lastID {
		// Drop bogus seq (server-issued IDs only).
		return false
	}

	if event == constReactEventRemove {
		if err := store.Reactions.Delete(t.name, seq, asUid); err != nil {
			logs.Warn.Printf("topic[%s]: failed to delete reaction: %v", t.name, err)
			return false
		}
	} else {
		if err := store.Reactions.Save(t.name, seq, asUid, emoji); err != nil {
			logs.Warn.Printf("topic[%s]: failed to save reaction: %v", t.name, err)
			return false
		}
	}

	// Fan out to attached sessions (sender included: clients apply idempotently).
	var payload json.RawMessage
	if event == constReactEventAdd {
		payload, _ = json.Marshal(reactionPayload{Emoji: emoji})
	}
	info := &ServerComMessage{
		Info: &MsgServerInfo{
			Topic:   t.original(asUid),
			From:    msg.AsUser,
			What:    "react",
			SeqId:   seq,
			Event:   event,
			Payload: payload,
		},
		RcptTo:    msg.RcptTo,
		AsUser:    msg.AsUser,
		Timestamp: msg.Timestamp,
	}
	t.broadcastToSessions(info)
	return true
}

// enrichMessagesWithReactions injects head["reactions"] = {uid: emoji} into
// history messages which have reactions. One batched query per history fetch.
func enrichMessagesWithReactions(topic string, messages []types.Message) {
	if len(messages) == 0 {
		return
	}
	seqs := make([]int, 0, len(messages))
	seqSet := map[int]struct{}{}
	for _, m := range messages {
		if _, dup := seqSet[m.SeqId]; !dup {
			seqSet[m.SeqId] = struct{}{}
			seqs = append(seqs, m.SeqId)
		}
	}
	bySeq, err := store.Reactions.ForMessages(topic, seqs)
	if err != nil {
		logs.Warn.Printf("topic[%s]: failed to load reactions: %v", topic, err)
		return
	}
	if len(bySeq) == 0 {
		return
	}
	for i := range messages {
		if users, ok := bySeq[messages[i].SeqId]; ok && len(users) > 0 {
			head := messages[i].Head
			if head == nil {
				head = types.KVMap{}
			}
			head["reactions"] = users
			messages[i].Head = head
		}
	}
}
