FROM golang:1.25-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bot ./cmd/bot

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bot /bot

EXPOSE 8080

ENTRYPOINT ["/bot"]
