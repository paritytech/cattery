ARG GO_VERSION=1.25.5
ARG ALPINE_VERSION=3.20

FROM golang:${GO_VERSION}-alpine AS builder

ARG CATTERY_VERSION="0.0.0"

WORKDIR /src

COPY src/go.mod src/go.sum ./
RUN go mod download

COPY src/ .

ENV CGO_ENABLED=0 GOOS=linux

RUN go build \
      -trimpath \
      -ldflags="-s -w -X cattery/cmd.Version=${CATTERY_VERSION}" \
      -o /out/cattery \
    && /out/cattery --version

FROM alpine:${ALPINE_VERSION}

ARG CATTERY_VERSION="0.0.0"

LABEL org.opencontainers.image.title="cattery" \
      org.opencontainers.image.description="Scheduler and lifecycle manager for GitHub Actions self-hosted runners" \
      org.opencontainers.image.source="https://github.com/paritytech/cattery" \
      org.opencontainers.image.url="https://github.com/paritytech/cattery" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${CATTERY_VERSION}"

# ca-certificates: required for outbound HTTPS (api.github.com, googleapis.com)
# tzdata:         lets users override timezone with TZ env var
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 65532 cattery \
    && adduser  -S -u 65532 -G cattery -H -h /nonexistent cattery

COPY --from=builder /out/cattery /usr/local/bin/cattery

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/cattery"]
CMD ["server", "-c", "/etc/cattery/config.yaml"]
