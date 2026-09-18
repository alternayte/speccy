set shell := ["bash", "-euo", "pipefail", "-c"]

version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-s -w -X github.com/alternayte/speccy/internal/kernel.Version=" + version
# pnpm runs at the version pinned in web/package.json, so a global pnpm is not needed.
pnpm := "npx --yes pnpm@12.4.2"

default:
    @just --list

# Install the web packages.
setup:
    cd web && {{pnpm}} install --frozen-lockfile

# Run the Go server with live reload and the Vite dev server. Local mode.
dev: setup
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'kill 0' EXIT
    go tool air &
    (cd web && {{pnpm}} run dev) &
    echo "Open http://127.0.0.1:5173"
    wait

# Generate the Go server interfaces and the TypeScript API client from api/openapi.yaml.
gen: setup
    cd internal/http/api && go tool oapi-codegen -config oapi-codegen.yaml ../../../api/openapi.yaml
    cd web && {{pnpm}} run gen:api

# Fail when generated files differ from the committed files.
gen-check: gen
    git diff --exit-code -- internal/http/api web/src/lib/api
    test -z "$(git ls-files --others --exclude-standard -- internal/http/api web/src/lib/api)"

# Go tests (SQLite engine) and web unit tests.
test: setup
    go tool gotestsum --format pkgname-and-test-fails -- -race -count=1 ./...
    cd web && {{pnpm}} run test

# Go tests against Postgres. Tests that need Postgres carry the postgres build tag.
test-pg:
    go tool gotestsum --format pkgname-and-test-fails -- -race -count=1 -tags postgres ./...

# golangci-lint, eslint, prettier, the type check and the convention checks.
lint: setup
    golangci-lint run ./...
    cd web && {{pnpm}} run lint && {{pnpm}} run format:check && {{pnpm}} run typecheck
    go run ./tools/buildtool conventions

# Build the web app into web/dist.
build-web: setup
    cd web && {{pnpm}} run build
    touch web/dist/.keep

# Build the web app and check the JavaScript budget (SDD §13.4).
budget: build-web
    go run ./tools/buildtool budget

# Build the speccy binary with the web app embedded, into bin/speccy.
build: build-web
    go build -trimpath -ldflags '{{ldflags}}' -o bin/speccy ./cmd/speccy

# Everything that gates a PR.
verify: gen-check lint test test-pg budget
