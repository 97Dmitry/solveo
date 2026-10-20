.DEFAULT_GOAL := help
WAILS := go run github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
HOST_OS := $(shell go env GOHOSTOS)
ifeq ($(HOST_OS),windows)
WIN_ENV :=
else
WIN_ENV := GOOS=windows GOARCH=amd64 CGO_ENABLED=0
endif
.PHONY: help bindings frontend-check frontend-test frontend-build test vet build check dev
help:
	@echo make dev: Run desktop development on Windows
	@echo make build: Build build/bin/Solveo.exe
	@echo make check: Check frontend, tests, Windows vet and build
bindings:
	go run -tags bindings .
frontend-check: bindings
	cd frontend && vp check
	cd frontend && vp run typecheck
frontend-test:
	cd frontend && vp test run
frontend-build: bindings
	cd frontend && vp run build
test:
	go test ./internal/...
vet: frontend-build
	$(WIN_ENV) go vet ./...
build: frontend-build
	$(WAILS) build -platform windows/amd64 -skipbindings -s -o Solveo.exe
check: frontend-check frontend-test test vet build
dev:
	$(WAILS) dev
