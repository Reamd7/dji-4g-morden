# Makefile — standardized dev/test commands.
#
# Why GOROOT via `mise where`: on Windows/MSYS2, `mise exec -- go` does not
# resolve inside make's non-interactive shell (mise shim limitation). So we
# derive GOROOT from `mise where go` (which DOES work) and call the go binary
# directly, with the same env vars that .mise.toml sets for interactive shells.
#
# Usage:
#   make test           # pure-logic + mock tests (CI-friendly, no hardware)
#   make test-race      # + race detector (required for transport/concurrency)
#   make test-hardware  # + tests needing a real EC25 modem (build tag: hardware)
#   make cover          # coverage report (HTML)
#   make bench          # benchmarks
#   make tidy           # go mod tidy
#   make lint           # staticcheck
#   make run-probe      # run USB descriptor probe (needs hardware)
#   make run-attest     # run AT channel test (needs hardware)

# Resolve toolchain paths from mise (works in make; mise exec does not).
GOROOT := $(shell mise where go)
GO     := $(GOROOT)/bin/go

# Project-local GOPATH (mirrors .mise.toml [env]); holds mise-installed tools.
GOPATH := $(CURDIR)/.gopath
GOBIN  :=
export GOPATH

# Windows env vars that go.exe needs but git-bash doesn't export. Without
# these, go fails with "GOCACHE ... %LocalAppData% is not defined" or tries to
# mkdir under C:\WINDOWS (denied).
export LocalAppData := C:\Users\reamd\AppData\Local
export APPDATA      := C:\Users\reamd\AppData\Roaming
export USERPROFILE  := C:\Users\reamd
export TMP          := C:\Users\reamd\AppData\Local\Temp
export TEMP         := C:\Users\reamd\AppData\Local\Temp
export GOTMPDIR     := C:\Users\reamd\AppData\Local\Temp

# mingw64 gcc + libusb for cgo (mirrors .mise.toml [env]).
export CC              := C:\msys64\mingw64\bin\gcc.exe
export PKG_CONFIG_PATH := C:\msys64\mingw64\lib\pkgconfig
export PATH            := $(GOPATH)/bin;C:\msys64\mingw64\bin;$(PATH)

.PHONY: test test-race test-hardware cover cover-func bench tidy lint vet fmt run-probe run-attest check

# Default: run everything a no-hardware CI would run.
check: vet test test-race lint

# Pure-logic + mock tests. No hardware required.
# Scoped to internal/: cmd/ are cgo (gousb) hardware tools, exercised via
# make run-probe / run-attest, not go test.
test:
	$(GO) test ./internal/...

# Race detector — required for transport and any concurrent code.
test-race:
	$(GO) test -race ./internal/...

# Hardware integration tests (build tag: hardware). Requires the EC25 modem
# plugged in with WinUSB drivers installed via Zadig.
test-hardware:
	$(GO) test -tags=hardware ./internal/...

# Coverage report (HTML). Scoped to internal/ — cmd/ are hardware probe tools
# (cgo + gousb), not unit-tested; see AGENTS.md "测试分层".
cover:
	$(GO) test -coverprofile=coverage.out ./internal/...
	$(GO) tool cover -html=coverage.out

# Per-function coverage in terminal.
cover-func:
	$(GO) test -coverprofile=coverage.out ./internal/...
	$(GO) tool cover -func=coverage.out

# Benchmarks.
bench:
	$(GO) test -bench=. -benchmem ./internal/...

# Module hygiene.
tidy:
	$(GO) mod tidy

vet:
	$(GO) vet ./internal/...

# staticcheck from .gopath/bin (mise-managed).
lint:
	$(GOPATH)/bin/staticcheck ./internal/...

fmt:
	$(GO) fmt ./...

# Hardware-dependent command runners (convenience).
run-probe:
	$(GO) run ./cmd/usbprobe/

run-attest:
	$(GO) run ./cmd/attest/
