package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tinode/chat/server/logs"
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
	UUID       string
	SmsCode    string
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

	if !telvalidate.IsRedsmsConfigured() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "redsms wait-call is not configured"})
		return
	}

	callNumber, msgUUID, err := telvalidate.SendWaitCallNoCode(cred)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to send wait-call: " + err.Error()})
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
		UUID:       msgUUID,
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

	var req phonePreverifyResendSmsRequest
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
	if ok {
		code, err := generateSmsCode()
		if err != nil {
			phonePreverifyStore.mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate code"})
			return
		}
		entry.SmsCode = code
		phonePreverifyStore.entries[req.VerificationID] = entry
	}
	phonePreverifyStore.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "verification session expired"})
		return
	}

	smsText := "Ласточка: ваш код подтверждения — " + entry.SmsCode
	if err := telvalidate.SendSmsCode(entry.Phone, smsText); err != nil {
		logs.Warn.Println("phone preverify: SMS send error:", err)
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to send SMS"})
		return
	}

	logs.Info.Println("phone preverify: SMS code sent, phone:", entry.Phone)
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

	if req.Code != "" {
		if entry.SmsCode == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "sms code was not requested"})
			return
		}
		if req.Code != entry.SmsCode {
			logs.Warn.Println("phone preverify: wrong SMS code, phone:", entry.Phone)
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "неверный код"})
			return
		}
		logs.Info.Println("phone preverify: verified via SMS code, phone:", entry.Phone)

		phonePreverifyStore.mu.Lock()
		delete(phonePreverifyStore.entries, req.VerificationID)
		phonePreverifyStore.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(phonePreverifyConfirmResponse{
			Verified: true,
			Phone:    entry.Phone,
		})
		return
	}

	// Wait call without code: poll REDSMS status
	var status string
	var pollErr error
	for i := 0; i < 8; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		status, pollErr = telvalidate.CheckWaitCallStatus(entry.UUID)
		if pollErr != nil {
			logs.Warn.Println("phone preverify: REDSMS poll error:", pollErr)
			continue
		}
		logs.Info.Println("phone preverify: REDSMS status:", status, "uuid:", entry.UUID)
		if status == "wcall_delivered" {
			break
		}
		if status == "timeout" || status == "undelivered" || status == "error" {
			break
		}
	}
	if status != "wcall_delivered" {
		w.WriteHeader(http.StatusBadRequest)
		msg := "звонок не обнаружен. убедитесь, что вы позвонили с номера " + entry.Phone
		if pollErr != nil {
			msg = "ошибка проверки статуса звонка"
		}
		logs.Warn.Println("phone preverify: FAILED status:", status, "phone:", entry.Phone)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	logs.Info.Println("phone preverify: verified, phone:", entry.Phone)

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

func generateSmsCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}
