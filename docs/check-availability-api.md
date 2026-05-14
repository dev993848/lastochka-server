# API доступности аккаунтов

## Обзор

Эндпоинт для проверки доступности логина, email и телефона при регистрации.

**Важно:** Этот эндпоинт **не требует аутентификации** и может вызываться до входа в систему.

## Эндпоинт

```
POST /v1/check-availability
```

## Запрос

### Headers

```
Content-Type: application/json
```

### Body

```json
{
  "login": "username",
  "email": "user@example.com",
  "phone": "79991234567"
}
```

**Параметры:**

| Поле | Тип | Обязательное | Описание |
|------|-----|--------------|----------|
| `login` | string | Нет | Логин пользователя (3-32 символа) |
| `email` | string | Нет | Email адрес |
| `phone` | string | Нет | Номер телефона в формате 79991234567 |

**Примечание:** Хотя бы одно поле должно быть указано.

## Ответ

### Успех (200 OK)

```json
{
  "login_available": true,
  "email_available": false,
  "phone_available": true,
  "error": "Этот email уже зарегистрирован"
}
```

**Параметры:**

| Поле | Тип | Описание |
|------|-----|----------|
| `login_available` | boolean | true если логин свободен |
| `email_available` | boolean | true если email свободен |
| `phone_available` | boolean | true если телефон свободен |
| `error` | string | Сообщение об ошибке (если есть) |

### Ошибки

| Код | Описание |
|-----|----------|
| 400 | Неверный формат запроса |
| 405 | Метод не поддерживается (только POST) |
| 500 | Внутренняя ошибка сервера |

## Примеры использования

### JavaScript/TypeScript

```typescript
async function checkAvailability(login: string) {
  const response = await fetch('http://localhost:6060/v1/check-availability', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ login }),
  })
  
  const data = await response.json()
  
  if (!data.login_available) {
    console.error('Логин занят:', data.error)
  }
  
  return data.login_available
}
```

### cURL

```bash
# Проверка логина
curl -X POST http://localhost:6060/v1/check-availability \
  -H "Content-Type: application/json" \
  -d '{"login":"anton"}'

# Проверка email
curl -X POST http://localhost:6060/v1/check-availability \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com"}'

# Проверка всех полей
curl -X POST http://localhost:6060/v1/check-availability \
  -H "Content-Type: application/json" \
  -d '{"login":"anton","email":"user@example.com","phone":"79991234567"}'
```

### Python

```python
import requests

def check_availability(login=None, email=None, phone=None):
    url = 'http://localhost:6060/v1/check-availability'
    data = {}
    
    if login:
        data['login'] = login
    if email:
        data['email'] = email
    if phone:
        data['phone'] = phone
    
    response = requests.post(url, json=data)
    return response.json()

# Пример
result = check_availability(login='anton')
print(f"Логин свободен: {result['login_available']}")
```

## Валидация

### Логин

- **Минимальная длина:** 3 символа
- **Максимальная длина:** 32 символа
- **Допустимые символы:** буквы (a-z, A-Z), цифры (0-9), подчёркивание (_)
- **Проверка:** регистронезависимая

### Email

- **Формат:** стандартный email (RFC 5322)
- **Проверка:** регистронезависимая (приводится к нижнему регистру)

### Телефон

- **Формат:** 79991234567 (код страны + номер)
- **Для России:** 7 + 10 цифр = 11 цифр
- **Проверка:** точное совпадение

## Реализация на сервере

### Файлы

- `check_availability.go` - HTTP handler
- `store/users_check.go` - функции для проверки в БД

### Зависимости

```go
import (
    "github.com/tinode/chat/server/store"
    "github.com/tinode/chat/server/store/types"
)
```

### Функции store

```go
// Проверка логина
func UsersGetByLogin(login string) (*types.User, error)

// Проверка credential (email/tel)
func UsersGetByCred(method, value string) (*types.User, error)
```

## Безопасность

### Rate Limiting

Рекомендуется настроить rate limiting для этого эндпоинта:

```
Максимум 10 запросов в минуту с одного IP
```

### Защита от перебора

Эндпоинт возвращает общую ошибку, если несколько полей заняты:

```json
{
  "login_available": false,
  "email_available": false,
  "phone_available": true,
  "error": "Этот логин уже занят"
}
```

### Логирование

Все запросы логируются для аудита:

```
[INFO] check-availability: login=anton ip=192.168.1.1
```

## Интеграция с регистрацией

### Типичный поток

```
1. Пользователь вводит логин
   ↓
2. Debounce 500ms
   ↓
3. POST /v1/check-availability {"login":"anton"}
   ↓
4. Ответ: {"login_available":true}
   ↓
5. Показываем зелёную галочку ✓
```

### Обработка ошибок

```typescript
try {
  const result = await checkAvailability({ login, email, phone })
  
  if (!result.loginAvailable) {
    setLoginError(result.error || 'Логин занят')
  }
  
  if (!result.emailAvailable) {
    setEmailError(result.error || 'Email занят')
  }
  
  if (!result.phoneAvailable) {
    setPhoneError(result.error || 'Телефон занят')
  }
} catch (err) {
  console.error('Ошибка проверки:', err)
}
```

## Тестирование

### Тестовые данные

```json
// Свободный логин
{"login":"newuser123"}
// Ответ: {"login_available":true}

// Занятый логин
{"login":"admin"}
// Ответ: {"login_available":false,"error":"Этот логин уже занят"}

// Несуществующий email
{"email":"test@example.com"}
// Ответ: {"email_available":true}

// Занятый телефон
{"phone":"79991234567"}
// Ответ: {"phone_available":false,"error":"Этот номер уже зарегистрирован"}
```

### Postman Collection

```json
{
  "info": {
    "name": "Check Availability API"
  },
  "item": [
    {
      "name": "Check Login",
      "request": {
        "method": "POST",
        "url": "http://localhost:6060/v1/check-availability",
        "header": [{"key": "Content-Type", "value": "application/json"}],
        "body": {
          "mode": "raw",
          "raw": "{\"login\":\"test\"}"
        }
      }
    }
  ]
}
```

---

**Ласточка** — народный мессенджер с открытым кодом 🕊️
