# ctx-wire dev tasks. Run `just` to list recipes, `just <recipe>` to run one.
# Override metadata via env vars, e.g. `VERSION=0.1.0-rc1 just pack`.

go := env_var_or_default("GO", "go")
bin := env_var_or_default("BIN", "ctx-wire")
version := env_var_or_default("VERSION", "dev")
commit := env_var_or_default("COMMIT", `git rev-parse --short HEAD 2>/dev/null || echo none`)
date := env_var_or_default("DATE", `date -u +%Y-%m-%dT%H:%M:%SZ`)
ldflags := "-X main.version=" + version + " -X main.commit=" + commit + " -X main.date=" + date
prefix := env_var_or_default("PREFIX", env_var("HOME") + "/.local")
install_dir := env_var_or_default("INSTALL_DIR", prefix + "/bin")
platforms := env_var_or_default("PLATFORMS", "darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64")

# List available recipes
default:
    @just --list

# Build a local ctx-wire binary
build:
    {{go}} build -o {{bin}} ./cmd/ctx-wire

# Build with VERSION/COMMIT/DATE metadata
build-release:
    {{go}} build -ldflags "{{ldflags}}" -o {{bin}} ./cmd/ctx-wire

# Build a release archive + checksum for the host platform under dist/
pack:
    GO="{{go}}" BIN="{{bin}}" VERSION="{{version}}" COMMIT="{{commit}}" DATE="{{date}}" scripts/pack.sh

# Build Windows release archives (amd64, arm64) under dist/
pack-windows:
    #!/usr/bin/env bash
    set -euo pipefail
    for arch in amd64 arm64; do
      echo "==> packing windows/$arch"
      GO="{{go}}" BIN="{{bin}}" VERSION="{{version}}" COMMIT="{{commit}}" DATE="{{date}}" \
        GOOS=windows GOARCH="$arch" scripts/pack.sh
    done

# Build release archives for every platform in PLATFORMS under dist/
pack-all:
    #!/usr/bin/env bash
    set -euo pipefail
    for target in {{platforms}}; do
      os="${target%/*}"; arch="${target#*/}"
      echo "==> packing $os/$arch"
      GO="{{go}}" BIN="{{bin}}" VERSION="{{version}}" COMMIT="{{commit}}" DATE="{{date}}" \
        GOOS="$os" GOARCH="$arch" scripts/pack.sh
    done

# Build with metadata and install to INSTALL_DIR (default ~/.local/bin)
install:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{install_dir}}"
    {{go}} build -ldflags "{{ldflags}}" -o "{{install_dir}}/{{bin}}" ./cmd/ctx-wire
    echo "installed {{bin}} to {{install_dir}}/{{bin}}"
    if ! command -v {{bin}} >/dev/null 2>&1 || [ "$(command -v {{bin}})" != "{{install_dir}}/{{bin}}" ]; then
      echo "note: {{install_dir}} is not the first {{bin}} on PATH; check 'which {{bin}}'"
    fi

# Fail if Go files are not gofmt-formatted
fmt-check:
    @test -z "$(gofmt -l cmd/ctx-wire internal filters)"

# Run unit tests
test:
    {{go}} test ./...

# Run tests with the race detector
race:
    {{go}} test -race ./...

# Run go vet
vet:
    {{go}} vet ./...

# Run built-in filter conformance tests
verify:
    {{go}} run ./cmd/ctx-wire verify

# Run the hermetic end-to-end smoke suite
smoke:
    bash scripts/smoke.sh

# Run the Windows (PowerShell) smoke suite — run this on Windows
smoke-windows:
    pwsh -NoProfile -File scripts/smoke.ps1

# Run the pre-commit validation suite
check: fmt-check vet test race verify

# Run full pre-release validation (check + smoke)
rc: check smoke

# Create a non-v version tag, build all archives, and publish a GitHub release.
# GitHub currently blocks v* tag creation for this repo, so use e.g.:
#   just release 0.1.0
release release_version:
    #!/usr/bin/env bash
    set -euo pipefail
    version="{{release_version}}"
    if [ "${version#v}" != "$version" ]; then
      echo "error: use a non-v version tag, e.g. 'just release 0.1.0' (GitHub blocks v* tags here)" >&2
      exit 2
    fi
    if [ -n "$(git status --porcelain)" ]; then
      echo "error: working tree is dirty; commit or stash changes before releasing" >&2
      git status --short >&2
      exit 1
    fi
    if git rev-parse -q --verify "refs/tags/$version" >/dev/null; then
      tag_commit="$(git rev-list -n 1 "$version")"
      head_commit="$(git rev-parse HEAD)"
      if [ "$tag_commit" != "$head_commit" ]; then
        echo "error: local tag $version points at $tag_commit, not HEAD $head_commit" >&2
        exit 1
      fi
      echo "tag $version already exists locally at HEAD"
    else
      git tag "$version"
      echo "created local tag $version"
    fi
    if git ls-remote --exit-code --tags origin "refs/tags/$version" >/dev/null 2>&1; then
      echo "tag $version already exists on origin"
    else
      git push origin "$version"
    fi
    VERSION="$version" just pack-all
    if gh release view "$version" >/dev/null 2>&1; then
      echo "error: GitHub release $version already exists" >&2
      exit 1
    fi
    gh release create "$version" dist/* --title "$version" --generate-notes

# Remove local build output
clean:
    rm -f {{bin}}
