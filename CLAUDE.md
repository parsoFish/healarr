# healarr — Claude Instructions

A Go daemon that watches a Plex/*arr media stack on two nodes (a Pi and a NAS), runs health checks, records decisions in SQLite and sends a daily digest. README.md has the phases, the CLI examples and the first-run table; `docs/runbook.md` has setup on each host; `docs/superpowers/` holds the specs, plans and ADRs. Read the ADR a change touches before changing the behaviour it records.

## Layout

- `cmd/healarr`: the binary. `internal/`: one package per concern (agent, check, checks, cleanup, cli, clients, config, decision, notify, peer, staleness, store, version, web).
- `config.example.toml` and `secrets.example.toml`: the two files a node copies and fills in; real values never enter the repository.
- `deploy/`: service units and install steps. `dist/` is build output and is not committed.

## Run

- `make build` for a local dev build (`dist/healarr`); `make build-pi` and `make build-nas` for the static node binaries.
- `healarr config validate --config <path>` loads a config and secrets pair and reports what it found without printing a key.
- Go is the version in `go.mod` or newer; `GOTOOLCHAIN=auto` fetches it.

## Test

The CI workflow (`.github/workflows/ci.yml`) runs `go vet ./...` and `go test ./... -race -cover`, then cross-builds both node binaries; run the same before a commit.

- `make test` (vet and race tests with coverage) and `make lint` (vet and golangci-lint).
- Tests sit beside the package they test (`*_test.go`) and assert values with real-shaped fixtures (a config file, an API body); a change that is not trivial gets one.

## Commits and branches

- Conventional commits: `<type>: <description>` with feat, fix, refactor, docs, test, chore, perf or ci (the history already follows it). Say what changed in plain words.
- Commit on a working branch and open a pull request; never commit on or push `main`. `main` moves only by a merged pull request with CI green.
- No AI attribution lines in a commit or a pull request.
- Nothing personal in a committed file: hostnames, LAN addresses, home paths, account names, emails and keys live in the node's `config.toml` and `secrets.toml`. Example files use placeholders (`192.0.2.x`, `example.com`, `/home/<user>`).

## Code

- Small, focused files organised by package (aim under 400 lines); one package per concern as above.
- Return new values rather than mutating an input; handle every error explicitly and never discard one; validate config and API input at the boundary and fail fast.
- No hardcoded values: constants, config or environment.
