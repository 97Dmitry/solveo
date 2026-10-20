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
GOLANGCI_LINT := .tools/$(GOLANGCI_LINT_VERSION)/golangci-lint$(EXE)
.PHONY: help bindings frontend-check frontend-test frontend-build test lint vet build check dev
help:
	@echo make dev: Run desktop development on Windows
	@echo make build: Build build/bin/Solveo.exe
	@echo make check: Check frontend, tests, Go lint, Windows vet and build
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
$(GOLANGCI_LINT):
	GOBIN="$(CURDIR)/.tools/$(GOLANGCI_LINT_VERSION)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
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
