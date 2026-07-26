package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store/types"
)

type invite struct {
	ID                  string     `json:"id"`
	Code                string     `json:"code,omitempty"`
	InviterUserID       string     `json:"inviter_user_id"`
	InviterDisplayName  string     `json:"inviter_display_name,omitempty"`
	Status              string     `json:"status"`
	MaxUses             int        `json:"max_uses"`
	UseCount            int        `json:"use_count"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`
	Note                string     `json:"note,omitempty"`
}

type inviteListItem struct {
	ID                 string     `json:"id"`
	Code               string     `json:"code"`
	InviterDisplayName string     `json:"inviter_display_name,omitempty"`
	Status             string     `json:"status"`
	MaxUses            int        `json:"max_uses"`
	UseCount           int        `json:"use_count"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	Note               string     `json:"note,omitempty"`
}

type createInviteRequest struct {
	MaxUses       int    `json:"max_uses"`
	ExpiresInDays int    `json:"expires_in_days"`
	Note          string `json:"note"`
}

type updateInviteRequest struct {
	Status    *string `json:"status"`
	MaxUses   *int    `json:"max_uses"`
	Note      *string `json:"note"`
	ExpiresAt *string `json:"expires_at"`
}

type inviteConnection struct {
	UserID              string     `json:"user_id"`
	InvitedByUserID     string     `json:"invited_by_user_id,omitempty"`
	InviteID            string     `json:"invite_id,omitempty"`
	InviteCode          string     `json:"invite_code,omitempty"`
	RegisteredAt        time.Time  `json:"registered_at"`
	SuspicionScore      int        `json:"suspicion_score"`
	Suspicious          bool       `json:"suspicious"`
	LastSignalAt        *time.Time `json:"last_signal_at,omitempty"`
	SignalReasons       []string   `json:"signal_reasons,omitempty"`
	DirectInviteCount   int        `json:"direct_invite_count,omitempty"`
	DirectSuspicionHits int        `json:"direct_suspicion_hits,omitempty"`
}

type invitePreview struct {
	Code               string     `json:"code"`
	InviterDisplayName string     `json:"inviter_display_name,omitempty"`
	Status             string     `json:"status"`
	MaxUses            int        `json:"max_uses"`
	UseCount           int        `json:"use_count"`
	RemainingUses      int        `json:"remaining_uses"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	Valid              bool       `json:"valid"`
}

func handleInvites(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleInvitesList(w, uid)
	case http.MethodPost:
		handleInvitesCreate(w, r, uid)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleInviteByID(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
	if !ok {
		return
	}

	idx := strings.Index(r.URL.Path, "/v1/invites/")
	if idx < 0 {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	path := strings.Trim(strings.TrimSpace(r.URL.Path[idx+len("/v1/invites/"):]), "/")
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
		handleInvitesUpdate(w, r, uid, id)
	case action == "" && r.Method == http.MethodDelete:
		handleInvitesDelete(w, uid, id)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func handleInviteGraph(w http.ResponseWriter, r *http.Request) {
	uid, ok := authenticateUserRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	graph, err := getInviteGraphForUser(uid.UserId())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, graph)
}

func handleInvitePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("code")))
	if code == "" {
		writeJSONError(w, http.StatusBadRequest, "invite code is required")
		return
	}
	preview, err := previewInviteByCode(code)
	if err == errInviteNotFound {
		writeJSONError(w, http.StatusNotFound, "invite not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, preview)
}

func handleInvitesList(w http.ResponseWriter, uid types.Uid) {
	items, err := listInvitesByUser(uid.UserId())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"items": items})
}

func handleInvitesCreate(w http.ResponseWriter, r *http.Request, uid types.Uid) {
	var req createInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MaxUses <= 0 {
		req.MaxUses = 1
	}
	if req.MaxUses > 20 {
		writeJSONError(w, http.StatusBadRequest, "max_uses must be between 1 and 20")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 90 {
		writeJSONError(w, http.StatusBadRequest, "expires_in_days must be between 0 and 90")
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if len(req.Note) > 500 {
		writeJSONError(w, http.StatusBadRequest, "note is too long")
		return
	}

	item, err := createInviteRecord(uid.UserId(), req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusCreated, item)
}

func handleInvitesUpdate(w http.ResponseWriter, r *http.Request, uid types.Uid, id string) {
	var req updateInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != nil {
		status := strings.TrimSpace(strings.ToLower(*req.Status))
		if status != "active" && status != "revoked" && status != "expired" {
			writeJSONError(w, http.StatusBadRequest, "status must be active, revoked or expired")
			return
		}
		*req.Status = status
	}
	if req.MaxUses != nil && (*req.MaxUses < 1 || *req.MaxUses > 20) {
		writeJSONError(w, http.StatusBadRequest, "max_uses must be between 1 and 20")
		return
	}
	if req.Note != nil {
		note := strings.TrimSpace(*req.Note)
		if len(note) > 500 {
			writeJSONError(w, http.StatusBadRequest, "note is too long")
			return
		}
		*req.Note = note
	}

	item, err := updateInviteRecord(uid.UserId(), id, req)
	if err == errInviteNotFound {
		writeJSONError(w, http.StatusNotFound, "invite not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, item)
}

func handleInvitesDelete(w http.ResponseWriter, uid types.Uid, id string) {
	if err := revokeInviteRecord(uid.UserId(), id); err == errInviteNotFound {
		writeJSONError(w, http.StatusNotFound, "invite not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"success": true})
}

func inviteCodeFromAccountCreate(msg *ClientComMessage) string {
	if msg == nil || msg.Acc == nil {
		return ""
	}
	for _, tag := range msg.Acc.Tags {
		tag = strings.TrimSpace(tag)
		if strings.HasPrefix(tag, "invite:") {
			return strings.TrimSpace(strings.TrimPrefix(tag, "invite:"))
		}
	}
	return ""
}

func enforceInviteForRegistration(s *Session, msg *ClientComMessage, user *types.User, creds []MsgCredClient) bool {
	if !inviteRegistrationRequired() {
		return true
	}

	inviteCode := inviteCodeFromAccountCreate(msg)
	if inviteCode == "" {
		s.queueOut(decodeStoreError(types.ErrPolicy, msg.Id, msg.Timestamp, map[string]any{
			"what":  "invite",
			"error": "invite_required",
		}))
		return false
	}

	result, err := consumeInviteForRegistration(inviteCode, s, user, creds)
	if err != nil {
		logs.Warn.Println("create user: invite check failed", err, "sid=", s.sid)
		s.queueOut(decodeStoreError(types.ErrPolicy, msg.Id, msg.Timestamp, map[string]any{
			"what":  "invite",
			"error": err.Error(),
		}))
		return false
	}

	user.Tags = normalizeTags(append(user.Tags, buildInviteDerivedTags(result)...), globals.maxTagCount)
	return true
}

func buildInviteDerivedTags(result inviteConsumptionResult) []string {
	tags := []string{"reg:invite"}
	if result.SuspicionScore > 0 {
		tags = append(tags, "reg:suspicious")
	}
	return tags
}
