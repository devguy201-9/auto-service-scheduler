# ─── Build stage ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache deps separately from source for faster rebuilds
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static binary, no CGO — runs on a scratch-like base
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /out/scheduler ./cmd/scheduler

# ─── Runtime stage ─────────────────────────────────────────────────────────
FROM alpine:3.20

# ca-certificates only — service makes no outbound HTTPS calls in this build,
# but keeping CA roots is cheap and future-proof for any external integration.
RUN apk add --no-cache ca-certificates

COPY --from=builder /out/scheduler /usr/local/bin/scheduler

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/scheduler"]