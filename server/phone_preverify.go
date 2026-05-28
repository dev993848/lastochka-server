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
	telvalidate "github.com/tinode/chat/server/validate/tel"
)

type phonePreverifyStartRequest struct {
	Phone       string `json:"phone"`
	CountryCode string `json:"country_code,omitempty"`
}

type phonePreverifyStartResponse struct {
	VerificationID string `json:"verification_id"`
	ExpiresIn      int64  `json:"expires_in"`
	CallNumber     string `json:"call_number,omitempty"`
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

	if !telvalidate.IsRedsmsConfigured() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "redsms wait-call is not configured"})
		return
	}

	callNumber, err := telvalidate.SendWaitCall(cred, code)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to send wait-call"})
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
		CallNumber:     callNumber,
	})
}

func handlePhonePreverifyResendSms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}
	w.WriteHeader(http.StatusGone)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "sms fallback is disabled; use wait-call flow"})
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
