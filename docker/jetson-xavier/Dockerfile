# syntax=docker/dockerfile:1
#
# The agent with CUDA for Jetson Xavier (NX and AGX) on JetPack 5. It builds
# on the ONNX Runtime base image from kokoro-run
# (github.com/androiddrew/kokoro-run, docker/jetson-xavier/Dockerfile.onnxruntime):
# CUDA 12.2 through cuda-compat, cuDNN 9.3 and ONNX Runtime 1.22.2 compiled for
# the Xavier's sm_72, Ubuntu 22.04. Build that once on the Xavier (many
# hours), then this, on the Xavier too:
#
#   make image-jetson-xavier
#   docker run --runtime nvidia ... eino-talkie-agent:jetson-xavier
#
#   ORT_IMAGE    the ONNX Runtime base image
#   ORT_VERSION  the ONNX Runtime version in it
#   Mounts and VERSION: as in ../Dockerfile.cpu

ARG ORT_IMAGE=onnxruntime-jetson-xavier:1.22.2-cuda12.2

# Built on Ubuntu 22.04, the runtime's own release: Go's Debian 12 images have
# a newer glibc than it. Go 1.26: bytedance/sonic, which eino uses for JSON,
# supports Go up to 1.26 and warns at every start on a newer one.
FROM ubuntu:22.04 AS build
ARG GO_VERSION=1.26.10
ARG GO_SHA256=3f4227ce7b206a513e794f1393adb473a07b46e94d18bd15d1c684bbb7319797
ENV DEBIAN_FRONTEND=noninteractive
RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl gcc libc6-dev; \
	rm -rf /var/lib/apt/lists/*; \
	curl -fsSL -o /go.tgz "https://go.dev/dl/go$GO_VERSION.linux-arm64.tar.gz"; \
	echo "$GO_SHA256  /go.tgz" | sha256sum -c -; \
	tar -C /usr/local -xzf /go.tgz; \
	rm /go.tgz
ENV PATH=/usr/local/go/bin:$PATH GOPATH=/go
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
	set -eux; \
	install -D -m 0755 "$(go list -m -f '{{.Dir}}' github.com/androiddrew/talkie-core)/scripts/get-assets.sh" /out/get-assets.sh; \
	CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o /out/example-eino-talkie-agent .

FROM ${ORT_IMAGE}
ARG ORT_VERSION=1.22.2
ARG VERSION=devel
LABEL org.opencontainers.image.title="eino-talkie-agent" \
	org.opencontainers.image.description="A voice agent: talkie-core and an eino agent, on Jetson Xavier (JetPack 5)" \
	org.opencontainers.image.source="https://github.com/androiddrew/example-eino-talkie-agent" \
	org.opencontainers.image.version="$VERSION" \
	org.opencontainers.image.licenses="Apache-2.0"
RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl libasound2 libpulse0 python3; \
	rm -rf /var/lib/apt/lists/*; \
	useradd --uid 10001 --user-group --no-create-home --shell /usr/sbin/nologin talkie

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
