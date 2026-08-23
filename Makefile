# Build/run the Samwise image with the version stamped from git.
#
# The version comes from `git describe` (e.g. v0.3.6, or v0.3.6-2-gabc123-dirty
# between tags) and is baked into the binary via -X main.version, so the web
# footer and the agent both report the right release. .git is excluded from the
# Docker build context, so the version must be computed here and passed in — the
# Dockerfile can't derive it itself.

IMAGE   ?= samwise:latest
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: version image push compose-build up test

## print the version that would be stamped
version:
	@echo $(VERSION)

## build the image, version stamped
image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

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
