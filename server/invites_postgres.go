//go:build postgres
// +build postgres

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v4"
	"github.com/tinode/chat/server/db/postgres"
	"github.com/tinode/chat/server/store/types"
)

var errInviteNotFound = errors.New("invite not found")

type inviteConsumptionResult struct {
	InviteID        string
	InviterUserID   string
	SuspicionScore  int
	SuspicionReason []string
}

func inviteRegistrationRequired() bool {
	return true
}

func ensureInvitesStorage() error {
	db := postgres.CurrentDB()
	if db == nil {
		return errors.New("postgres connection is not initialized")
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS invites (
			id VARCHAR(32) PRIMARY KEY,
			code VARCHAR(64) NOT NULL UNIQUE,
			inviter_user_id VARCHAR(32) NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			max_uses INTEGER NOT NULL DEFAULT 1,
			use_count INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NULL,
			last_used_at TIMESTAMPTZ NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_inviter_updated_at ON invites(inviter_user_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS invite_registrations (
			invited_user_id VARCHAR(32) PRIMARY KEY,
			invited_by_user_id VARCHAR(32) NOT NULL,
			invite_id VARCHAR(32) NOT NULL,
			invite_code VARCHAR(64) NOT NULL,
			registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			suspicion_score INTEGER NOT NULL DEFAULT 0,
			suspicious BOOLEAN NOT NULL DEFAULT FALSE,
			last_signal_at TIMESTAMPTZ NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_invite_registrations_inviter ON invite_registrations(invited_by_user_id, registered_at DESC)`,
		`CREATE TABLE IF NOT EXISTS invite_registration_signals (
			id BIGSERIAL PRIMARY KEY,
			invited_user_id VARCHAR(32) NOT NULL,
			invite_id VARCHAR(32) NOT NULL,
			signal_type TEXT NOT NULL,
			details JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_invite_registration_signals_user ON invite_registration_signals(invited_user_id, created_at DESC)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(context.Background(), stmt); err != nil {
			return fmt.Errorf("init invites storage: %w", err)
		}
	}

	return nil
}

func listInvitesByUser(userID string) ([]inviteListItem, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return nil, errors.New("postgres connection is not initialized")
	}

	rows, err := db.Query(context.Background(), `
		SELECT id, code, status, max_uses, use_count, created_at, updated_at, expires_at, last_used_at, note
		FROM invites WHERE inviter_user_id = $1
		ORDER BY updated_at DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]inviteListItem, 0)
	for rows.Next() {
		var item inviteListItem
		var expiresAt sql.NullTime
		var lastUsedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.Code, &item.Status, &item.MaxUses, &item.UseCount, &item.CreatedAt, &item.UpdatedAt, &expiresAt, &lastUsedAt, &item.Note); err != nil {
			return nil, err
		}
		item.ExpiresAt = nullableTimePtr(expiresAt)
		item.LastUsedAt = nullableTimePtr(lastUsedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func createInviteRecord(userID string, req createInviteRequest) (invite, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return invite{}, errors.New("postgres connection is not initialized")
	}

	id, err := randomHex(8)
	if err != nil {
		return invite{}, err
	}
	code, err := randomHex(12)
	if err != nil {
		return invite{}, err
	}
	code = strings.ToUpper(code)

	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().UTC().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}

	item := invite{ID: id, Code: code, InviterUserID: userID, Status: "active", MaxUses: req.MaxUses, Note: req.Note}
	var dbExpiresAt sql.NullTime
	var lastUsedAt sql.NullTime
	err = db.QueryRow(context.Background(), `
		INSERT INTO invites (id, code, inviter_user_id, status, max_uses, note, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at, expires_at, last_used_at`,
		item.ID, item.Code, item.InviterUserID, item.Status, item.MaxUses, item.Note, expiresAt,
	).Scan(&item.CreatedAt, &item.UpdatedAt, &dbExpiresAt, &lastUsedAt)
	if err != nil {
		return invite{}, err
	}
	item.ExpiresAt = nullableTimePtr(dbExpiresAt)
	item.LastUsedAt = nullableTimePtr(lastUsedAt)
	return item, nil
}

func previewInviteByCode(code string) (invitePreview, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return invitePreview{}, errors.New("postgres connection is not initialized")
	}

	var preview invitePreview
	var expiresAt sql.NullTime
	var inviterDisplayName sql.NullString
	err := db.QueryRow(context.Background(), `
		SELECT i.code, COALESCE(u.public->>'fn', ''), i.status, i.max_uses, i.use_count, i.expires_at
		FROM invites i
		LEFT JOIN users u ON u.id::text = i.inviter_user_id
		WHERE i.code = $1`, strings.ToUpper(strings.TrimSpace(code)),
	).Scan(&preview.Code, &inviterDisplayName, &preview.Status, &preview.MaxUses, &preview.UseCount, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invitePreview{}, errInviteNotFound
		}
		return invitePreview{}, err
	}
	preview.InviterDisplayName = strings.TrimSpace(inviterDisplayName.String)
	preview.ExpiresAt = nullableTimePtr(expiresAt)
	preview.RemainingUses = preview.MaxUses - preview.UseCount
	if preview.RemainingUses < 0 {
		preview.RemainingUses = 0
	}
	now := time.Now().UTC()
	preview.Valid = preview.Status == "active" && preview.RemainingUses > 0 && (preview.ExpiresAt == nil || preview.ExpiresAt.UTC().After(now))
	if preview.ExpiresAt != nil && preview.ExpiresAt.UTC().Before(now) && preview.Status == "active" {
		preview.Status = "expired"
		preview.Valid = false
	}
	return preview, nil
}

func updateInviteRecord(userID, id string, req updateInviteRequest) (invite, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return invite{}, errors.New("postgres connection is not initialized")
	}

	ctx := context.Background()
	var item invite
	var expiresAt sql.NullTime
	var lastUsedAt sql.NullTime
	err := db.QueryRow(ctx, `
		SELECT id, code, inviter_user_id, status, max_uses, use_count, created_at, updated_at, expires_at, last_used_at, note
		FROM invites WHERE inviter_user_id = $1 AND id = $2`, userID, id,
	).Scan(&item.ID, &item.Code, &item.InviterUserID, &item.Status, &item.MaxUses, &item.UseCount, &item.CreatedAt, &item.UpdatedAt, &expiresAt, &lastUsedAt, &item.Note)
	if err != nil {
		return invite{}, errInviteNotFound
	}
	item.ExpiresAt = nullableTimePtr(expiresAt)
	item.LastUsedAt = nullableTimePtr(lastUsedAt)

	if req.Status != nil {
		item.Status = *req.Status
	}
	if req.MaxUses != nil {
		item.MaxUses = *req.MaxUses
	}
	if req.Note != nil {
		item.Note = *req.Note
	}
	if req.ExpiresAt != nil {
		trimmed := strings.TrimSpace(*req.ExpiresAt)
		if trimmed == "" {
			item.ExpiresAt = nil
		} else if parsed, parseErr := time.Parse(time.RFC3339, trimmed); parseErr == nil {
			parsed = parsed.UTC()
			item.ExpiresAt = &parsed
		} else {
			return invite{}, parseErr
		}
	}

	err = db.QueryRow(ctx, `
		UPDATE invites
		SET status = $3, max_uses = $4, note = $5, expires_at = $6, updated_at = NOW()
		WHERE inviter_user_id = $1 AND id = $2
		RETURNING updated_at, last_used_at`,
		userID, id, item.Status, item.MaxUses, item.Note, item.ExpiresAt,
	).Scan(&item.UpdatedAt, &lastUsedAt)
	if err != nil {
		return invite{}, err
	}
	item.LastUsedAt = nullableTimePtr(lastUsedAt)
	return item, nil
}

func revokeInviteRecord(userID, id string) error {
	db := postgres.CurrentDB()
	if db == nil {
		return errors.New("postgres connection is not initialized")
	}
	commandTag, err := db.Exec(context.Background(), `UPDATE invites SET status = 'revoked', updated_at = NOW() WHERE inviter_user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return errInviteNotFound
	}
	return nil
}

func getInviteGraphForUser(userID string) (map[string]any, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return nil, errors.New("postgres connection is not initialized")
	}

	rows, err := db.Query(context.Background(), `
		SELECT invited_user_id, invited_by_user_id, invite_id, invite_code, registered_at, suspicion_score, suspicious, last_signal_at
		FROM invite_registrations
		WHERE invited_by_user_id = $1 OR invited_user_id = $1
		ORDER BY registered_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := make([]inviteConnection, 0)
	for rows.Next() {
		var node inviteConnection
		var lastSignalAt sql.NullTime
		if err := rows.Scan(&node.UserID, &node.InvitedByUserID, &node.InviteID, &node.InviteCode, &node.RegisteredAt, &node.SuspicionScore, &node.Suspicious, &lastSignalAt); err != nil {
			return nil, err
		}
		node.LastSignalAt = nullableTimePtr(lastSignalAt)
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range nodes {
		reasons, err := listSignalReasons(nodes[i].UserID)
		if err != nil {
			return nil, err
		}
		nodes[i].SignalReasons = reasons
	}

	return map[string]any{"items": nodes}, nil
}

func consumeInviteForRegistration(inviteCode string, s *Session, user any, creds []MsgCredClient) (inviteConsumptionResult, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return inviteConsumptionResult{}, errors.New("postgres connection is not initialized")
	}
	targetUser, ok := user.(*types.User)
	if !ok {
		return inviteConsumptionResult{}, errors.New("invalid user payload")
	}

	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		return inviteConsumptionResult{}, err
	}
	defer tx.Rollback(ctx)

	var inviteID string
	var inviterUserID string
	var status string
	var maxUses int
	var useCount int
	var expiresAt sql.NullTime
	err = tx.QueryRow(ctx, `
		SELECT id, inviter_user_id, status, max_uses, use_count, expires_at
		FROM invites WHERE code = $1 FOR UPDATE`, strings.ToUpper(strings.TrimSpace(inviteCode)),
	).Scan(&inviteID, &inviterUserID, &status, &maxUses, &useCount, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return inviteConsumptionResult{}, errors.New("invite_not_found")
		}
		return inviteConsumptionResult{}, err
	}

	now := time.Now().UTC()
	if status != "active" {
		return inviteConsumptionResult{}, errors.New("invite_inactive")
	}
	if expiresAt.Valid && expiresAt.Time.UTC().Before(now) {
		_, _ = tx.Exec(ctx, `UPDATE invites SET status = 'expired', updated_at = NOW() WHERE id = $1`, inviteID)
		return inviteConsumptionResult{}, errors.New("invite_expired")
	}
	if useCount >= maxUses {
		return inviteConsumptionResult{}, errors.New("invite_exhausted")
	}
	if inviterUserID == targetUser.Uid().UserId() {
		return inviteConsumptionResult{}, errors.New("self_invite_not_allowed")
	}

	suspicionScore, reasons, details := assessRegistrationSuspicion(ctx, tx, inviterUserID, s, targetUser, creds)
	_, err = tx.Exec(ctx, `
		INSERT INTO invite_registrations (invited_user_id, invited_by_user_id, invite_id, invite_code, registered_at, suspicion_score, suspicious, last_signal_at)
		VALUES ($1, $2, $3, $4, NOW(), $5, $6, $7)`,
		targetUser.Uid().UserId(), inviterUserID, inviteID, strings.ToUpper(strings.TrimSpace(inviteCode)), suspicionScore, suspicionScore > 0, nullableNow(suspicionScore > 0),
	)
	if err != nil {
		return inviteConsumptionResult{}, err
	}

	for _, reason := range reasons {
		if _, err := tx.Exec(ctx, `
			INSERT INTO invite_registration_signals (invited_user_id, invite_id, signal_type, details)
			VALUES ($1, $2, $3, $4)`,
			targetUser.Uid().UserId(), inviteID, reason, details[reason],
		); err != nil {
			return inviteConsumptionResult{}, err
		}
	}

	newUseCount := useCount + 1
	newStatus := status
	if newUseCount >= maxUses {
		newStatus = "expired"
	}
	_, err = tx.Exec(ctx, `
		UPDATE invites SET use_count = $2, status = $3, last_used_at = NOW(), updated_at = NOW()
		WHERE id = $1`, inviteID, newUseCount, newStatus,
	)
	if err != nil {
		return inviteConsumptionResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return inviteConsumptionResult{}, err
	}

	return inviteConsumptionResult{
		InviteID:        inviteID,
		InviterUserID:   inviterUserID,
		SuspicionScore:  suspicionScore,
		SuspicionReason: reasons,
	}, nil
}

func assessRegistrationSuspicion(ctx context.Context, tx interface {
	QueryRow(context.Context, string, ...interface{}) pgx.Row
	}, inviterUserID string, s *Session, user *types.User, creds []MsgCredClient) (int, []string, map[string]string) {
	score := 0
	reasons := make([]string, 0, 3)
	details := make(map[string]string)

	var recentCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM invite_registrations WHERE invited_by_user_id = $1 AND registered_at >= NOW() - INTERVAL '24 hours'`, inviterUserID).Scan(&recentCount); err == nil && recentCount >= 5 {
		score += 2
		reasons = append(reasons, "high_invite_velocity")
		details["high_invite_velocity"] = fmt.Sprintf(`{"count":%d}`, recentCount)
	}

	remoteIP := strings.TrimSpace(s.remoteAddr)
	if remoteIP != "" {
		var sameIPCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM invite_registration_signals WHERE signal_type = 'shared_remote_addr' AND details->>'remote_addr' = $1 AND created_at >= NOW() - INTERVAL '24 hours'`, remoteIP).Scan(&sameIPCount); err == nil && sameIPCount >= 2 {
			score += 3
			reasons = append(reasons, "shared_remote_addr")
			details["shared_remote_addr"] = fmt.Sprintf(`{"remote_addr":%q,"recent_count":%d}`, remoteIP, sameIPCount+1)
		}
	}

	if phone := normalizedCredentialValue(creds, "tel"); phone != "" {
		if looksSyntheticPhone(phone) {
			score++
			reasons = append(reasons, "synthetic_phone_pattern")
			details["synthetic_phone_pattern"] = fmt.Sprintf(`{"phone":%q}`, phone)
		}
	}

	if name := extractDisplayName(user.Public); looksSyntheticName(name) {
		score++
		reasons = append(reasons, "synthetic_profile_name")
		details["synthetic_profile_name"] = fmt.Sprintf(`{"name":%q}`, name)
	}

	sort.Strings(reasons)
	return score, reasons, details
}

func listSignalReasons(userID string) ([]string, error) {
	db := postgres.CurrentDB()
	if db == nil {
		return nil, errors.New("postgres connection is not initialized")
	}
	rows, err := db.Query(context.Background(), `SELECT DISTINCT signal_type FROM invite_registration_signals WHERE invited_user_id = $1 ORDER BY signal_type ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reasons []string
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil {
			return nil, err
		}
		reasons = append(reasons, reason)
	}
	return reasons, rows.Err()
}

func nullableNow(valid bool) *time.Time {
	if !valid {
		return nil
	}
	now := time.Now().UTC()
	return &now
}

func normalizedCredentialValue(creds []MsgCredClient, method string) string {
	for _, cred := range creds {
		if cred.Method == method {
			return strings.TrimSpace(strings.ToLower(cred.Value))
		}
	}
	return ""
}

func extractDisplayName(public any) string {
	pub, _ := public.(map[string]any)
	if pub == nil {
		return ""
	}
	name, _ := pub["fn"].(string)
	return strings.TrimSpace(name)
}

func looksSyntheticPhone(phone string) bool {
	if len(phone) < 6 {
		return false
	}
	repeated := true
	for i := 1; i < len(phone); i++ {
		if phone[i] != phone[0] {
			repeated = false
			break
		}
	}
	if repeated {
		return true
	}
	if strings.Contains(phone, "0000") || strings.Contains(phone, "12345") {
		return true
	}
	return false
}

func looksSyntheticName(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return true
	}
	if strings.HasPrefix(name, "test") || strings.HasPrefix(name, "user") || strings.HasPrefix(name, "qwerty") {
		return true
	}
	return false
}
