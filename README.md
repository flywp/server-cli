# fly — the FlyWP server CLI

`fly` manages the sites on a server that [FlyWP](https://flywp.com) provisions. It also runs the
FlyWP monitoring agent.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/flywp/server-cli/main/install.sh | sudo bash
```

The script installs the latest release for your architecture (Linux amd64 or arm64) to
`/usr/local/bin/fly`. It checks the download against `checksums.txt` first.

To update later, run `sudo fly update`.

<details>
<summary>Install by hand</summary>

```bash
arch=amd64   # or arm64
base=https://github.com/flywp/server-cli/releases/latest/download
curl -fsSLO "$base/fly-linux-$arch.tar.gz" && curl -fsSLO "$base/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "fly-linux-$arch.tar.gz" && sudo install -m 0755 "fly-linux-$arch" /usr/local/bin/fly
fly version
```

</details>

## Use

The site commands need Docker and Docker Compose. Run them inside a site folder, or name the site
with `--domain`.

```bash
fly base start|stop|restart     # the shared services: MySQL, Redis, Ofelia, Nginx Proxy
fly start|stop|restart          # the site
fly restart <container>         # one container of the site
fly wp <command>                # WP-CLI, for example: fly wp plugin list
fly exec [container] <command>  # a command in a container (default: PHP)
fly logs [-f] [container]       # the logs of the site, or of one container

fly sites start|stop|restart    # all sites
fly status                      # the state of the server and its services
fly update                      # install the latest release
fly version
```

Put `--domain` before the command: `fly --domain example.com wp plugin list`. Everything after
the command goes to it unchanged. To pass a flag as the first argument, put `--` before it:
`fly wp -- --info`.

## Monitoring agent

FlyWP installs `fly agent run` as the systemd service `fly-agent`. Each minute, it sends the health
of the server and of each site to FlyWP. It runs as the server user, not as root, and it updates
itself only to signed releases.

```bash
systemctl status fly-agent
journalctl -u fly-agent -f
```

See [docs/monitoring-agent.md](docs/monitoring-agent.md).

## Documentation

| | |
|---|---|
| [Monitoring agent](docs/monitoring-agent.md) | What it measures, its settings and files, updates, security |
| [Development](docs/development.md) | Build, tests, code layout |
| [Releasing](docs/releasing.md) | Release, signing, dev pre-releases, rollback |
| [Design decisions](docs/decisions.md) | Why the agent works the way it does |

## License

MIT. See [LICENSE](LICENSE).
