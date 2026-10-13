package main

import (
	"testing"

	"github.com/tinode/chat/server/store/types"
)

func TestRegistrationAuditPayload(t *testing.T) {
	s := test_makeSession(types.Uid(1))
	s.sid = "sid-1"

	// Nil message: only session basics, must not panic.
	got := registrationAuditPayload(s, nil)
	if got["session_id"] != "sid-1" {
		t.Errorf("session_id = %v", got["session_id"])
	}
	if got["login_requested"] != false {
		t.Errorf("login_requested = %v, want false", got["login_requested"])
	}

	// Full message: scheme, login flag, invite code, display name.
	msg := &ClientComMessage{Acc: &MsgClientAcc{
		Scheme: "basic",
		Login:  true,
		Tags:   []string{"invite:CODE7"},
		Desc: &MsgSetDesc{Public: map[string]any{
			"fn":    " Ivan ",
			"uname": "ivan",
		}},
	}}
	got = registrationAuditPayload(s, msg)
	if got["login_requested"] != true {
		t.Errorf("login_requested = %v, want true", got["login_requested"])
	}
	if got["scheme"] != "basic" {
		t.Errorf("scheme = %v", got["scheme"])
	}
	if got["invite_code"] != "CODE7" {
		t.Errorf("invite_code = %v", got["invite_code"])
	}
	if got["public_name"] != "Ivan" {
		t.Errorf("public_name = %v, want trimmed", got["public_name"])
	}
	if got["public_address"] != "ivan" {
		t.Errorf("public_address = %v", got["public_address"])
	}

	// No secrets may leak into the audit payload.
	for _, key := range []string{"secret", "password", "token", "tmpsecret"} {
		if _, ok := got[key]; ok {
			t.Errorf("audit payload must not contain %q", key)
		}
	}
}

func TestPostRegistrationAuditEventNoop(t *testing.T) {
	// Without COMPLIANCE_AUDIT_URL configured this must return immediately.
	s := test_makeSession(types.Uid(1))
	postRegistrationAuditEvent(s, registrationAuditEvent{EventType: "registration.failed"})
}
