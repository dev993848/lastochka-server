package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tinode/chat/server/store"
)

type phonePreverifyStartRequest struct {
	Phone       string `json:"phone"`
	CountryCode string `json:"country_code,omitempty"`
}

type phonePreverifyStartResponse struct {
	VerificationID string `json:"verification_id"`
	ExpiresIn      int64  `json:"expires_in"`
}

type phonePreverifyResendSmsRequest struct {
	VerificationID string `json:"verification_id"`
}

type phonePreverifyConfirmRequest struct {
	VerificationID string `json:"verification_id"`
	Code           string `json:"code"`
}

type phonePreverifyConfirmResponse struct {
	Verified bool   `json:"verified"`
	Phone    string `json:"phone"`
}

type phonePreverifyEntry struct {
	Credential string
	Phone      string
	Code       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

var phonePreverifyStore = struct {
	mu      sync.Mutex
	entries map[string]phonePreverifyEntry
}{
	entries: make(map[string]phonePreverifyEntry),
}

const phonePreverifyTTL = 10 * time.Minute
const phonePreverifyCodeLen = 4
const phonePreverifySmsFallbackDelay = 60 * time.Second

func handlePhonePreverifyStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	var req phonePreverifyStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	validator := store.Store.GetValidator("tel")
	if validator == nil || !validator.IsInitialized() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "tel validator is not initialized"})
		return
	}

	params := map[string]any{}
	if cc := strings.TrimSpace(req.CountryCode); cc != "" {
		params["countryCode"] = cc
	}

	tag, err := validator.PreCheck(strings.TrimSpace(req.Phone), params)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid phone format"})
		return
	}
	cred := strings.TrimPrefix(tag, "tel:")
	if cred == tag {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to normalize phone"})
		return
	}

	code, err := randomNumericCode(phonePreverifyCodeLen)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate verification code"})
		return
	}

	if err := validator.ResetSecret(cred, "basic", "ru", []byte(code), nil); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to send SMS"})
		return
	}

	verificationID, err := randomHex(16)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to create verification session"})
		return
	}

	now := time.Now()
	expiresAt := now.Add(phonePreverifyTTL)
	phonePreverifyStore.mu.Lock()
	for id, e := range phonePreverifyStore.entries {
		if e.ExpiresAt.Before(now) {
			delete(phonePreverifyStore.entries, id)
		}
	}
	phonePreverifyStore.entries[verificationID] = phonePreverifyEntry{
		Credential: "tel:" + cred,
		Phone:      cred,
		Code:       code,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
	}
	phonePreverifyStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(phonePreverifyStartResponse{
		VerificationID: verificationID,
		ExpiresIn:      int64(phonePreverifyTTL / time.Second),
	})
}

func handlePhonePreverifyResendSms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	var req phonePreverifyResendSmsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	verificationID := strings.TrimSpace(req.VerificationID)
	if verificationID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "verification_id is required"})
		return
	}

	phonePreverifyStore.mu.Lock()
	entry, ok := phonePreverifyStore.entries[verificationID]
	if ok && entry.ExpiresAt.Before(time.Now()) {
		delete(phonePreverifyStore.entries, verificationID)
		ok = false
	}
	phonePreverifyStore.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "verification session expired"})
		return
	}

	if wait := time.Until(entry.CreatedAt.Add(phonePreverifySmsFallbackDelay)); wait > 0 {
		retryAfter := int(wait.Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":                "sms fallback is not available yet",
			"retry_after_seconds": retryAfter,
		})
		return
	}

	validator := store.Store.GetValidator("tel")
	if validator == nil || !validator.IsInitialized() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "tel validator is not initialized"})
		return
	}

	if err := validator.ResetSecret(entry.Phone, "basic", "ru", []byte(entry.Code), map[string]any{"route": "sms"}); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to send SMS"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func handlePhonePreverifyConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	var req phonePreverifyConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	phonePreverifyStore.mu.Lock()
	entry, ok := phonePreverifyStore.entries[req.VerificationID]
	if ok && entry.ExpiresAt.Before(time.Now()) {
		delete(phonePreverifyStore.entries, req.VerificationID)
		ok = false
	}
	phonePreverifyStore.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "verification session expired"})
		return
	}

	code := strings.TrimSpace(req.Code)
	if subtle.ConstantTimeCompare([]byte(code), []byte(entry.Code)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid verification code"})
		return
	}

	phonePreverifyStore.mu.Lock()
	delete(phonePreverifyStore.entries, req.VerificationID)
	phonePreverifyStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(phonePreverifyConfirmResponse{
		Verified: true,
		Phone:    entry.Phone,
	})
}

func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomNumericCode(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("invalid code length")
	}
	max := byte(10)
	out := make([]byte, length)
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := 0; i < length; i++ {
		out[i] = '0' + (buf[i] % max)
	}
	return string(out), nil
}
