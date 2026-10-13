package main

import (
	"reflect"
	"testing"
)

func TestParseEditHead(t *testing.T) {
	mkmsg := func(head map[string]any) *ClientComMessage {
		return &ClientComMessage{Pub: &MsgClientPub{Head: head}}
	}

	seq, ok := parseEditHead(mkmsg(map[string]any{"replace": ":42"}))
	if !ok || seq != 42 {
		t.Errorf("edit carrier: got seq=%d ok=%v", seq, ok)
	}

	// Call signalling reuses "replace" — must never be treated as an edit.
	if _, ok := parseEditHead(mkmsg(map[string]any{
		"replace": ":42", "webrtc": "started",
	})); ok {
		t.Error("call state update must not parse as edit")
	}

	invalid := []*ClientComMessage{
		{},
		mkmsg(nil),
		mkmsg(map[string]any{}),
		mkmsg(map[string]any{"replace": "42"}),   // missing ':' prefix
		mkmsg(map[string]any{"replace": ":0"}),   // non-positive seq
		mkmsg(map[string]any{"replace": ":abc"}), // not a number
		mkmsg(map[string]any{"webrtc": "started"}),
	}
	for i, msg := range invalid {
		if _, ok := parseEditHead(msg); ok {
			t.Errorf("case %d: expected not-ok edit carrier", i)
		}
	}
}

func TestParseReplyHead(t *testing.T) {
	mkmsg := func(head map[string]any) *ClientComMessage {
		return &ClientComMessage{Pub: &MsgClientPub{Head: head}}
	}

	seq, has := parseReplyHead(mkmsg(map[string]any{"reply": ":7"}))
	if !has || seq != 7 {
		t.Errorf("reply marker: got seq=%d has=%v", seq, has)
	}

	if _, has := parseReplyHead(mkmsg(map[string]any{})); has {
		t.Error("no marker must report absent")
	}
	if _, has := parseReplyHead(&ClientComMessage{}); has {
		t.Error("no pub must report absent")
	}

	// Present-but-malformed markers report presence with seq <= 0.
	for _, head := range []map[string]any{
		{"reply": "7"},
		{"reply": ":0"},
		{"reply": ":-2"},
		{"reply": ":abc"},
		{"reply": ""},
		{"reply": 42},
	} {
		if seq, has := parseReplyHead(mkmsg(head)); !has || seq > 0 {
			t.Errorf("head %v: expected present-but-invalid, got seq=%d has=%v", head, seq, has)
		}
	}
}

func TestEditPersistHead(t *testing.T) {
	if got := editPersistHead(nil); got != nil {
		t.Errorf("nil head must stay nil, got %v", got)
	}
	got := editPersistHead(map[string]any{"replace": ":5"})
	if got != nil {
		t.Errorf("replace-only head must collapse to nil, got %v", got)
	}
	got = editPersistHead(map[string]any{"replace": ":5", "reply": ":3", "x": 1})
	want := map[string]any{"reply": ":3", "x": 1}
	if !reflect.DeepEqual(map[string]any(got), want) {
		t.Errorf("control key must be stripped, got %v", got)
	}
}
