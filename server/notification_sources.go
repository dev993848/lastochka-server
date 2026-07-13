package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tinode/chat/server/store/types"
)

type notificationSource struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TopicName  string    `json:"topic_name"`
	Enabled    bool      `json:"enabled"`
	Token      string    `json:"token,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

type notificationSourceListItem struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TopicName  string    `json:"topic_name"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

type notificationSourceStore struct {
	mu    sync.RWMutex
	items map[string]map[string]notificationSource
}

var notificationSources = notificationSourceStore{
	items: make(map[string]map[string]notificationSource),
}

type createNotificationSourceRequest struct {
	Name      string `json:"name"`
	TopicName string `json:"topic_name"`
}

type updateNotificationSourceRequest struct {
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
}

type rotateNotificationSourceResponse struct {
	Token string `json:"token"`
}

func handleNotificationSources(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleNotificationSourcesList(w, uid)
	case http.MethodPost:
		handleNotificationSourcesCreate(w, r, uid)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleNotificationSourceByID(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
	if !ok {
		return
	}

	idx := strings.Index(r.URL.Path, "/v1/notification-sources/")
	if idx < 0 {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	path := r.URL.Path[idx+len("/v1/notification-sources/"):]
	path = strings.Trim(path, "/")
	if path == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	parts := strings.Split(path, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "" && r.Method == http.MethodPatch:
		handleNotificationSourcesUpdate(w, r, uid, id)
	case action == "" && r.Method == http.MethodDelete:
		handleNotificationSourcesDelete(w, uid, id)
	case action == "rotate" && r.Method == http.MethodPost:
		handleNotificationSourcesRotate(w, uid, id)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleNotificationSourcesList(w http.ResponseWriter, uid types.Uid) {
	result, err := listNotificationSourcesByUser(uid.UserId())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSONStatus(w, http.StatusOK, map[string]any{"items": result})
}

func handleNotificationSourcesCreate(w http.ResponseWriter, r *http.Request, uid types.Uid) {
	var req createNotificationSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}

	topicName := normalizeNotificationTopicName(req.TopicName, uid)
	if topicName == "" {
		writeJSONError(w, http.StatusBadRequest, "topic_name is invalid")
		return
	}

	item, err := createNotificationSourceRecord(uid.UserId(), req.Name, topicName)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSONStatus(w, http.StatusCreated, item)
}

func handleNotificationSourcesUpdate(w http.ResponseWriter, r *http.Request, uid types.Uid, id string) {
	var req updateNotificationSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	item, err := updateNotificationSourceRecord(uid.UserId(), id, req)
	if errors.Is(err, errNotificationSourceNotFound) {
		writeJSONError(w, http.StatusNotFound, "source not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSONStatus(w, http.StatusOK, item)
}

func handleNotificationSourcesRotate(w http.ResponseWriter, uid types.Uid, id string) {
	newToken, err := rotateNotificationSourceRecord(uid.UserId(), id)
	if errors.Is(err, errNotificationSourceNotFound) {
		writeJSONError(w, http.StatusNotFound, "source not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSONStatus(w, http.StatusOK, rotateNotificationSourceResponse{Token: newToken})
}

func handleNotificationSourcesDelete(w http.ResponseWriter, uid types.Uid, id string) {
	if err := deleteNotificationSourceRecord(uid.UserId(), id); errors.Is(err, errNotificationSourceNotFound) {
		writeJSONError(w, http.StatusNotFound, "source not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func authenticateUserRequest(w http.ResponseWriter, r *http.Request) (types.Uid, bool) {
	w.Header().Set("Content-Type", "application/json")

	authMethod, secret := getHttpAuth(r)
	if strings.EqualFold(authMethod, "bearer") {
		authMethod = "token"
	}

	uid, challenge, err := authFileRequest(authMethod, secret, r.URL.Query().Get("sid"), getRemoteAddr(r))
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return types.ZeroUid, false
	}
	if challenge != nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication challenge is not supported")
		return types.ZeroUid, false
	}
	if uid.IsZero() {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return types.ZeroUid, false
	}

	return uid, true
}

func normalizeNotificationTopicName(topicName string, uid types.Uid) string {
	topicName = strings.TrimSpace(topicName)
	if topicName == "" || topicName == "slf" {
		return uid.SlfName()
	}
	if strings.HasPrefix(topicName, "slf") || strings.HasPrefix(topicName, "grp") || strings.HasPrefix(topicName, "usr") {
		return topicName
	}
	return ""
}

func toNotificationSourceListItem(item notificationSource) notificationSourceListItem {
	return notificationSourceListItem{
		ID:         item.ID,
		Name:       item.Name,
		TopicName:  item.TopicName,
		Enabled:    item.Enabled,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
		LastUsedAt: item.LastUsedAt,
	}
}

func writeJSONStatus(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSONStatus(w, status, map[string]string{"error": message})
}
