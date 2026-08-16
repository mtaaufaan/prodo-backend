# ============================================================
# PRODO Backend — Multi-stage Dockerfile
# Stage 1: builder  — compile Go binary
# Stage 2: runner   — minimal runtime image
# ============================================================

# ── Stage 1: Builder ─────────────────────────────────────────
FROM golang:1.22-alpine AS builder

# Install git (diperlukan untuk go mod download dari private repos)
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Copy go.mod dan go.sum terlebih dahulu untuk cache layer
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build binary dengan optimasi size
# -s: strip symbol table
# -w: strip DWARF debug info
# CGO_ENABLED=0: static binary, tidak butuh libc
ARG GIT_SHA=dev
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-s -w -X main.GitSHA=${GIT_SHA} -X main.BuildDate=${BUILD_DATE}" \
    -o prodo-backend \
    ./cmd/api/main.go

# Verifikasi binary valid
RUN ./prodo-backend --help 2>&1 || true

# ── Stage 2: Runner ───────────────────────────────────────────
# Gunakan distroless untuk attack surface minimal
# Alternatif: alpine:3.20 jika butuh shell access
FROM gcr.io/distroless/static-debian12:nonroot AS runner

# Copy timezone data dari builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy CA certificates untuk HTTPS calls (Vault, Keycloak, MinIO)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy binary
COPY --from=builder /build/prodo-backend /prodo-backend

# Jalankan sebagai non-root user (distroless nonroot = UID 65532)
USER nonroot:nonroot

# Expose port backend
EXPOSE 3001

# Health check
# Catatan: distroless tidak punya curl/wget — healthcheck dilakukan oleh orchestrator
# (Docker Compose atau k8s liveness probe via HTTP GET)

# Entry point
ENTRYPOINT ["/prodo-backend"]
