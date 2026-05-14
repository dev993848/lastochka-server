//go:build ignore

// Минимальный тестовый сервер для проверки CORS
// Запуск: go run cors_test_server.go
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type CheckAvailabilityRequest struct {
	Login string `json:"login,omitempty"`
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

type CheckAvailabilityResponse struct {
	LoginAvailable bool   `json:"login_available"`
	EmailAvailable bool   `json:"email_available"`
	PhoneAvailable bool   `json:"phone_available"`
	Error          string `json:"error,omitempty"`
}

func handleCheckAvailability(w http.ResponseWriter, r *http.Request) {
	// CORS заголовки - разрешаем все origin для разработки
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

	// Простая проверка логина
	if req.Login != "" {
		if len(req.Login) < 3 {
			resp.LoginAvailable = false
			resp.Error = "Логин слишком короткий"
		} else if req.Login == "admin" || req.Login == "test" {
			resp.LoginAvailable = false
			resp.Error = "Этот логин уже занят"
		}
	}

	// Простая проверка email
	if req.Email != "" {
		if len(req.Email) < 3 || req.Email == "test@test.com" {
			resp.EmailAvailable = false
			resp.Error = "Этот email уже зарегистрирован"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/check-availability", handleCheckAvailability)

	log.Println("Test CORS server starting on :6060")
	log.Println("Test with:")
	log.Println("  curl -X OPTIONS http://localhost:6060/v1/check-availability -H \"Origin: http://localhost:5173\" -H \"Access-Control-Request-Method: POST\" -H \"Access-Control-Request-Headers: Content-Type\" -i")
	log.Fatal(http.ListenAndServe(":6060", mux))
}
