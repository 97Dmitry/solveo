.DEFAULT_GOAL := help
WAILS := go run github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
GOLANGCI_LINT_VERSION := v2.14.0
HOST_OS := $(shell go env GOHOSTOS)
ifeq ($(HOST_OS),windows)
WIN_ENV :=
EXE := .exe
else
WIN_ENV := GOOS=windows GOARCH=amd64 CGO_ENABLED=0
endif
# Built for the host OS: `go run` would apply WIN_ENV to the linter binary itself.
# The linter must be built with a Go at least as new as the analyzed standard library,
# so the path is keyed by the host Go version and the binary is rebuilt after Go upgrades.
# Keep only the base version: build suffixes such as "-X:nodwarf5" break Make target names.
GO_VERSION := $(firstword $(subst :, ,$(subst -, ,$(shell go env GOVERSION))))
GOLANGCI_LINT_DIR := .tools/$(GOLANGCI_LINT_VERSION)-$(GO_VERSION)
GOLANGCI_LINT := $(GOLANGCI_LINT_DIR)/golangci-lint$(EXE)
.PHONY: help bindings frontend-check frontend-test frontend-build test lint vet build check dev audio-bench-test audio-bench-image audio-bench-nemotron-image
help:
	@echo make dev: Run desktop development on Windows
	@echo make build: Build build/bin/Solveo.exe
	@echo make check: Check frontend, tests, Go lint, Windows vet and build
	@echo make audio-bench-test: Test the STT benchmark scorer and runner with fakes
	@echo make audio-bench-image: Build the local Qwen3-ASR benchmark image
bindings:
	go run -tags bindings .
# The generator marks runtime files executable; keep the committed 0644 modes.
ifneq ($(HOST_OS),windows)
	chmod 644 frontend/wailsjs/runtime/*
endif
frontend-check: bindings
	cd frontend && vp check
	cd frontend && vp run typecheck
frontend-test:
	cd frontend && vp test run
frontend-build: bindings
	cd frontend && vp run build
test:
	go test ./internal/...
# Lint both the host build (portable tests) and the Windows application.
# Build the linter with throwaway module/build caches so its dependencies do not bloat
# the shared Go cache (CI restores that cache in every job).
LINT_BUILD_ENV := GOMODCACHE="$(CURDIR)/.tools/build/mod" GOCACHE="$(CURDIR)/.tools/build/cache"
$(GOLANGCI_LINT):
	$(LINT_BUILD_ENV) GOBIN="$(CURDIR)/$(GOLANGCI_LINT_DIR)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(LINT_BUILD_ENV) go clean -cache -modcache
lint: frontend-build $(GOLANGCI_LINT)
	"./$(GOLANGCI_LINT)" run ./...
	$(WIN_ENV) "./$(GOLANGCI_LINT)" run ./...
vet: frontend-build
	$(WIN_ENV) go vet ./...
build: frontend-build
	$(WAILS) build -platform windows/amd64 -skipbindings -s -o Solveo.exe
check: frontend-check frontend-test test lint vet build
dev:
	$(WAILS) dev
# Stage 9.0 STT benchmark: opt-in, not part of check (needs python3 + numpy; the image needs Docker).
audio-bench-test:
	cd tools/audio-bench && python3 -m unittest discover -s tests -t .
audio-bench-image:
	docker build -t solveo-audio-bench:dev -f tools/audio-bench/docker/Dockerfile tools/audio-bench
audio-bench-nemotron-image:
	docker build -t solveo-audio-bench-nemotron:dev -f tools/audio-bench/docker/nemotron.Dockerfile tools/audio-bench
