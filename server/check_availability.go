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
	Login string `json:"login,omitempty"`
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

// CheckAvailabilityResponse - ответ проверки доступности
type CheckAvailabilityResponse struct {
	LoginAvailable bool   `json:"login_available"`
	EmailAvailable bool   `json:"email_available"`
	PhoneAvailable bool   `json:"phone_available"`
	Error          string `json:"error,omitempty"`
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
		LoginAvailable: true,
		EmailAvailable: true,
		PhoneAvailable: true,
	}

	// Проверка логина
	if req.Login != "" {
		login := strings.TrimSpace(req.Login)
		if len(login) < 3 {
			resp.LoginAvailable = false
			resp.Error = "Логин слишком короткий"
			writeJSON(w, resp)
			return
		}

		// Проверяем наличие в базе
		found, err := getUserByLogin(login)
		if err != nil {
			logs.Warn.Printf("Ошибка проверки логина '%s': %v", login, err)
			resp.LoginAvailable = false
			resp.Error = "Ошибка проверки логина"
			writeJSON(w, resp)
			return
		}
		if found != nil {
			resp.LoginAvailable = false
			resp.Error = "Этот логин уже занят"
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

// getUserByLogin - поиск пользователя по логину (basic auth)
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
