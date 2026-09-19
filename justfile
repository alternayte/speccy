set shell := ["bash", "-euo", "pipefail", "-c"]

version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-s -w -X github.com/alternayte/speccy/internal/kernel.Version=" + version
# pnpm runs at the version pinned in web/package.json, so a global pnpm is not needed.
pnpm := "npx --yes pnpm@12.4.2"
# The environment of `just dev-hosted` (SDD §15.1). Development values only.
hosted_env := "export SPECCY_DATABASE_URL='postgres://speccy:speccy@127.0.0.1:55432/speccy?sslmode=disable' SPECCY_MASTER_KEY='c3BlY2N5LWRldi1vbmx5LW1hc3Rlci1rZXktMDAwMCE=' SPECCY_BASE_URL='http://127.0.0.1:5173' SPECCY_LISTEN='127.0.0.1:7878'"

default:
    @just --list

# Install the web packages.
setup:
    cd web && {{pnpm}} install --frozen-lockfile

# Run the Go server with live reload and the Vite dev server. Local mode, serving a copy of
# testdata/bundles in build/dev-bundles, so edits in dev never change the fixtures.
dev: setup
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'kill 0' EXIT
    mkdir -p build
    test -d build/dev-bundles || cp -R testdata/bundles build/dev-bundles
    go tool air &
    (cd web && {{pnpm}} run dev) &
    echo "Open http://127.0.0.1:5173"
    wait

# Hosted mode for development: Postgres from compose.yaml, the Go server with live reload,
# and the Vite dev server. The master key and the URLs are for development only.
dev-hosted: setup
    #!/usr/bin/env bash
    set -euo pipefail
    trap 'kill 0' EXIT
    docker compose up -d --wait postgres
    {{hosted_env}}
    go tool air -build.full_bin "build/air/speccy serve --hosted" &
    (cd web && {{pnpm}} run dev) &
    echo "Open http://127.0.0.1:5173. Make the first admin with: just invite admin"
    wait

# Print an invite link for the dev-hosted server (REQ-083). role is admin or member.
invite role="member":
    #!/usr/bin/env bash
    set -euo pipefail
    {{hosted_env}}
    go run ./cmd/speccy admin invite --role {{role}}

# Generate code: sqlc (both engines), oapi-codegen, and the TypeScript API client.
gen: setup
    go tool sqlc generate
    go run ./tools/buildtool sqladapter
    cd internal/http/api && go tool oapi-codegen -config oapi-codegen.yaml ../../../api/openapi.yaml && go tool oapi-codegen -config oapi-client.yaml ../../../api/openapi.yaml
    cd web && {{pnpm}} run gen:api

# Fail when generated files differ from the committed files.
gen-check: gen
    git diff --exit-code -- db/postgres db/sqlite internal/http/api web/src/lib/api
    test -z "$(git ls-files --others --exclude-standard -- db/postgres db/sqlite internal/http/api web/src/lib/api)"

# Go tests (SQLite engine), the lint speed check (T-081), and web unit tests.
test: setup
    go tool gotestsum --format pkgname-and-test-fails -- -race -count=1 ./...
    go test -run '^$' -bench BenchmarkLint_10kWords -benchtime 3x ./internal/engine/lint
    cd web && {{pnpm}} run test

# Go tests against Postgres in a test container (Docker must run). The postgres build tag
# adds the postgres engine to every test that loops over storetest.Engines().
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

# Screenshot the gauntlet screens (BUILD.md §6.2) into docs/gauntlet/<run>/. Needs agent-browser,
# Docker for hosted mode, and the claude CLI for the full reviews (model: GAUNTLET_MODEL, default haiku).
gauntlet run: build
    go run ./tools/buildtool gauntlet {{run}}

# Build the release archives and images locally, without publishing (a snapshot).
release-check:
    goreleaser release --snapshot --clean

# Publish a release from the current tag: archives, checksums, and images (BUILD.md §3).
# CI runs this on a pushed v* tag; see .github/workflows/release.yml.
release:
    goreleaser release --clean
