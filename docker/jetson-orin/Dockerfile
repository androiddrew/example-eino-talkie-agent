# syntax=docker/dockerfile:1
#
# The agent with CUDA for Jetson Orin on JetPack 7.2. It builds on the ONNX
# Runtime base image from kokoro-run (github.com/androiddrew/kokoro-run,
# docker/jetson-orin/Dockerfile.onnxruntime): ONNX Runtime 1.23.0 compiled for
# the Orin's GPU against CUDA 13.2 and cuDNN 9, Ubuntu 24.04. Build that once
# on the Orin (about 4 hours), then this (minutes), on the Orin too:
#
#   make image-jetson-orin
#   docker run --runtime nvidia ... eino-talkie-agent:jetson-orin
#
#   ORT_IMAGE    the ONNX Runtime base image
#   ORT_VERSION  the ONNX Runtime version in it
#   Mounts and VERSION: as in ../Dockerfile.cpu

ARG ORT_IMAGE=onnxruntime-jetson-orin:1.23.0-cuda13.2
# Go 1.26: bytedance/sonic, which eino uses for JSON, supports Go up to 1.26
# and warns at every start on a newer one.
ARG GO_IMAGE=golang:1.26-bookworm

# Built natively on the Orin. Debian 12's glibc is older than Ubuntu 24.04's.
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
	set -eux; \
	install -D -m 0755 "$(go list -m -f '{{.Dir}}' github.com/androiddrew/talkie-core)/scripts/get-assets.sh" /out/get-assets.sh; \
	CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o /out/example-eino-talkie-agent .

FROM ${ORT_IMAGE}
ARG ORT_VERSION=1.23.0
ARG VERSION=devel
LABEL org.opencontainers.image.title="eino-talkie-agent" \
	org.opencontainers.image.description="A voice agent: talkie-core and an eino agent, on Jetson Orin (JetPack 7.2)" \
	org.opencontainers.image.source="https://github.com/androiddrew/example-eino-talkie-agent" \
	org.opencontainers.image.version="$VERSION" \
	org.opencontainers.image.licenses="Apache-2.0"
RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl libasound2t64 libpulse0 python3; \
	rm -rf /var/lib/apt/lists/*; \
	useradd --uid 10001 --user-group --no-create-home --shell /usr/sbin/nologin talkie

# The base image keeps ONNX Runtime's notices in /usr/share/doc/onnxruntime.
ARG DOCS=/usr/share/doc/eino-talkie-agent
COPY LICENSE NOTICE THIRD_PARTY.md $DOCS/
COPY --from=build /out/get-assets.sh /usr/local/bin/get-assets.sh
COPY --from=build /out/example-eino-talkie-agent /usr/local/bin/example-eino-talkie-agent

ENV XDG_CONFIG_HOME=/config \
	XDG_DATA_HOME=/data \
	HOME=/tmp \
	EINO_TALKIE_CUDA=true \
	EINO_TALKIE_ORT=/opt/onnxruntime/lib/libonnxruntime.so.$ORT_VERSION \
	EINO_TALKIE_DATA=/data/eino-talkie-agent \
	EINO_TALKIE_PARAKEET=/data/eino-talkie-agent/parakeet \
	EINO_TALKIE_KOKORO=/data/eino-talkie-agent/kokoro \
	EINO_TALKIE_SKILLS=/config/eino-talkie-agent/skills
USER talkie
WORKDIR /tmp
ENTRYPOINT ["example-eino-talkie-agent"]
