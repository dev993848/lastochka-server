// Registration anti-abuse audit: ships registration attempt events
// (success/failure) to the Compliance service.
//
// Delivery is best-effort and asynchronous (fire-and-forget goroutine, 3s
// timeout); a missing COMPLIANCE_AUDIT_URL disables it silently. No secrets
// (passwords, tokens) are ever included in the payload — only metadata safe
// for audit: scheme, invite code, display name, credential methods.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store/types"
)

// registrationAuditEvent is one registration attempt shipped to Compliance.
type registrationAuditEvent struct {
	EventType string         `json:"event_type"`
	UserID    string         `json:"user_id,omitempty"`
	IPAddress string         `json:"ip_address,omitempty"`
	UserAgent string         `json:"user_agent,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// logRegistrationFailure records a rejected registration attempt.
// Full detail stays server-side; the client only ever sees typed reason codes.
func logRegistrationFailure(s *Session, msg *ClientComMessage, reason string, err error, payload map[string]any) {
	basePayload := registrationAuditPayload(s, msg)
	basePayload["status"] = "failed"
	basePayload["reason"] = reason
	if err != nil {
		basePayload["error"] = err.Error()
	}
	for k, v := range payload {
		basePayload[k] = v
	}
	postRegistrationAuditEvent(s, registrationAuditEvent{
		EventType: "registration.failed",
		IPAddress: strings.TrimSpace(s.remoteAddr),
		UserAgent: strings.TrimSpace(s.userAgent),
		Payload:   basePayload,
		Timestamp: time.Now().UTC(),
	})
}

// logRegistrationSuccess records an accepted registration with the created user id.
func logRegistrationSuccess(s *Session, msg *ClientComMessage, userID types.Uid) {
	payload := registrationAuditPayload(s, msg)
	payload["status"] = "succeeded"
	payload["created_user_id"] = userID.UserId()
	postRegistrationAuditEvent(s, registrationAuditEvent{
		EventType: "registration.succeeded",
		UserID:    userID.UserId(),
		IPAddress: strings.TrimSpace(s.remoteAddr),
		UserAgent: strings.TrimSpace(s.userAgent),
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	})
}

// registrationAuditPayload extracts audit-safe metadata from a registration
// attempt: session id, whether it was a login, auth scheme, invite code,
// display name and credential methods. It never includes secrets.
func registrationAuditPayload(s *Session, msg *ClientComMessage) map[string]any {
	payload := map[string]any{
		"session_id":      strings.TrimSpace(s.sid),
		"login_requested": msg != nil && msg.Acc != nil && msg.Acc.Login,
	}
	if msg == nil || msg.Acc == nil {
		return payload
	}
	if scheme := strings.TrimSpace(msg.Acc.Scheme); scheme != "" {
		payload["scheme"] = scheme
	}
	if inviteCode := strings.TrimSpace(inviteCodeFromAccountCreate(msg)); inviteCode != "" {
		payload["invite_code"] = inviteCode
	}
	if msg.Acc.Desc != nil {
		if pub, ok := msg.Acc.Desc.Public.(map[string]any); ok {
			if fullName, _ := pub["fn"].(string); strings.TrimSpace(fullName) != "" {
				payload["public_name"] = strings.TrimSpace(fullName)
			}
			if uname, _ := pub["uname"].(string); strings.TrimSpace(uname) != "" {
				payload["public_address"] = strings.TrimSpace(uname)
			}
		}
	}
	if methods := credentialMethods(normalizeCredentials(msg.Acc.Cred, true)); len(methods) > 0 {
		payload["credential_methods"] = methods
	}
	return payload
}

// postRegistrationAuditEvent ships the event to COMPLIANCE_AUDIT_URL with
// COMPLIANCE_INTERNAL_TOKEN (fallback: INTERNAL_TOKEN). It is a no-op when the
// endpoint or token is unconfigured, and never blocks the caller.
func postRegistrationAuditEvent(s *Session, event registrationAuditEvent) {
	endpoint := strings.TrimSpace(os.Getenv("COMPLIANCE_AUDIT_URL"))
	if endpoint == "" {
		return
	}
	token := strings.TrimSpace(os.Getenv("COMPLIANCE_INTERNAL_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("INTERNAL_TOKEN"))
	}
	if token == "" {
		logs.Warn.Println("registration audit: compliance token is not configured")
		return
	}

	go func() {
		body, err := json.Marshal(event)
		if err != nil {
			logs.Warn.Println("registration audit: marshal failed", err, "sid=", s.sid)
			return
		}

		client := &http.Client{Timeout: 3 * time.Second}
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			logs.Warn.Println("registration audit: request build failed", err, "sid=", s.sid)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", token)

		resp, err := client.Do(req)
		if err != nil {
			logs.Warn.Println("registration audit: request failed", err, "sid=", s.sid)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			logs.Warn.Println("registration audit: unexpected response status", resp.StatusCode, "sid=", s.sid)
		}
	}()
}
