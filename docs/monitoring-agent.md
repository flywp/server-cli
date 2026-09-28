# The monitoring agent

`fly agent run` is the FlyWP monitoring agent. It measures the server each minute and sends the
values to FlyWP. FlyWP shows them as charts and alerts, and can restart or update the agent without
SSH.

The agent follows the FlyWP monitoring agent contract **v0.5.0**. The contract defines the requests,
the fields and the rules on both sides.

## How it runs

| | |
|---|---|
| Service | `/etc/systemd/system/fly-agent.service`, `Restart=always`. FlyWP installs it. |
| User | The server user (for example `fly`), never root. |
| Binary | `~/.fly/bin/fly` of the server user. `/usr/local/bin/fly` is a link to it. |
| Settings | `/etc/fly/agent.env` (root, mode 0600) |
| State | `/var/lib/fly-agent` (`STATE_DIRECTORY` of systemd) |
| Log | `journalctl -u fly-agent` |

Only one agent can run with one state folder. The agent does not need Docker.

### Settings

| Variable | |
|---|---|
| `FLY_AGENT_URL` | The FlyWP control plane. It must be `https://`. Plain `http://` is allowed only for a loopback host (tests and local development). |
| `FLY_AGENT_TOKEN` | The token of this server. The agent never logs it. |
| `FLY_AGENT_SERVER_ID` | The id of the server in FlyWP. It also sets the second of the minute at which the agent works, so that servers do not all send at the same time. |
| `FLY_AGENT_AUTO_UPDATE` | `off` stops the automatic updates on this server. See [Updates](#updates). |

After a change, run `sudo systemctl restart fly-agent`.

### State files

| File | |
|---|---|
| `samples.json` | Samples not yet accepted by FlyWP. At most 24 hours (1440 samples); then the oldest go. |
| `events.json` | Events not yet accepted (at most 1000). |
| `ran.json` | The commands that ran, so that no command runs two times. |
| `counters.json` | The last counters, so that a restart does not lose a minute of traffic. |
| `state.json` | The report interval, and the time of the last release check. |
| `lock` | Stops a second agent. |

Each file is written to a temporary file first, then renamed, so a crash never leaves half a file.

## What it sends

The agent reads the server every 10 seconds. Each minute, it sends one sample with the value of the
minute and, where it makes sense, the **peak** of the minute. A peak shows a short spike that the
average of the minute hides.

| Group | Values |
|---|---|
| CPU | use (%) and its peak, load (1 min) |
| Memory | used and total, swap used and total, with the peaks of the used values |
| Disk space | used and total of `/` (the same as `df`) |
| Network | bytes in and out of the physical interfaces, and the peak bytes each second |
| Pressure (PSI) | the share of time that tasks waited for CPU, memory or disk I/O, and its peak. Not sent when the kernel has no PSI. |
| Disk activity | bytes and operations read and written on the physical disks, and the peaks each second |
| Sites | the CPU, memory and disk use of each site (see below) |

With the samples, the agent sends the **status** of the server: restart needed, waiting updates
(and security updates), OS, kernel, uptime, architecture, CPU count, Docker state (`running`,
`not_running`, `not_installed`) and Docker version.

### Sites

A site is a Docker Compose project whose folder is directly in the home folder of the server user.
For each site, the agent sends:

- **CPU:** the CPU time of its containers in the minute, as a share of all CPUs.
- **Memory:** the memory of its containers, without the page cache that the kernel can free (the
  same as `docker stats`).
- **Disk:** the size of its folder. The agent measures it at most once an hour, in the background, at
  the lowest I/O priority. A folder that the server user cannot list is skipped, so the value can be
  lower than the real use (for example, the database files in `~/.fly` belong to the container
  user).

The agent reads Docker through its socket with two requests only: `GET /version` and
`GET /containers/json`. It reads CPU and memory from cgroup v2. Without Docker or cgroup v2, it
sends no site values.

### Gaps and null values

- A value that the agent cannot measure is `null`, not 0.
- The first sample after a fresh start has no traffic (`net_counters_reset` is true) and no
  pressure or disk activity values: they need two readings. A restart within 90 seconds keeps
  them.
- When the first minute after a fresh start is shorter than 10 seconds, the agent sends no sample
  for it. That short minute is mostly the load of the start itself.
- A reading that fails skips that minute. The agent never stops for a measurement error.

## Sending

- The agent sends every 1 to 10 minutes. FlyWP sets the interval in each reply.
- When FlyWP cannot be reached, the agent keeps the data on disk and tries again: after 1, 2, 4, 8,
  then every 10 minutes. It keeps measuring meanwhile. After a 401 it tries each 5 minutes; after a
  429 it waits as FlyWP asks.
- When FlyWP accepts the data again, the agent logs one line: "the control plane accepts the
  requests again".
- Samples are sent even while events fail, and the other way around.

## Commands from FlyWP

| Command | What the agent does |
|---|---|
| `agent.restart` | Exits; systemd starts it again. |
| `agent.update` | Downloads the version that FlyWP names, checks its sha256 against the value that FlyWP sends, replaces the binary and exits. |

Each command runs at most one time, also after a crash. The result goes back as an event
(`command.completed` or `command.failed`). The agent never installs an older version: an update to
an older release fails with the reason.

## Updates

There are three ways to a new version. All keep the binary owned by the server user and restart the
agent.

1. **FlyWP sends `agent.update`** with the version and its sha256. This path does not need a
   signature, so it also works when the release key is lost.
2. **The agent updates itself.** Once a day, at a time set by the server id, it checks the latest
   release on GitHub. It installs it only when:
   - the release is newer than the running version;
   - `checksums.txt.sig` has a valid signature from a FlyWP release key that this build trusts;
   - the signature is more than 24 hours old.

   Each server installs at its own time of day. A dev pre-release updates itself too. A build
   without a release version (`make build` between tags, `go build`) never does.
   `FLY_AGENT_AUTO_UPDATE=off` stops this path on one server.
3. **`sudo fly update`** or `install.sh` install the latest release after checking its sha256 in
   `checksums.txt`.

## Security

- The agent runs as the server user. It never runs as root, and it refuses to start as root.
- It sends data only to `FLY_AGENT_URL`, over HTTPS, and it does not follow redirects. Downloads
  are HTTPS too.
- The token is never logged, and an error never shows it.
- Every download is checked against a sha256 before it replaces the binary.
- The release signing key is kept offline, never on GitHub. A person who controls the GitHub
  repository alone cannot make every agent install their code. See [releasing.md](releasing.md).

## Troubleshooting

| Log line | Meaning |
|---|---|
| "must be an https URL" | `FLY_AGENT_URL` is plain http. |
| "does not accept the token" | The token in `/etc/fly/agent.env` does not match FlyWP. The agent keeps its data and tries each 5 minutes. |
| "asks the agent to wait" | FlyWP rate-limits the agent (429). |
| "a different agent is running" | A second `fly agent run` uses the same state folder. |
| "no sample for this minute" | The first minute after a start was too short. Normal. |
| "some files of a site cannot be read" | The disk walk skipped folders; the site value is lower than the real use. Logged once per folder after each start. |
| "a new release installs after its wait" | Normal: a signed release waits 24 hours. |
| "a new release waits for its signature" | The latest release is not signed yet. |
| "auto-update is off: this build has no release version" | A local build. Install a release or a dev pre-release. |
| "auto-update is off: the agent cannot write the folder of its binary" | The binary is not in a folder of the server user. Install again with `install.sh`. |

To remove the agent from a server:

```bash
sudo systemctl disable --now fly-agent
sudo rm -rf /etc/systemd/system/fly-agent.service /etc/fly /var/lib/fly-agent
sudo systemctl daemon-reload
```
