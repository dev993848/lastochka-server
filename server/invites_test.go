package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestInviteCodeFromAccountCreate(t *testing.T) {
	tests := []struct {
		name string
		msg  *ClientComMessage
		want string
	}{
		{"nil message", nil, ""},
		{"nil acc", &ClientComMessage{}, ""},
		{"no tags", &ClientComMessage{Acc: &MsgClientAcc{}}, ""},
		{"unrelated tags", &ClientComMessage{Acc: &MsgClientAcc{Tags: []string{"email:foo", "tel:123"}}}, ""},
		{"invite tag", &ClientComMessage{Acc: &MsgClientAcc{Tags: []string{"invite:ABC123"}}}, "ABC123"},
		{"invite among others", &ClientComMessage{Acc: &MsgClientAcc{Tags: []string{"email:x", "invite:ZZ9", "tel:1"}}}, "ZZ9"},
		{"whitespace trimmed", &ClientComMessage{Acc: &MsgClientAcc{Tags: []string{"  invite:  QW12  "}}}, "QW12"},
		{"first invite wins", &ClientComMessage{Acc: &MsgClientAcc{Tags: []string{"invite:FIRST", "invite:SECOND"}}}, "FIRST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inviteCodeFromAccountCreate(tt.msg); got != tt.want {
				t.Errorf("inviteCodeFromAccountCreate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInviteClientErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil becomes generic", nil, "invite_check_failed"},
		{"typed code passes through", errors.New("invite_required"), "invite_required"},
		{"typed expired passes through", errors.New("invite_expired"), "invite_expired"},
		{"raw driver error becomes generic", errors.New("pq: connection reset"), "invite_check_failed"},
		{"empty message becomes generic", errors.New(""), "invite_check_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inviteClientErrorCode(tt.err); got != tt.want {
				t.Errorf("inviteClientErrorCode(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestBuildInviteDerivedTags(t *testing.T) {
	clean := buildInviteDerivedTags(inviteConsumptionResult{})
	if !reflect.DeepEqual(clean, []string{"reg:invite"}) {
		t.Errorf("clean invite tags = %v, want [reg:invite]", clean)
	}

	suspicious := buildInviteDerivedTags(inviteConsumptionResult{SuspicionScore: 3})
	if !reflect.DeepEqual(suspicious, []string{"reg:invite", "reg:suspicious"}) {
		t.Errorf("suspicious invite tags = %v, want [reg:invite reg:suspicious]", suspicious)
	}
}

func TestIsServiceSession(t *testing.T) {
	if isServiceSession(nil) {
		t.Error("nil session must not be a service session")
	}
	if isServiceSession(&Session{}) {
		t.Error("plain session must not be a service session")
	}
	if !isServiceSession(&Session{isService: true}) {
		t.Error("root-keyed session must be a service session")
	}
}
