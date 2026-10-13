// User notifications REST API (/v1/notifications) and external ingress (/v1/ingress).
//
// External systems post notifications with a source token; users read and
// manage them with their session auth. Persistence is PostgreSQL-backed (see
// notification_events_postgres.go, notification_events_stub.go). Token
// extraction and content normalization are pure and unit-tested in
// notification_events_test.go.

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/store/types"
)

// notificationEvent is a single notification delivered to a user.
type notificationEvent struct {
	ID         string          `json:"id"`
	SourceID   string          `json:"source_id"`
	SourceName string          `json:"source_name"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	ReadAt     *time.Time      `json:"read_at,omitempty"`
}

// notificationIngressRequest is the JSON body of POST /v1/ingress.
// Title and body fall back to each other and to the source name
// (see normalizeIngressContent).
type notificationIngressRequest struct {
	Title   string          `json:"title"`
	Text    string          `json:"text"`
	Body    string          `json:"body"`
	Payload json.RawMessage `json:"payload"`
}

// handleNotificationIngress accepts a notification from an external system.
// Auth is the source token (see notificationIngressToken), not the user session.
func handleNotificationIngress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := notificationIngressToken(r)
	if token == "" {
		writeJSONError(w, http.StatusUnauthorized, "source token is required")
		return
	}

	ownerID, source, ok, err := findNotificationSourceByToken(token)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok || !source.Enabled {
		writeJSONError(w, http.StatusUnauthorized, "invalid source token")
		return
	}

	var req notificationIngressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	title, body, valid := normalizeIngressContent(req.Title, req.Text, req.Body, source.Name)
	if !valid {
		writeJSONError(w, http.StatusBadRequest, "title or body is required")
		return
	}

	item, err := createNotificationEvent(ownerID, source, title, body, normalizeIngressPayload(req.Payload))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = touchNotificationSourceLastUsed(source.ID)

	writeJSONStatus(w, http.StatusCreated, item)
}

// handleNotifications routes GET/POST /v1/notifications (list / mark one read)
// for the authenticated user.
func handleNotifications(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleNotificationsList(w, r, uid)
	case http.MethodPost:
		handleNotificationsMarkRead(w, r, uid)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleNotificationByID routes per-notification and bulk actions:
// DELETE /v1/notifications/{id}, POST .../read-all, DELETE .../read.
func handleNotificationByID(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
	if !ok {
		return
	}

	idx := strings.Index(r.URL.Path, "/v1/notifications/")
	if idx < 0 {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	id := strings.Trim(strings.TrimSpace(r.URL.Path[idx+len("/v1/notifications/"):]), "/")
	if id == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if id == "read-all" {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleNotificationsMarkAllRead(w, uid)
		return
	}
	if id == "read" {
		if r.Method != http.MethodDelete {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleNotificationsDeleteRead(w, uid)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		handleNotificationsDelete(w, uid, id)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleNotificationsList writes the user's notifications with an unread counter.
// Query params: limit (1..200, default 100), unread_only (1/true).
func handleNotificationsList(w http.ResponseWriter, r *http.Request, uid types.Uid) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeJSONError(w, http.StatusBadRequest, "limit is invalid")
			return
		}
		if parsed > 200 {
			parsed = 200
		}
		limit = parsed
	}

	unreadOnly := r.URL.Query().Get("unread_only") == "1" || strings.EqualFold(r.URL.Query().Get("unread_only"), "true")
	items, unreadCount, err := listNotificationEvents(uid.UserId(), limit, unreadOnly)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSONStatus(w, http.StatusOK, map[string]any{"items": items, "unread_count": unreadCount})
}

// handleNotificationsMarkRead marks one notification (JSON body {"id": ...}) as read.
func handleNotificationsMarkRead(w http.ResponseWriter, r *http.Request, uid types.Uid) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		writeJSONError(w, http.StatusBadRequest, "id is required")
		return
	}

	item, err := markNotificationEventRead(uid.UserId(), req.ID)
	if errors.Is(err, errNotificationEventNotFound) {
		writeJSONError(w, http.StatusNotFound, "notification not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSONStatus(w, http.StatusOK, item)
}

// handleNotificationsDelete removes one notification, 404 when absent.
func handleNotificationsDelete(w http.ResponseWriter, uid types.Uid, id string) {
	if err := deleteNotificationEvent(uid.UserId(), id); errors.Is(err, errNotificationEventNotFound) {
		writeJSONError(w, http.StatusNotFound, "notification not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleNotificationsMarkAllRead marks all user notifications as read,
// returning the count of updated rows.
func handleNotificationsMarkAllRead(w http.ResponseWriter, uid types.Uid) {
	updated, err := markAllNotificationEventsRead(uid.UserId())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"updated": updated})
}

// handleNotificationsDeleteRead deletes all read notifications of the user,
// returning the count of deleted rows.
func handleNotificationsDeleteRead(w http.ResponseWriter, uid types.Uid) {
	deleted, err := deleteReadNotificationEvents(uid.UserId())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// normalizeIngressContent trims and resolves the ingress title/body fallbacks:
// body falls back to text, title falls back to the source name. It returns
// valid=false when both end up empty (request must be rejected).
func normalizeIngressContent(title, text, body, defaultTitle string) (string, string, bool) {
	title = strings.TrimSpace(title)
	text = strings.TrimSpace(text)
	body = strings.TrimSpace(body)
	if body == "" {
		body = text
	}
	if title == "" && body == "" {
		return "", "", false
	}
	if title == "" {
		title = defaultTitle
	}
	return title, body, true
}

// normalizeIngressPayload defaults an empty payload to an empty JSON object
// so downstream consumers always see valid JSON.
func normalizeIngressPayload(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage("{}")
	}
	return payload
}

// notificationIngressToken extracts the source token from an ingress request.
// Precedence: X-Source-Token header, Authorization: Bearer, ?token= query.
func notificationIngressToken(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get("X-Source-Token")); token != "" {
		return token
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token
	}
	return ""
}
