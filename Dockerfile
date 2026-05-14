# Ласточка — сборка Tinode из исходников (форк lastochka-server)
# Multi-stage: Go builder → минимальный Alpine образ

# ─── Stage 1: Компиляция ─────────────────────────────────────
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /src

# Зависимости отдельным слоем (кешируются пока go.mod не меняется)
COPY go.mod go.sum ./
RUN go mod download

# Исходный код
COPY . .

# Собрать основной сервер с PostgreSQL адаптером
RUN go build -tags postgres \
    -ldflags="-s -w" \
    -o /out/tinode \
    ./server/

# Собрать инструмент инициализации БД
RUN go build -tags postgres \
    -ldflags="-s -w" \
    -o /out/init-db \
    ./tinode-db/

# ─── Stage 2: Минимальный runtime образ ──────────────────────
FROM alpine:3.22

RUN apk add --no-cache ca-certificates bash grep

WORKDIR /opt/tinode

# Бинарники из builder
COPY --from=builder /out/tinode .
COPY --from=builder /out/init-db .

# Скрипты запуска и шаблон конфига (из папки docker/tinode/)
COPY docker/tinode/entrypoint.sh .
COPY docker/tinode/config.template .

RUN chmod +x entrypoint.sh credentials.sh 2>/dev/null || chmod +x entrypoint.sh

# Создать нужные директории
RUN mkdir -p static uploads /botdata /var/log

# ─── Дефолтные env-переменные ────────────────────────────────

ENV STORE_USE_ADAPTER=postgres
ENV POSTGRES_DSN="postgresql://postgres:postgres@localhost:5432/tinode?sslmode=disable&connect_timeout=10"

ENV TLS_ENABLED=false
ENV TLS_DOMAIN_NAME=
ENV TLS_CONTACT_ADDRESS=

ENV FCM_PUSH_ENABLED=false
ENV TNPG_PUSH_ENABLED=false
ENV WEBRTC_ENABLED=false
ENV PLUGIN_PYTHON_CHAT_BOT_ENABLED=false
ENV ACC_GC_ENABLED=false

ENV MEDIA_HANDLER=fs
ENV FS_CORS_ORIGINS='["*"]'
ENV AWS_CORS_ORIGINS='["*"]'

# Ключи — для прода ОБЯЗАТЕЛЬНО заменить через env
ENV API_KEY_SALT=T713/rYYgW7g4m3vG6zGRh7+FM1t0T8j13koXScOAj4=
ENV AUTH_TOKEN_KEY=wfaY2RgF2S1OQI/ZlK+LSrp1KB2jwAdGAIHQ7JZn+Kc=
ENV UID_ENCRYPTION_KEY=la6YsO+bNX/+XIkOqc5Svw==

ENV DEFAULT_COUNTRY_CODE=RU
ENV SERVER_STATUS_PATH=
ENV EMAIL_VERIFICATION_REQUIRED=
ENV SMTP_DOMAINS=
ENV SAMPLE_DATA=
ENV RESET_DB=false
ENV UPGRADE_DB=false
ENV NO_DB_INIT=false
ENV WAIT_FOR=

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s \
    CMD nc -z localhost 6060 || exit 1

EXPOSE 6060 16060

ENTRYPOINT ["./entrypoint.sh"]
