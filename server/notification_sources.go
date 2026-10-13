// Notification sources REST API (/v1/notification-sources).
//
// A notification source lets a user expose a named ingress endpoint (with a
// rotating token) which external systems use to post notifications into one of
// the user's topics. Persistence is PostgreSQL-backed; the record helpers live
// in notification_sources_postgres.go (postgres build) with a stub fallback in
// notification_sources_stub.go. The pure helpers here (topic normalization,
// list-item projection) are unit-tested in notification_sources_test.go.

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

// notificationSource is a user-owned ingress endpoint for external notifications.
// Token is only ever returned on creation, never in lists.
type notificationSource struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	TopicName  string     `json:"topic_name"`
	Enabled    bool       `json:"enabled"`
	Token      string     `json:"token,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// notificationSourceListItem is the public projection of a notificationSource:
// same fields minus the secret token.
type notificationSourceListItem struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	TopicName  string     `json:"topic_name"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// notificationSourceStore is a concurrency-safe in-memory fallback store keyed
// by user id, then by source id. (Postgres builds use the database instead.)
type notificationSourceStore struct {
	mu    sync.RWMutex
	items map[string]map[string]notificationSource
}

// notificationSources is the in-memory source storage for non-postgres builds.
var notificationSources = notificationSourceStore{
	items: make(map[string]map[string]notificationSource),
}

// createNotificationSourceRequest is the JSON body of POST /v1/notification-sources.
type createNotificationSourceRequest struct {
	Name      string `json:"name"`
	TopicName string `json:"topic_name"`
}

// updateNotificationSourceRequest is the JSON body of PATCH /v1/notification-sources/{id}.
// Nil pointers mean "field not present, keep as is".
type updateNotificationSourceRequest struct {
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
}

// rotateNotificationSourceResponse is the JSON body returned by token rotation:
// the only time an existing token is exposed.
type rotateNotificationSourceResponse struct {
	Token string `json:"token"`
}

// handleNotificationSources routes GET/POST /v1/notification-sources
// (list / create) for the authenticated user.
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

// handleNotificationSourceByID routes per-source actions:
// PATCH (update), DELETE, and POST .../rotate (token rotation).
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

// handleNotificationSourcesList writes the user's sources (without tokens).
func handleNotificationSourcesList(w http.ResponseWriter, uid types.Uid) {
	result, err := listNotificationSourcesByUser(uid.UserId())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSONStatus(w, http.StatusOK, map[string]any{"items": result})
}

// handleNotificationSourcesCreate validates the request and stores a new source,
// returning it once together with its secret token.
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

// handleNotificationSourcesUpdate applies a partial update to the user's source.
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

// handleNotificationSourcesRotate replaces the source token and returns the new one.
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

// handleNotificationSourcesDelete removes the user's source, 404 when absent.
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

// authenticateUserRequest authenticates an HTTP request via the Tinode auth
// schemes ("token", optionally "bearer" as an alias) and writes 401 on failure.
// It returns the authenticated user id and true on success.
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

// normalizeNotificationTopicName validates the requested target topic.
// Empty or "slf" resolves to the user's own personal ("slf") topic;
// "slf*"/"grp*"/"usr*" names pass through; anything else is rejected ("").
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

// toNotificationSourceListItem projects a source to its public form,
// stripping the secret token.
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

// writeJSONStatus writes payload as JSON with the given HTTP status code.
func writeJSONStatus(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, payload)
}

// writeJSONError writes {"error": message} as JSON with the given status code.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSONStatus(w, status, map[string]string{"error": message})
}
