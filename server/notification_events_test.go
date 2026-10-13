package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotificationIngressToken(t *testing.T) {
	newReq := func(headers map[string]string, query string) *http.Request {
		r := httptest.NewRequest("POST", "/v1/ingress"+query, nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}

	tests := []struct {
		name string
		req  *http.Request
		want string
	}{
		{"no token", newReq(nil, ""), ""},
		{"header wins", newReq(map[string]string{"X-Source-Token": "hdr", "Authorization": "Bearer br", "": ""}, "?token=q"), "hdr"},
		{"bearer", newReq(map[string]string{"Authorization": "Bearer abc123"}, ""), "abc123"},
		{"bearer case-insensitive", newReq(map[string]string{"Authorization": "BEARER xyz"}, ""), "xyz"},
		{"query", newReq(nil, "?token=qry"), "qry"},
		{"header trimmed", newReq(map[string]string{"X-Source-Token": "  spaced  "}, ""), "spaced"},
		{"bearer without token falls to query", newReq(map[string]string{"Authorization": "Bearer"}, "?token=fallback"), "fallback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notificationIngressToken(tt.req); got != tt.want {
				t.Errorf("notificationIngressToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeIngressContent(t *testing.T) {
	tests := []struct {
		name         string
		title, text  string
		body         string
		defaultTitle string
		wantTitle    string
		wantBody     string
		wantValid    bool
	}{
		{"all empty invalid", "", "", "", "src", "", "", false},
		{"whitespace invalid", "  ", " ", " ", "src", "", "", false},
		{"title only", " Hello ", "", "", "src", "Hello", "", true},
		{"body only, title from source", "", "", " World ", "src", "src", "World", true},
		{"text becomes body", "", "Some text", "", "src", "src", "Some text", true},
		{"body preferred over text", "T", "text", "body", "src", "T", "body", true},
		{"explicit title kept", "T", "", "B", "src", "T", "B", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body, valid := normalizeIngressContent(tt.title, tt.text, tt.body, tt.defaultTitle)
			if title != tt.wantTitle || body != tt.wantBody || valid != tt.wantValid {
				t.Errorf("normalizeIngressContent() = (%q, %q, %v), want (%q, %q, %v)",
					title, body, valid, tt.wantTitle, tt.wantBody, tt.wantValid)
			}
		})
	}
}

func TestNormalizeIngressPayload(t *testing.T) {
	if got := normalizeIngressPayload(nil); string(got) != "{}" {
		t.Errorf("nil payload = %q, want {}", got)
	}
	if got := normalizeIngressPayload(json.RawMessage{}); string(got) != "{}" {
		t.Errorf("empty payload = %q, want {}", got)
	}
	keep := json.RawMessage(`{"a":1}`)
	if got := normalizeIngressPayload(keep); string(got) != string(keep) {
		t.Errorf("non-empty payload changed: %q", got)
	}
}
