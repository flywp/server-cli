# server-cli

Easy CLI tool for servers managed by FlyWP.

Conforms to the FlyWP monitoring agent contract v0.2.1.

## Installation

### Prerequisites

- [Docker](https://www.docker.com/get-started)
- [Docker Compose](https://docs.docker.com/compose/install/)

### Quick Install

You can easily install the `fly` CLI tool using the following command. This will download and run the `install.sh` script, which will automatically detect your operating system and architecture, download the latest release, and install it to `/usr/local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/flywp/server-cli/main/install.sh | sudo bash
```

<details>

<summary>Manual Installation</summary>

### Manual Installation

If you prefer to manually download and install the binary, follow these steps:

1. Download the precompiled binaries from the [Releases](https://github.com/flywp/server-cli/releases) page. Choose the version suitable for your operating system and architecture.

1. Download the [latest tarball]((https://github.com/flywp/server-cli/releases)) for your platform:

    ```bash
    wget https://github.com/flywp/server-cli/releases/download/v0.1.0/fly-linux-amd64.tar.gz
    ```

2. Extract the tarball:
    ```bash
    tar -xzf fly-linux-amd64.tar.gz
    ```

3. Move the binary to a directory in your PATH:
    ```bash
    sudo mv fly-linux-amd64 /usr/local/bin/fly
    ```

4. Verify the installation:
    ```bash
    fly version
    ```

</details>

## Usage

### Base Docker Compose

FlyWP has a base Docker Compose configuration for running MySQL, Redis, Ofelia, and Nginx Proxy that are shared for all sites hosted on the server. The base Docker Compose must be started before a site can be created.

```bash
fly base start              # starts the base services (mysql, redis, nginx-proxy)
fly base stop               # stops the base services   
fly base restart            # restarts the base services
```

### Site Operations

You can run the following commands from anywhere inside a site folder or by specifying the domain name.

```bash
fly start --domain example.com       # starts the website
fly stop --domain example.com        # stops the website
fly restart --domain example.com     # restarts the website
fly --domain example.com wp <command>          # execute WP-CLI commands
fly logs --domain example.com        # view logs from all containers or a single one
fly restart <container> --domain example.com  # restart a container
fly --domain example.com exec [container] <command>  # execute commands inside a container. Default: the PHP container
```

Or run the commands from within the site directory without specifying the domain:

```bash
fly start                   # starts the website
fly stop                    # stops the website
fly restart                 # restarts the website
fly wp <command>            # execute WP-CLI commands
fly logs [container]        # view logs from all containers or a single one
fly logs -f [container]     # follow the logs (--tail N shows the last N lines)
fly restart <container>     # restart a container
fly exec [container] <command>  # execute commands inside a container. Default: the PHP container
```

### WP-CLI

**wp-cli**: To access `wp-cli`, use the following command from anywhere in the website folder or specify the domain name. The CLI will find the appropriate WordPress folder to execute the `wp` command.

```bash
fly --domain example.com wp plugin list --format=json
```

All arguments after the WP-CLI command (or after the command for `fly exec`) go to that command unchanged, flags included. Put `--domain` before the command. To pass a flag as the first argument, put `--` before it, for example `fly wp -- --info`.

### Monitoring agent

`fly agent run` is the FlyWP monitoring agent. It runs all the time under systemd (`fly-agent.service`, as the server user, not root), and FlyWP installs it. Each minute it measures CPU, load, memory, swap, disk and network traffic, and it sends the values and the server status (restart needed, waiting updates, OS, kernel, uptime) to FlyWP. It keeps unsent data on disk for up to 24 hours. FlyWP can update and restart the agent through it, without SSH. The agent does not need Docker.

It reads `FLY_AGENT_URL` (https), `FLY_AGENT_TOKEN` and `FLY_AGENT_SERVER_ID` from `/etc/fly/agent.env`, and keeps its state in `STATE_DIRECTORY` (`/var/lib/fly-agent`).

The agent also updates itself. Once a day, at a time set by the server id, it checks the latest release on GitHub. It installs the release only when:

- the release is newer than the running version (the agent never downgrades)
- the release has a valid signature from the FlyWP release key (see [Releasing](#releasing))
- the signature is more than 24 hours old

To turn this off on one server, add `FLY_AGENT_AUTO_UPDATE=off` to `/etc/fly/agent.env` and restart the agent. Updates that FlyWP sends still work.

```bash
systemctl status fly-agent    # is the agent running?
journalctl -u fly-agent -f    # the agent log
```

### Global Commands

A few helper commands to debug the server installation and start/stop all sites.

```bash
fly status                  # shows the status of the system
fly sites start             # starts all sites
fly sites stop              # stops all sites
fly sites restart           # stops and starts all sites
```

## Development

Go 1.27 or later is required (`go.mod` selects the toolchain). The Makefile holds the common tasks:

```bash
make build      # builds bin/fly with the version from git
make test       # go test ./... -race
make lint       # golangci-lint (pinned version, built with the module's Go)
make vuln       # govulncheck
make check      # fmt-check, vet, lint, test and vuln (CI runs the same)
make release    # static linux/amd64 and linux/arm64 archives + checksums.txt in build/
make help       # lists all targets
```

`make release VERSION=v0.2.0` stamps a specific version. The release archives must keep the names `fly-linux-<arch>.tar.gz` with the binary `fly-linux-<arch>` inside: installed CLIs look for these names when they run `fly update`.

CI runs `make check` and `make release` on every pull request and on every push to `develop` and `main`.

### Releasing

`main` is the release branch. To publish a release, tag a commit on `main` and push the tag:

```bash
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

The Release workflow checks that the tag is on `main`, runs `make check`, builds the archives with `make release`, and creates the GitHub release with both archives and `checksums.txt`. A tag with a pre-release suffix, such as `v0.2.0-rc.1`, becomes a pre-release, so installed CLIs do not update to it.

`install.sh` and `fly update` install only a release that has `checksums.txt`. Releases before v0.2.0 have none, so push the tag right after the merge into `main`: until the release is published, `install.sh` from `main` stops.

After the workflow publishes the release, sign it on your own computer:

```bash
make sign-release VERSION=v0.2.0 KEY=<private key file>    # KEY=- reads the key from stdin
```

This signs `checksums.txt`, checks the signature with the key that the code trusts, and uploads `checksums.txt.sig`. Agents install a release by themselves only when it has a valid signature, and only 24 hours after it was signed. Each server then installs it at its own time of day. To stop a bad release in those 24 hours, mark it as a pre-release on GitHub.

The signing key is kept outside GitHub, so that a push to GitHub alone cannot reach every server. `make release-key KEY=<file>` makes a key and prints its public key line for `internal/release/keys.go`. Keep the private key in a password manager, with a backup. Never commit it, and never put it in a GitHub secret. If the key is lost or leaked, ship a binary with a new key through a FlyWP update: that path does not use the signature.

### Dev pre-releases

To test a branch on real servers before it merges, publish a dev pre-release of its current commit:

```bash
make dev-version    # prints the tag, for example v0.2.0-dev.1a2b3c4
make dev-release    # tags the commit and pushes the tag; CI publishes the pre-release
```

The version is the next minor version after the latest release, plus the short commit hash (`DEV_BASE=v0.1.2` overrides the first part). Pre-release tags can come from any branch. `make dev-release` refuses uncommitted changes, commits that are not pushed, and commits whose release workflow would publish the tag as a full release.

`fly update` and `install.sh` only install the latest full release, so install a dev pre-release on a test server by hand:

```bash
tag=v0.2.0-dev.1a2b3c4 arch=amd64   # arch: amd64 or arm64 (uname -m: x86_64 or aarch64)
base=https://github.com/flywp/server-cli/releases/download/$tag
curl -fsSLO "$base/fly-linux-$arch.tar.gz" && curl -fsSLO "$base/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "fly-linux-$arch.tar.gz" && sudo install -m 0755 "fly-linux-$arch" /usr/local/bin/fly
fly version
```

To build the same version locally without publishing it, run `make release VERSION=$(make -s dev-version)`.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
