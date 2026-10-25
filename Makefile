# Build and test the agent, and its Docker images (see the README's "Run it
# in Docker").

IMAGE ?= eino-talkie-agent
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
IMAGE_PLATFORMS ?= linux/amd64 linux/arm64
HOST_ARCH := $(if $(filter aarch64 arm64,$(shell uname -m)),arm64,amd64)
BUILD_ARGS = --build-arg VERSION=$(VERSION)

.PHONY: build test vet image-cpu image-cuda image-jetson-orin image-jetson-xavier

build:
	CGO_ENABLED=1 go build -o bin/example-eino-talkie-agent .

vet:
	go vet ./...

test:
	go test -race ./...

# The CPU image, built and loaded per platform as $(IMAGE):cpu-<arch>, since
# the classic image store can't hold a multi-platform image; the host's
# platform is also tagged $(IMAGE):cpu. Other platforms need QEMU binfmt.
image-cpu:
	set -e; for p in $(IMAGE_PLATFORMS); do \
		docker buildx build --platform $$p -f docker/Dockerfile.cpu $(BUILD_ARGS) \
			--load -t $(IMAGE):cpu-$${p#linux/} . ; \
	done
	$(if $(filter linux/$(HOST_ARCH),$(IMAGE_PLATFORMS)),docker tag $(IMAGE):cpu-$(HOST_ARCH) $(IMAGE):cpu)

image-cuda:
	docker buildx build --platform linux/amd64 -f docker/Dockerfile.cuda $(BUILD_ARGS) --load -t $(IMAGE):cuda .

# The Jetson images build on the Jetson, on kokoro-run's ONNX Runtime base
# images. The Xavier's is on Docker Hub; for the Orin's, run
# `make image-jetson-orin-onnxruntime` in kokoro-run once (hours) first.
JETSON_ORIN_ORT_IMAGE ?= onnxruntime-jetson-orin:1.23.0-cuda13.2
JETSON_XAVIER_ORT_IMAGE ?= androiddrew/onnxruntime-jetson-xavier:1.22.2-cuda-12.2-cudnn-9.3

image-jetson-orin:
	docker buildx build --platform linux/arm64 -f docker/jetson-orin/Dockerfile $(BUILD_ARGS) \
		--build-arg ORT_IMAGE=$(JETSON_ORIN_ORT_IMAGE) --load -t $(IMAGE):jetson-orin .

image-jetson-xavier:
	docker buildx build --platform linux/arm64 -f docker/jetson-xavier/Dockerfile $(BUILD_ARGS) \
		--build-arg ORT_IMAGE=$(JETSON_XAVIER_ORT_IMAGE) --load -t $(IMAGE):jetson-xavier .
