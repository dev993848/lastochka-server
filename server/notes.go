// Personal notes REST API (/v1/notes).
//
// Notes are a per-user key-value scratchpad (title + content) kept in memory.
// They are an MVP: no persistence, no sharing, no search. The HTTP handlers in
// this file only deal with auth, routing and JSON; all business logic lives on
// noteStore methods below so it can be unit-tested without HTTP.

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

// errNoteNotFound is returned when a note id does not exist for the user.
var errNoteNotFound = errors.New("note not found")

// errNoteTitleRequired is returned when a note title is empty after trimming.
var errNoteTitleRequired = errors.New("title is required")

// note is a single personal note of a user.
type note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// noteStore is a concurrency-safe in-memory store of notes keyed by user id,
// then by note id.
type noteStore struct {
	mu    sync.RWMutex
	items map[string]map[string]note
}

// notes is the global in-memory note storage (MVP, not persisted).
var notes = noteStore{
	items: make(map[string]map[string]note),
}

// createNoteRequest is the JSON body of POST /v1/notes.
type createNoteRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// updateNoteRequest is the JSON body of PATCH /v1/notes/{id}.
// Nil pointers mean "field not present, keep as is".
type updateNoteRequest struct {
	Title   *string `json:"title"`
	Content *string `json:"content"`
}

// list returns all notes of the given user (never nil).
func (s *noteStore) list(userID string) []note {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userItems := s.items[userID]
	result := make([]note, 0, len(userItems))
	for _, item := range userItems {
		result = append(result, item)
	}
	return result
}

// create validates the title, stores a new note for the user and returns it.
func (s *noteStore) create(userID, title, content string) (note, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return note{}, errNoteTitleRequired
	}

	id, err := randomHex(8)
	if err != nil {
		return note{}, err
	}
	now := time.Now().UTC()
	item := note{
		ID:        id,
		Title:     title,
		Content:   strings.TrimSpace(content),
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[userID] == nil {
		s.items[userID] = make(map[string]note)
	}
	s.items[userID][id] = item
	return item, nil
}

// update applies a partial update to the user's note.
// A nil title/content keeps the existing value. An explicitly empty title
// is rejected with errNoteTitleRequired.
func (s *noteStore) update(userID, id string, title, content *string) (note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.items[userID][id]
	if !ok {
		return note{}, errNoteNotFound
	}

	changed := false
	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" {
			return note{}, errNoteTitleRequired
		}
		item.Title = trimmed
		changed = true
	}
	if content != nil {
		item.Content = strings.TrimSpace(*content)
		changed = true
	}
	if changed {
		item.UpdatedAt = time.Now().UTC()
		s.items[userID][id] = item
	}
	return item, nil
}

// delete removes the user's note. It returns errNoteNotFound when absent.
func (s *noteStore) delete(userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[userID][id]; !ok {
		return errNoteNotFound
	}
	delete(s.items[userID], id)
	return nil
}

// handleNotes routes GET/POST /v1/notes (list / create) for the authenticated user.
func handleNotes(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
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

// handleNoteByID routes PATCH/DELETE /v1/notes/{id} (update / delete).
func handleNoteByID(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
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

// handleNotesList writes the authenticated user's notes as {"items": [...]}.
func handleNotesList(w http.ResponseWriter, uid types.Uid) {
	writeJSONStatus(w, http.StatusOK, map[string]any{"items": notes.list(uid.UserId())})
}

// handleNotesCreate validates the request body and stores a new note.
func handleNotesCreate(w http.ResponseWriter, r *http.Request, uid types.Uid) {
	var req createNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	item, err := notes.create(uid.UserId(), req.Title, req.Content)
	if errors.Is(err, errNoteTitleRequired) {
		writeJSONError(w, http.StatusBadRequest, "title is required")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to generate note id")
		return
	}

	writeJSONStatus(w, http.StatusCreated, item)
}

// handleNotesUpdate applies a partial update to the user's note.
func handleNotesUpdate(w http.ResponseWriter, r *http.Request, uid types.Uid, id string) {
	var req updateNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	item, err := notes.update(uid.UserId(), id, req.Title, req.Content)
	if errors.Is(err, errNoteNotFound) {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	}
	if errors.Is(err, errNoteTitleRequired) {
		writeJSONError(w, http.StatusBadRequest, "title must not be empty")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSONStatus(w, http.StatusOK, item)
}

// handleNotesDelete removes the user's note, 404 when absent.
func handleNotesDelete(w http.ResponseWriter, uid types.Uid, id string) {
	if err := notes.delete(uid.UserId(), id); errors.Is(err, errNoteNotFound) {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
