FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY src/ .

RUN go build -o bin/cattery

FROM alpine:latest

ARG CONFIG_PATH="/etc/cattery/config.yaml"

WORKDIR /cattery

COPY --from=builder /app/bin/cattery .

ENTRYPOINT ["./cattery", "server", "-c", "/etc/cattery/config.yaml"]