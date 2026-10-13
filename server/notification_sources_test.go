package main

import (
	"testing"
	"time"

	"github.com/tinode/chat/server/store/types"
)

func TestNormalizeNotificationTopicName(t *testing.T) {
	uid := types.Uid(1)
	slf := uid.SlfName()
	if slf == "" {
		t.Fatal("test precondition: expected non-empty slf name")
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty resolves to slf", "", slf},
		{"blank resolves to slf", "   ", slf},
		{"slf resolves to slf", "slf", slf},
		{"padded slf resolves to slf", "  slf  ", slf},
		{"grp passes through", "grpAbCdEf", "grpAbCdEf"},
		{"usr passes through", "usrAbCdEf", "usrAbCdEf"},
		{"slf-prefixed passes through", slf, slf},
		{"channel rejected", "chnNews", ""},
		{"arbitrary rejected", "random", ""},
		{"padded arbitrary rejected", "  random  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeNotificationTopicName(tt.input, uid); got != tt.want {
				t.Errorf("normalizeNotificationTopicName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToNotificationSourceListItem(t *testing.T) {
	now := time.Now().UTC()
	lastUsed := now.Add(-time.Hour)
	item := notificationSource{
		ID:         "src1",
		Name:       "CI",
		TopicName:  "grpAbCdEf",
		Enabled:    true,
		Token:      "super-secret-token",
		CreatedAt:  now,
		UpdatedAt:  now,
		LastUsedAt: &lastUsed,
	}

	got := toNotificationSourceListItem(item)
	if got.ID != item.ID || got.Name != item.Name || got.TopicName != item.TopicName {
		t.Errorf("identity fields not copied: %+v", got)
	}
	if got.Enabled != item.Enabled {
		t.Errorf("enabled not copied: %+v", got)
	}
	if !got.CreatedAt.Equal(item.CreatedAt) || !got.UpdatedAt.Equal(item.UpdatedAt) {
		t.Errorf("timestamps not copied: %+v", got)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(lastUsed) {
		t.Errorf("last_used_at not preserved: %+v", got)
	}
	// The projection must not expose the token: notificationSourceListItem
	// has no token field, so a secret can never leak through list responses.
}
