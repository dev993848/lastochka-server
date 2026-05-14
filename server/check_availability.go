package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// CheckAvailabilityRequest - запрос на проверку доступности
type CheckAvailabilityRequest struct {
	// Deprecated: use PublicAddress.
	Login string `json:"login,omitempty"`
	// Public address used for user addressing and search (maps to public.uname).
	PublicAddress string `json:"public_address,omitempty"`
	Email         string `json:"email,omitempty"`
	Phone         string `json:"phone,omitempty"`
}

// CheckAvailabilityResponse - ответ проверки доступности
type CheckAvailabilityResponse struct {
	// Deprecated: use PublicAddressAvailable.
	LoginAvailable bool `json:"login_available"`
	// Availability of public_address (public.uname).
	PublicAddressAvailable bool   `json:"public_address_available"`
	EmailAvailable         bool   `json:"email_available"`
	PhoneAvailable         bool   `json:"phone_available"`
	Error                  string `json:"error,omitempty"`
}

// handleCheckAvailability - HTTP handler для проверки доступности
// POST /v1/check-availability
// Body: {"login": "username", "email": "user@example.com", "phone": "79991234567"}
// Response: {"login_available": true, "email_available": false, "phone_available": true}
func handleCheckAvailability(w http.ResponseWriter, r *http.Request) {
	// CORS заголовки - разрешаем все origin для разработки
	// В продакшене нужно ограничить до конкретного домена
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Обработка preflight запроса
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

	var req CheckAvailabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid request body"}`))
		return
	}

	resp := CheckAvailabilityResponse{
		LoginAvailable:         true,
		PublicAddressAvailable: true,
		EmailAvailable:         true,
		PhoneAvailable:         true,
	}

	publicAddress := strings.TrimSpace(strings.ToLower(req.PublicAddress))
	// Backward compatibility for old clients sending "login".
	if publicAddress == "" {
		publicAddress = strings.TrimSpace(strings.ToLower(req.Login))
	}

	// Проверка публичного адреса
	if publicAddress != "" {
		if _, ok := normalizePublicAddress(publicAddress); !ok {
			resp.PublicAddressAvailable = false
			resp.LoginAvailable = false
			resp.Error = "Неверный формат публичного адреса"
			writeJSON(w, resp)
			return
		}

		// Проверяем наличие в базе через индексируемый tag namespace uname:*.
		// Важно: для корректной работы существующие пользователи должны быть backfilled.
		found, err := getUserByPublicAddress(publicAddress)
		if err != nil {
			logs.Warn.Printf("Ошибка проверки public_address '%s': %v", publicAddress, err)
			resp.PublicAddressAvailable = false
			resp.LoginAvailable = false
			resp.Error = "Ошибка проверки публичного адреса"
			writeJSON(w, resp)
			return
		}
		if found != "" {
			resp.PublicAddressAvailable = false
			resp.LoginAvailable = false
			resp.Error = "Этот публичный адрес уже занят"
		}
	}

	// Проверка email
	if req.Email != "" {
		email := strings.TrimSpace(strings.ToLower(req.Email))

		if !isValidEmail(email) {
			resp.EmailAvailable = false
			resp.Error = "Неверный формат email"
			writeJSON(w, resp)
			return
		}

		exists, err := store.Users.CredExists("email", email)
		if err != nil {
			logs.Warn.Printf("Ошибка проверки email '%s': %v", email, err)
			resp.EmailAvailable = false
			resp.Error = "Ошибка проверки email"
			writeJSON(w, resp)
			return
		}
		if exists {
			resp.EmailAvailable = false
			resp.Error = "Этот email уже зарегистрирован"
		}
	}

	// Проверка телефона
	if req.Phone != "" {
		phone := strings.TrimSpace(req.Phone)

		exists, err := store.Users.CredExists("tel", phone)
		if err != nil {
			logs.Warn.Printf("Ошибка проверки телефона '%s': %v", phone, err)
			resp.PhoneAvailable = false
			resp.Error = "Ошибка проверки телефона"
			writeJSON(w, resp)
			return
		}
		if exists {
			resp.PhoneAvailable = false
			if resp.Error == "" {
				resp.Error = "Этот номер уже зарегистрирован"
			}
		}
	}

	writeJSON(w, resp)
}

// isValidEmail - простая валидация email
func isValidEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 || at > len(email)-3 {
		return false
	}
	if strings.Index(email[at+1:], ".") < 1 {
		return false
	}
	return true
}

// getUserByPublicAddress - поиск пользователя по тегу `uname:<public_address>`.
// Возвращает UID-подобный user id (usr...) либо пустую строку.
func getUserByPublicAddress(address string) (string, error) {
	return store.Users.FindOne("uname:" + address)
}

// getUserByLogin - поиск пользователя по логину (basic auth)
// Deprecated: для публичного адреса используйте getUserByPublicAddress.
func getUserByLogin(login string) (*types.User, error) {
	uid, _, _, _, err := store.Users.GetAuthUniqueRecord("basic", login)
	if err != nil {
		if err == types.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if uid.IsZero() {
		return nil, nil
	}
	users, err := store.Users.GetAll(uid)
	if err != nil || len(users) == 0 {
		return nil, err
	}
	return &users[0], nil
}

// writeJSON - запись JSON ответа
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
