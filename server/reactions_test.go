package main

import (
	"strings"
	"testing"
)

func TestParseReactionCarrier(t *testing.T) {
	mkmsg := func(head map[string]any) *ClientComMessage {
		return &ClientComMessage{Pub: &MsgClientPub{Head: head}}
	}

	seq, op, ok := parseReactionCarrier(mkmsg(map[string]any{"react": ":42"}))
	if !ok || seq != 42 || op != constReactEventAdd {
		t.Errorf("default add carrier: got seq=%d op=%q ok=%v", seq, op, ok)
	}

	seq, op, ok = parseReactionCarrier(mkmsg(map[string]any{"react": ":7", "react-op": "remove"}))
	if !ok || seq != 7 || op != constReactEventRemove {
		t.Errorf("remove carrier: got seq=%d op=%q ok=%v", seq, op, ok)
	}

	invalid := []*ClientComMessage{
		{},
		mkmsg(nil),
		mkmsg(map[string]any{}),
		mkmsg(map[string]any{"react": "42"}),   // missing ':' prefix
		mkmsg(map[string]any{"react": ":0"}),   // non-positive seq
		mkmsg(map[string]any{"react": ":-3"}),  // negative seq
		mkmsg(map[string]any{"react": ":abc"}), // not a number
		mkmsg(map[string]any{"react": ":5", "react-op": "bogus"}),
	}
	for i, msg := range invalid {
		if _, _, ok := parseReactionCarrier(msg); ok {
			t.Errorf("case %d: expected not-ok carrier", i)
		}
	}
}

func TestReactionEmojiFromContent(t *testing.T) {
	if got := reactionEmojiFromContent("  ❤️  "); got != "❤️" {
		t.Errorf("plain string: got %q", got)
	}
	if got := reactionEmojiFromContent(map[string]any{"txt": " 👍 "}); got != "👍" {
		t.Errorf("drafty content: got %q", got)
	}
	if got := reactionEmojiFromContent(map[string]any{"nope": 1}); got != "" {
		t.Errorf("missing txt: got %q", got)
	}
	if got := reactionEmojiFromContent(42); got != "" {
		t.Errorf("wrong type: got %q", got)
	}
	if got := reactionEmojiFromContent(nil); got != "" {
		t.Errorf("nil: got %q", got)
	}
}

func TestIsValidReactionEmoji(t *testing.T) {
	if !isValidReactionEmoji("❤️") {
		t.Error("plain emoji should be valid")
	}
	if isValidReactionEmoji("") {
		t.Error("empty emoji should be invalid")
	}
	if isValidReactionEmoji(strings.Repeat("a", maxReactionRunes+1)) {
		t.Errorf("emoji longer than %d runes should be invalid", maxReactionRunes)
	}
	if !isValidReactionEmoji(strings.Repeat("a", maxReactionRunes)) {
		t.Errorf("emoji of exactly %d runes should be valid", maxReactionRunes)
	}
}
