# Multi stage build: the SPA is compiled first and then embedded into the Go binary, so
# the runtime image contains exactly one executable and nothing else.
#
#   docker build -t webhook-zq .
#   docker compose up

# ── frontend ─────────────────────────────────────────────────────────────────
FROM node:24-alpine AS web
WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

# ── server ───────────────────────────────────────────────────────────────────
FROM golang:1.27-alpine AS api
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /src/web/dist ./web/dist

# Build args so `docker build --build-arg VERSION=v1.2.3` produces a binary that can
# name itself. Without this the version stays at the default and every image looks
# identical.
ARG VERSION=docker
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X 'github.com/yuandzhang/webhook-zq/internal/version.version=${VERSION}' -X 'github.com/yuandzhang/webhook-zq/internal/version.buildTime=${BUILD_TIME}'" \
    -o /out/webhook-zq ./cmd/webhook-tester

# ── runtime ──────────────────────────────────────────────────────────────────
# Alpine (not scratch/distroless) because the healthcheck needs wget and the container
# talks to PostgreSQL over TLS, which needs CA certificates.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget tzdata \
    && adduser -D -u 10001 -h /nonexistent -s /sbin/nologin appuser

COPY --from=api /out/webhook-zq /usr/local/bin/webhook-zq

USER 10001:10001
EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/webhook-zq"]
CMD ["start", "--port", "8080"]
