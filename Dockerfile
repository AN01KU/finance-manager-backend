FROM golang:1.26.2 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG BUILD_TAGS=release
RUN if [ -n "${BUILD_TAGS}" ]; then \
      CGO_ENABLED=0 GOOS=linux go build -tags ${BUILD_TAGS} -o finance-manager ./cmd/main.go; \
    else \
      CGO_ENABLED=0 GOOS=linux go build -o finance-manager ./cmd/main.go; \
    fi

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates wget && rm -rf /var/lib/apt/lists/*

WORKDIR /root/

COPY --from=builder /app/finance-manager .
COPY --from=builder /app/internal/db/migrations ./internal/db/migrations
COPY --from=builder /app/internal/admin/templates ./internal/admin/templates
COPY --from=builder /app/internal/portal/templates ./internal/portal/templates

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1

CMD ["./finance-manager"]
