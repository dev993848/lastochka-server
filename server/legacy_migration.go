package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	stypes "github.com/tinode/chat/server/store/types"
)

const (
	legacyMigrationLimit       = 10
	legacyMigrationWindow      = 10 * time.Minute
	legacyMigrationUnknownAddr = "unknown"
)

type endpointRateState struct {
	Count   int
	ResetAt time.Time
}

type endpointRateLimiter struct {
	mu      sync.Mutex
	entries map[string]endpointRateState
}

var legacyMigrationLimiter = &endpointRateLimiter{
	entries: make(map[string]endpointRateState),
}

func (l *endpointRateLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.entries[key]
	if !ok || !now.Before(state.ResetAt) {
		l.entries[key] = endpointRateState{
			Count:   1,
			ResetAt: now.Add(legacyMigrationWindow),
		}
		return true, 0
	}

	if state.Count >= legacyMigrationLimit {
		return false, time.Until(state.ResetAt)
	}

	state.Count++
	l.entries[key] = state
	return true, 0
}

// LegacyPhoneMigrationRequest describes a request to migrate a legacy account to phone login.
type LegacyPhoneMigrationRequest struct {
	LegacyLogin    string `json:"legacy_login"`
	LegacyPassword string `json:"legacy_password"`
	Phone          string `json:"phone"`
}

// LegacyPhoneMigrationResponse is returned by the legacy migration endpoint.
type LegacyPhoneMigrationResponse struct {
	Success    bool   `json:"success"`
	PhoneLogin string `json:"phone_login,omitempty"`
	Error      string `json:"error,omitempty"`
}

// handleLegacyPhoneMigration verifies legacy login/password and binds a phone as canonical basic login.
// POST /v1/legacy/migrate-phone
func handleLegacyPhoneMigration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error":"method not allowed"}`))
		return
	}

	rateKey := migrationRateLimitKey(getRemoteAddr(r))
	if allowed, retryIn := legacyMigrationLimiter.allow(rateKey, time.Now()); !allowed {
		retry := int(retryIn.Seconds())
		if retry < 1 {
			retry = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
		w.WriteHeader(http.StatusTooManyRequests)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Слишком много попыток. Повторите позже."})
		return
	}

	var req LegacyPhoneMigrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid request body"}`))
		return
	}

	legacyLogin := strings.ToLower(strings.TrimSpace(req.LegacyLogin))
	legacyPassword := req.LegacyPassword
	phoneLogin := normalizeRussianPhone(req.Phone)

	if legacyLogin == "" || legacyPassword == "" || phoneLogin == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Заполните все поля"})
		return
	}
	if !isValidRussianMobilePhone(phoneLogin) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Неверный формат телефона"})
		return
	}

	e164Phone := "+" + phoneLogin

	phoneTaken, err := phoneCredentialExists(phoneLogin)
	if err != nil {
		logs.Warn.Printf("legacy migration: phone exists check failed for %s: %v", e164Phone, err)
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Ошибка сервера"})
		return
	}
	if phoneTaken {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Этот номер уже зарегистрирован"})
		return
	}

	uid, authLvl, passhash, expires, err := store.Users.GetAuthUniqueRecord("basic", legacyLogin)
	if err != nil {
		if err == stypes.ErrNotFound {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Неверные данные"})
			return
		}
		logs.Warn.Printf("legacy migration: failed to read auth record for %s: %v", legacyLogin, err)
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Ошибка сервера"})
		return
	}
	if uid.IsZero() {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Неверные данные"})
		return
	}
	if !expires.IsZero() && expires.Before(time.Now()) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Неверные данные"})
		return
	}

	if err := bcrypt.CompareHashAndPassword(passhash, []byte(legacyPassword)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Неверные данные"})
		return
	}

	if err := ensurePhoneLoginIsFreeOrOwned(phoneLogin, uid); err != nil {
		if err == stypes.ErrDuplicate {
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Этот номер уже зарегистрирован"})
			return
		}
		logs.Warn.Printf("legacy migration: phone login uniqueness check failed for %s: %v", phoneLogin, err)
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Ошибка сервера"})
		return
	}

	if err := store.Users.UpdateAuthRecord(uid, authLvl, "basic", phoneLogin, passhash, expires); err != nil {
		if err == stypes.ErrDuplicate {
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Этот номер уже зарегистрирован"})
			return
		}
		logs.Warn.Printf("legacy migration: failed to update basic login for uid=%s to %s: %v", uid.String(), phoneLogin, err)
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Ошибка сервера"})
		return
	}

	if _, err := store.Users.UpsertCred(&stypes.Credential{
		User:   uid.String(),
		Method: "tel",
		Value:  e164Phone,
		Done:   true,
	}); err != nil {
		if err == stypes.ErrDuplicate {
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Этот номер уже зарегистрирован"})
			return
		}
		logs.Warn.Printf("legacy migration: failed to upsert tel credential uid=%s phone=%s: %v", uid.String(), e164Phone, err)
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, LegacyPhoneMigrationResponse{Success: false, Error: "Ошибка сервера"})
		return
	}

	if err := store.Users.ConfirmCred(uid, "tel"); err != nil && err != stypes.ErrNotFound {
		logs.Warn.Printf("legacy migration: confirm tel credential uid=%s failed: %v", uid.String(), err)
	}

	logs.Info.Printf("legacy migration succeeded: uid=%s legacy=%s phone=%s", uid.String(), legacyLogin, phoneLogin)
	writeJSON(w, LegacyPhoneMigrationResponse{Success: true, PhoneLogin: phoneLogin})
}

func normalizeRussianPhone(value string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.TrimSpace(value))

	if strings.HasPrefix(digits, "8") {
		digits = "7" + digits[1:]
	}
	if len(digits) == 10 {
		digits = "7" + digits
	}
	return digits
}

func isValidRussianMobilePhone(phone string) bool {
	if len(phone) != 11 {
		return false
	}
	return strings.HasPrefix(phone, "79")
}

func phoneCredentialExists(phoneLogin string) (bool, error) {
	e164 := "+" + phoneLogin
	exists, err := store.Users.CredExists("tel", e164)
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}
	// Compatibility fallback for old records without '+'.
	return store.Users.CredExists("tel", phoneLogin)
}

func ensurePhoneLoginIsFreeOrOwned(phoneLogin string, ownerUID stypes.Uid) error {
	uid, _, _, _, err := store.Users.GetAuthUniqueRecord("basic", phoneLogin)
	if err != nil && err != stypes.ErrNotFound {
		return err
	}
	if uid.IsZero() || uid == ownerUID {
		return nil
	}
	return stypes.ErrDuplicate
}

func migrationRateLimitKey(remoteAddr string) string {
	raw := strings.TrimSpace(remoteAddr)
	if raw == "" {
		return legacyMigrationUnknownAddr
	}
	if comma := strings.Index(raw, ","); comma >= 0 {
		raw = strings.TrimSpace(raw[:comma])
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	if raw == "" {
		return legacyMigrationUnknownAddr
	}
	return raw
}
