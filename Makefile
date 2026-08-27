# Build/run the Samwise image with the version stamped from git.
#
# The version comes from `git describe` (e.g. v0.3.6, or v0.3.6-2-gabc123-dirty
# between tags) and is baked into the binary via -X main.version, so the web
# footer and the agent both report the right release. .git is excluded from the
# Docker build context, so the version must be computed here and passed in — the
# Dockerfile can't derive it itself.

IMAGE    ?= choozy/samwise:latest
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# The image that ships to the VPS must match the VPS's CPU architecture — a
# docker image is NOT architecture-neutral: it carries the compiled samwise
# binary and the claude CLI, both built for one arch. The VPS is x86_64, so
# builds are pinned to linux/amd64 and stay correct from any build host
# (an Apple Silicon Mac would otherwise produce an arm64 image that dies on
# the VPS with "exec format error"). Override for an arm64 host:
#   make push PLATFORM=linux/arm64
PLATFORM ?= linux/amd64

.PHONY: version image push compose-build up test

## print the version that would be stamped
version:
	@echo $(VERSION)

## build the image, version stamped, for the deploy target arch
image:
	docker build --platform $(PLATFORM) --build-arg VERSION=$(VERSION) -t $(IMAGE) .

## build then push to the registry
push: image
	docker push $(IMAGE)

## build via docker compose (also version-stamped)
compose-build:
	VERSION=$(VERSION) docker compose build

## start the stack, version-stamped
up:
	VERSION=$(VERSION) docker compose up -d --build

## run the Go test suite
test:
	go test ./...
