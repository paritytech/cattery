ARG GO_VERSION=1.26.5
ARG ALPINE_VERSION=3.22

# The builder runs on the build host's own platform and cross-compiles, so a
# multi-platform image build never compiles Go under emulation. It references
# no TARGET* argument, which lets every target platform share this one stage.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

ARG CATTERY_VERSION="0.0.0"

WORKDIR /src

COPY src/go.mod src/go.sum ./
RUN go mod download

COPY src/ .

ENV CGO_ENABLED=0 GOOS=linux

# One binary per linux architecture, named like the release assets
# (cattery_<os>_<arch>_<version>). The image runs the one matching its own
# architecture and serves the other to trays through /agent/download.
RUN for arch in amd64 arm64; do \
      GOARCH="${arch}" go build \
        -trimpath \
        -ldflags="-s -w -X cattery/lib/version.Version=${CATTERY_VERSION}" \
        -o "/out/cattery_linux_${arch}_${CATTERY_VERSION}" \
      || exit 1; \
    done

FROM alpine:${ALPINE_VERSION}

ARG CATTERY_VERSION="0.0.0"
ARG TARGETARCH

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

# /usr/local/bin/cattery is the binary for this image's architecture. Every
# other one goes to /var/lib/cattery/agents, the default
# server.agentBinaries.dir, which the cattery user can write to so on-demand
# downloads can add more platforms. --version runs on the target architecture,
# so it also proves the cross-compiled binary starts there.
RUN --mount=type=bind,from=builder,source=/out,target=/out \
    own="cattery_linux_${TARGETARCH}_${CATTERY_VERSION}" \
    && install -m 0755 "/out/${own}" /usr/local/bin/cattery \
    && install -d -o cattery -g cattery /var/lib/cattery/agents \
    && for binary in /out/cattery_linux_*; do \
         [ "$(basename "${binary}")" = "${own}" ] \
           || install -o cattery -g cattery -m 0755 "${binary}" /var/lib/cattery/agents/ \
           || exit 1; \
       done \
    && /usr/local/bin/cattery --version

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/cattery"]
CMD ["server", "-c", "/etc/cattery/config.yaml"]
