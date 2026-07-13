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

type notificationIngressRequest struct {
	Title   string          `json:"title"`
	Text    string          `json:"text"`
	Body    string          `json:"body"`
	Payload json.RawMessage `json:"payload"`
}

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

	req.Title = strings.TrimSpace(req.Title)
	req.Text = strings.TrimSpace(req.Text)
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" {
		req.Body = req.Text
	}
	if req.Title == "" && req.Body == "" {
		writeJSONError(w, http.StatusBadRequest, "title or body is required")
		return
	}
	if req.Title == "" {
		req.Title = source.Name
	}
	payload := req.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}

	item, err := createNotificationEvent(ownerID, source, req.Title, req.Body, payload)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = touchNotificationSourceLastUsed(source.ID)

	writeJSONStatus(w, http.StatusCreated, item)
}

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

func handleNotificationsMarkAllRead(w http.ResponseWriter, uid types.Uid) {
	updated, err := markAllNotificationEventsRead(uid.UserId())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"updated": updated})
}

func handleNotificationsDeleteRead(w http.ResponseWriter, uid types.Uid) {
	deleted, err := deleteReadNotificationEvents(uid.UserId())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"deleted": deleted})
}

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
