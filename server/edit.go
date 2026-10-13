/******************************************************************************
 *
 *  Description :
 *    In-place message edits and reply markers
 *    (see org/docs/main/contracts/edit-reply.md).
 *
 *  Wire protocol:
 *    edit:  {pub head={"replace": ":<seq>"} content=<new content>}
 *             (no head.webrtc — call state updates are NOT edits)
 *    live:  {data seq=<origSeq> head={"replace": ":<origSeq>"}
 *             content=<new content>}  (no new seq, no unread bump, no push)
 *    reply: {pub head={"reply": ":<seq>"} content=<drafty with QQ quote>}
 *             (saved as a normal new message; server only checks the range)
 *    del:   stock Tinode {del} — no custom code, clients implement it.
 *
 *****************************************************************************/
package main

import (
	"strconv"
	"strings"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// parseEditHead detects an edit carrier: head.replace=":<seq>" WITHOUT
// head.webrtc (call signalling reuses "replace" for state updates and must
// never be treated as an edit). Returns the target seq and true when matched.
func parseEditHead(msg *ClientComMessage) (seq int, ok bool) {
	if msg.Pub == nil || msg.Pub.Head == nil {
		return 0, false
	}
	if msg.Pub.Head["webrtc"] != nil {
		return 0, false
	}
	replace, _ := msg.Pub.Head["replace"].(string)
	if replace == "" || !strings.HasPrefix(replace, ":") {
		return 0, false
	}
	seq, err := strconv.Atoi(strings.TrimPrefix(replace, ":"))
	if err != nil || seq <= 0 {
		return 0, false
	}
	return seq, true
}

// parseReplyHead detects a reply marker head.reply=":<seq>".
// hasReply reports the key presence; seq is valid only when hasReply is true.
// A present-but-malformed marker is reported as hasReply with seq <= 0 so the
// caller can reject it instead of saving a dangling reference.
func parseReplyHead(msg *ClientComMessage) (seq int, hasReply bool) {
	if msg.Pub == nil || msg.Pub.Head == nil {
		return 0, false
	}
	raw, present := msg.Pub.Head["reply"]
	if !present {
		return 0, false
	}
	text, _ := raw.(string)
	if text == "" || !strings.HasPrefix(text, ":") {
		return 0, true
	}
	seq, err := strconv.Atoi(strings.TrimPrefix(text, ":"))
	if err != nil || seq <= 0 {
		return 0, true
	}
	return seq, true
}

// isValidReplyTarget reports whether seq may be referenced (server-issued IDs
// only). Replying to a soft-deleted message is allowed by design (no extra DB
// read on the hot path); clients render "message deleted" on encounter.
func (t *Topic) isValidReplyTarget(seq int) bool {
	return seq > 0 && seq <= t.lastID
}

// editPersistHead returns the head to store: a copy of the publish head
// without the "replace" control key, so history replays never look like
// live edits. Returns nil when nothing remains.
func editPersistHead(head map[string]any) types.KVMap {
	if len(head) == 0 {
		return nil
	}
	stored := make(types.KVMap, len(head))
	for k, v := range head {
		if k == "replace" {
			continue
		}
		stored[k] = v
	}
	if len(stored) == 0 {
		return nil
	}
	return stored
}

// handleEditMessage processes an edit carrier (see parseEditHead): validates
// membership/write access/range, persists the replacement author-only, and
// fans it out as {data} with the ORIGINAL seq (no seq consumed, no unread
// bump, no push). The sender is acked with plain 200 like reaction carriers.
func (t *Topic) handleEditMessage(msg *ClientComMessage, asUid types.Uid, seq int) {
	pud, found := t.perUser[asUid]
	if !found || pud.deleted {
		logs.Warn.Printf("topic[%s]: edit from non-member %s", t.name, asUid.UserId())
		return
	}
	if !((pud.modeGiven & pud.modeWant).IsWriter()) {
		logs.Warn.Printf("topic[%s]: edit without write access %s", t.name, asUid.UserId())
		return
	}
	if seq <= 0 || seq > t.lastID {
		// Drop bogus seq (server-issued IDs only).
		return
	}
	if msg.Pub.Content == nil {
		msg.sess.queueOut(ErrMalformedReply(msg, types.TimeNow()))
		return
	}

	marked, err := store.MessageEdits.UpdateContent(t.name, seq, asUid, editPersistHead(msg.Pub.Head), msg.Pub.Content)
	if err == store.ErrEditsNotSupported {
		msg.sess.queueOut(ErrNotImplementedReply(msg, types.TimeNow()))
		return
	}
	if err != nil {
		logs.Warn.Printf("topic[%s]: failed to save edit: %v", t.name, err)
		msg.sess.queueOut(ErrUnknown(msg.Id, t.original(asUid), msg.Timestamp))
		return
	}
	if marked == 0 {
		// No such message, foreign message, or already deleted.
		// Reasons are intentionally indistinguishable (no enumeration).
		msg.sess.queueOut(ErrPermissionDeniedReply(msg, types.TimeNow()))
		return
	}

	data := &ServerComMessage{
		Data: &MsgServerData{
			Topic:     msg.Original,
			From:      msg.AsUser,
			Timestamp: types.TimeNow(),
			SeqId:     seq,
			Head:      types.KVMap{"replace": ":" + strconv.Itoa(seq)},
			Content:   msg.Pub.Content,
		},
		RcptTo:    msg.RcptTo,
		AsUser:    msg.AsUser,
		Timestamp: msg.Timestamp,
	}
	t.broadcastToSessions(data)
	msg.sess.queueOut(NoErrReply(msg, types.TimeNow()))
}
