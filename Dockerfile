FROM golang:1.24-alpine AS builder

ARG CATTERY_VERSION="0.0.0"

WORKDIR /app

COPY src/ .

RUN go build -o bin/cattery -ldflags="-X cattery/cmd.Version=$CATTERY_VERSION"

FROM alpine:latest

ARG CONFIG_PATH="/etc/cattery/config.yaml"

WORKDIR /cattery

COPY --from=builder /app/bin/cattery .

RUN ./cattery --version

ENTRYPOINT ["./cattery", "server", "-c", "/etc/cattery/config.yaml"]