# Development

Go 1.27 or later (`go.mod` selects the toolchain). `make help` lists every target.

```bash
make build      # bin/fly, with the version from git
make test       # go test ./... -race
make check      # fmt-check, vet, lint, test and govulncheck: the gate before each merge
make release    # static linux/amd64 and linux/arm64 archives and checksums.txt in build/
```

CI runs `make check` and `make release` on each pull request and on each push to `develop` and
`main`.

## Tests on Linux

The agent reads `/proc`, `/sys` and cgroups, so some tests build only on Linux
(`*_linux_test.go`, for example the test against the real `/proc`). On macOS, `make check` skips
them. Before you push a change to `internal/metrics` or `internal/agent`, run the tests on Linux as
a non-root user:

```bash
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD":/src -w /src golang:1.27 go test ./...
```

A few tests need root (the owner of the binary after `sudo fly update`). CI runs them with `sudo`.

## Run the agent locally

The agent accepts plain `http://` for a loopback host, so you can point it at a local control
plane:

```bash
make build
STATE_DIRECTORY=$(mktemp -d) FLY_AGENT_URL=http://127.0.0.1:8080 \
FLY_AGENT_TOKEN=test FLY_AGENT_SERVER_ID=1 FLY_AGENT_AUTO_UPDATE=off bin/fly agent run
```

## Reproducible builds

`make release` gives the same bytes for the same commit and Go version, on any computer: the build
date is the commit date, paths are trimmed, and the archives are written with owner 0:0.
`make sign-release` depends on this. See [releasing.md](releasing.md).

## Code layout

| Path | |
|---|---|
| `cmd/` | The commands (cobra). `cmd/agent.go` is `fly agent run`. |
| `internal/agent` | The agent: settings, lock, schedule, queues, sending, commands, auto-update. |
| `internal/agent/wire` | The JSON types of the contract. |
| `internal/metrics` | The Linux measurements: `/proc`, PSI, disks, Docker, sites. |
| `internal/dockerapi` | A minimal Docker client over the unix socket (two GET requests). |
| `internal/release` | Release download, sha256 check, binary replacement, signatures, trusted keys. |
| `internal/service` | The `fly-agent` systemd unit: restart after an update. |
| `internal/statefile` | JSON files written atomically. |
| `internal/docker` | Docker Compose calls and the Docker checks of the site commands. |
| `internal/utils` | Finds the site folder from the current folder or `--domain`. |
| `tools/releasesign`, `tools/sign-release.sh` | Key generation and release signing. |
| `install.sh` | The installer. |
