package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tinode/chat/server/store/types"
)

type note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type noteStore struct {
	mu    sync.RWMutex
	items map[string]map[string]note
}

var notes = noteStore{
	items: make(map[string]map[string]note),
}

type createNoteRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type updateNoteRequest struct {
	Title   *string `json:"title"`
	Content *string `json:"content"`
}

func handleNotes(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateAPIRequest(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleNotesList(w, uid)
	case http.MethodPost:
		handleNotesCreate(w, r, uid)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleNoteByID(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateAPIRequest(w, r)
	if !ok {
		return
	}

	idx := strings.Index(r.URL.Path, "/v1/notes/")
	if idx < 0 {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	path := r.URL.Path[idx+len("/v1/notes/"):]
	path = strings.Trim(path, "/")
	if path == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	parts := strings.Split(path, "/")
	id := parts[0]

	switch r.Method {
	case http.MethodPatch:
		handleNotesUpdate(w, r, uid, id)
	case http.MethodDelete:
		handleNotesDelete(w, uid, id)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleNotesList(w http.ResponseWriter, uid types.Uid) {
	userID := uid.UserId()

	notes.mu.RLock()
	defer notes.mu.RUnlock()

	userItems := notes.items[userID]
	result := make([]note, 0, len(userItems))
	for _, item := range userItems {
		result = append(result, item)
	}

	writeJSONStatus(w, http.StatusOK, map[string]any{"items": result})
}

func handleNotesCreate(w http.ResponseWriter, r *http.Request, uid types.Uid) {
	var req createNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeJSONError(w, http.StatusBadRequest, "title is required")
		return
	}

	id, err := randomHex(8)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate note id")
		return
	}
	now := time.Now().UTC()
	item := note{
		ID:        id,
		Title:     req.Title,
		Content:   strings.TrimSpace(req.Content),
		CreatedAt: now,
		UpdatedAt: now,
	}

	userID := uid.UserId()
	notes.mu.Lock()
	if notes.items[userID] == nil {
		notes.items[userID] = make(map[string]note)
	}
	notes.items[userID][id] = item
	notes.mu.Unlock()

	writeJSONStatus(w, http.StatusCreated, item)
}

func handleNotesUpdate(w http.ResponseWriter, r *http.Request, uid types.Uid, id string) {
	var req updateNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userID := uid.UserId()
	notes.mu.Lock()
	defer notes.mu.Unlock()

	item, ok := notes.items[userID][id]
	if !ok {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	}

	changed := false
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			writeJSONError(w, http.StatusBadRequest, "title must not be empty")
			return
		}
		item.Title = title
		changed = true
	}
	if req.Content != nil {
		item.Content = strings.TrimSpace(*req.Content)
		changed = true
	}
	if changed {
		item.UpdatedAt = time.Now().UTC()
		notes.items[userID][id] = item
	}

	writeJSONStatus(w, http.StatusOK, item)
}

func handleNotesDelete(w http.ResponseWriter, uid types.Uid, id string) {
	userID := uid.UserId()

	notes.mu.Lock()
	defer notes.mu.Unlock()

	if _, ok := notes.items[userID][id]; !ok {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	}
	delete(notes.items[userID], id)

	w.WriteHeader(http.StatusNoContent)
}
