# Developing ctx-wire

Build, test, and release tasks. For what ctx-wire does and how to use it, see
[README.md](README.md) and [COMMANDS.md](COMMANDS.md).

## Build from source

```sh
go build -o ctx-wire ./cmd/ctx-wire
```

With version metadata (what releases use):

```sh
go build -ldflags "-X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
  -o ctx-wire ./cmd/ctx-wire
```

`ctx-wire version` prints the injected version, commit, and build date.

## Tasks (`just`)

Common development tasks run through [`just`](https://github.com/casey/just),
similar to scripts in a `package.json`:

```sh
just            # list recipes
```

| Command | What it runs |
|---|---|
| `just build` | Build a local `ctx-wire` binary |
| `VERSION=1.0.0 just build-release` | Build with version, commit, and date metadata |
| `VERSION=1.0.0 just pack` | Build a release archive and checksum under `dist/` |
| `just pack-all` | Build release archives for every platform under `dist/` |
| `just fmt-check` | Fail if Go files need `gofmt` |
| `just test` | Run `go test ./...` |
| `just race` | Run `go test -race ./...` |
| `just vet` | Run `go vet ./...` |
| `just verify` | Run `ctx-wire verify` through `go run` |
| `just smoke` | Run `scripts/smoke.sh` |
| `just check` | Run format check, vet, tests, race tests, and verify |
| `just rc` | Run `just check` plus the smoke suite |
| `just clean` | Remove the local build output |

## Smoke test

End-to-end check of the main install, run, hook, MCP, trust, gain, tune, and
telemetry paths. It builds the binary and runs in hermetic temp directories, so
it never touches your real HOME, agent config, or telemetry:

```sh
bash scripts/smoke.sh
```

Exits non-zero if any check fails.

## Release packaging

Build a shareable archive under `dist/`:

```sh
VERSION=0.1.0-rc1 just pack
```

This writes `dist/ctx-wire_<version>_<os>_<arch>.tar.gz` plus a `.sha256`
checksum. The archive contains the `ctx-wire` binary with executable
permissions, `README.md`, `COMMANDS.md`, `CONFIGURATION.md`, `FILTERS.md`,
`TROUBLESHOOTING.md`, `DEVELOPMENT.md`, and `INSTALL.txt`.

For a downloaded macOS archive, remove the quarantine attribute before the first
run if Gatekeeper offers only "Move to Trash":

```sh
xattr -d com.apple.quarantine ./ctx-wire 2>/dev/null || true
./ctx-wire init claude    # or codex, cursor, gemini, ...
```
