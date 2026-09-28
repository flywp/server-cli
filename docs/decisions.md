# Design decisions

Each decision of the monitoring agent, with its reason. Change one only when new evidence
contradicts the reason.

## Shape

- **One binary.** The agent is `fly agent run`, part of `fly`, not a separate program. One release,
  one install, one update path.
- **One loop.** One goroutine wakes every 10 seconds, reads the server, and at the tick of each
  minute makes the sample and, every few minutes, sends. Only the disk walk of the sites runs apart.
  The work is small, and one loop is easy to reason about after a crash.
- **Plain JSON files for state**, written to a temporary file, synced and renamed. No database: the
  queues are small, and a crash must never leave half a file. Event ids are ULIDs, made when the
  event is queued, so a resend keeps its id and FlyWP can drop duplicates.
- **HTTPS only**, except for a loopback host, which tests and local development need. Redirects are
  refused: a redirect could turn a POST into a GET, or send the token over plain http.

## Measurements

- **Peaks, not only averages.** A minute average hides a 10-second spike: 10 seconds at 100% CPU and
  50 seconds idle average to about 17%. So the agent reads every 10 seconds and sends the peak
  window of the minute next to the minute value. The minute values stay as they were, because alerts
  read them: a sustained value is an incident, a 10-second peak is not.
- **No peak of load or disk space.** The kernel already smooths the load over a minute, and disk
  space changes over minutes, not seconds.
- **Pressure (PSI)**, from the `total=` counters, not the kernel's `avg10`/`avg60` (moving averages
  that do not match the windows of a sample). "How full" is not "under pressure": memory that makes
  tasks wait is a problem at any percent.
- **Disk activity, not a busy percent.** An SSD or a cloud disk serves many requests at once, so
  "100% busy" does not mean full. The agent sends bytes and operations, and I/O pressure shows the
  waiting. Only whole hardware disks count: partitions, loop, device-mapper and RAID devices would
  count the same I/O twice.
- **Network:** only interfaces with a hardware device (and no `master`), else the default-route
  interface. Virtual interfaces would count the same traffic twice.
- **Short windows are dropped.** A reading less than 5 seconds from its neighbour is left out, and a
  first minute shorter than 10 seconds after a fresh start sends no sample. A window of a few milliseconds, or the load of
  the start itself, would show as a false spike.
- **`null`, not 0,** when a value is unknown: after a reboot, a counter that went back, or a missing
  kernel feature. A 0 would draw a false dip.
- **Waiting updates** come from `apt-check`, once an hour and not in the send budget. A failed count
  keeps the last one.

## Sites

- **A site is a Docker Compose project** whose folder is directly in the home folder of the server
  user. That is where FlyWP puts sites, and the Compose label gives the folder without guessing.
- **CPU and memory from cgroup v2**, not `docker stats`: the agent reads files and makes no extra
  Docker requests. A container counts for CPU only when both ticks saw the same container **and the
  same cgroup**: `docker restart` keeps the container id but starts a new cgroup whose CPU time
  begins at 0.
- **Only two Docker requests** (`GET /version`, `GET /containers/json`), with a 5-second timeout.
  The socket is powerful; the agent uses the least of it.
- **Disk use once an hour, in the background, at idle I/O priority**, with a 5-minute limit for each
  folder. A walk of a large site must never delay a sample or slow the sites.

## Sending

- **Keep data until FlyWP accepts it**, up to 24 hours of samples. A 400 drops the batch (it will
  never be accepted); a 401, 429, 5xx or network error keeps it.
- **Values are checked before they are queued.** One bad value makes FlyWP refuse the whole batch,
  so the agent clamps each value to the range of the contract first.
- **Samples and events have separate backoffs.** A failing event must not stop the metrics.

## Updates

- **The agent never downgrades.** An update to an older release fails with the reason. A bad
  release is fixed with a newer one. This keeps agents that updated themselves from being moved back.
- **Two update paths, and both stay:**
  - FlyWP's `agent.update` with a pinned sha256. It is fast, and it is the recovery path: it does
    not read the signature.
  - The daily automatic update, which needs a signature. Without it, a person who controls the GitHub
    repository alone could put code on every server.
- **Offline ed25519 signatures.** The maintainer signs `checksums.txt` on their own computer after
  rebuilding the release from their local tag. The key is never on GitHub or in CI. The public keys
  are compiled in.
- **A 24-hour wait after the signature**, then each server at its own time of day. A bad release can
  be stopped in that time by marking it as a pre-release.
- **A signature does not expire.**
- **Why the command path must stay:** if the key leaks, the release that removes it would itself
  need a signature, and only the leaked key could make one. The pinned-sha256 path is the way out.
- **`fly update` stays.** It restarts the agent, and it keeps the owner of the binary, so the agent
  can still replace it.

## Security

- **The agent never runs as root.** It runs as the server user under systemd.
- **Root never writes into the server user's folder.** When `install.sh` or `fly update` replaces
  the agent's binary, the file is written by the server user or changed through the open file, never
  by a path that the server user could swap for a link.
