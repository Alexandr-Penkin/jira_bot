FROM golang:1.25-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CMD_PATH selects which binary this image contains. Default keeps the
# existing monolith build at ./cmd/bot. Phase 1 adds ./cmd/webhook-svc
# and future phases will add more.
ARG CMD_PATH=./cmd/bot

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/service ${CMD_PATH}

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/service /service

EXPOSE 8080

ENTRYPOINT ["/service"]
