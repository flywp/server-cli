# Releasing

`develop` is the development branch. `main` is the release branch: a release tag must be on `main`.

## A release

1. **Merge `develop` into `main`** with a pull request and a **merge commit** (not a squash, so the
   two branches keep a common history).
2. **Tag and push at once:**

   ```bash
   git checkout main && git pull --ff-only
   git tag -a v0.2.0 -m "v0.2.0"
   git push origin v0.2.0
   ```

   Push the tag right after the merge: `install.sh` from `main` installs only a release with
   `checksums.txt`, so until the release exists it stops.

   The Release workflow checks that the tag is on `main`, runs `make check`, builds the archives
   with `make release`, and publishes the release with `fly-linux-amd64.tar.gz`,
   `fly-linux-arm64.tar.gz` and `checksums.txt`. Do not rename these files: installed CLIs look for
   them.

3. **Sign it**, on your own computer (see [Signing](#signing)):

   ```bash
   make sign-release VERSION=v0.2.0 KEY=<private key file>   # KEY=- reads the key from stdin
   ```

4. **Watch it for 24 hours.** Agents install a signed release by themselves only 24 hours after the
   signature, each at its own time of day. First update one test server at once with
   `sudo fly update --yes`, and watch its log.
5. **To stop a bad release** within those 24 hours, mark it as a pre-release on GitHub. Agents no
   longer see it. After that, fix forward: the agent never downgrades.

A tag with a suffix, such as `v0.3.0-rc.1`, becomes a pre-release. `fly update`, `install.sh` and
the agents ignore pre-releases.

## Signing

The signature says: "this release is the code of my tag". Agents check it before they update
themselves. The key never goes to GitHub, so control of the GitHub repository alone is not enough to
reach every server.

`make sign-release` signs only what it can build again:

1. It shows the commit of your **local** tag and asks you to type the tag. Review that commit first:
   the signature is your approval.
2. It downloads the archives and `checksums.txt`, and checks the archives against the sums.
3. It builds the release again from your local tag, with the Go version of the CI build, and
   compares the binaries byte for byte. A binary holds its commit, so this also proves that CI built
   your tag.
4. It signs `checksums.txt`, checks the signature with the keys of the tag, and uploads
   `checksums.txt.sig`.

If an archive on GitHub was swapped, or the tag moved, step 2 or 3 stops before anything is signed.
`UPLOAD=0` does every check and writes the signature without uploading it, which is useful for a
rehearsal on a dev pre-release.

### Keys

- `make release-key KEY=<file outside the repo>` makes a key pair. It prints the public key line
  for `internal/release/keys.go`. `COMMENT="..."` names the key.
- Keep the private key in a password manager or vault, with a backup. Never commit it, and never put
  it in a GitHub secret or CI.
- `keys.go` can list more than one key. One valid signature from any listed key is enough. To
  replace a key, list both for one release, then remove the old one.
- A change to `keys.go` takes effect only in the binaries of the next release.

**If the key is lost or leaked:** make a new key, put its line in `keys.go` (remove the leaked
line), and release. Ship that release through a FlyWP `agent.update`: that path checks the sha256
that FlyWP sends and does not read the signature. This is why the command path must stay.

## Dev pre-releases

To test a branch on a real server before it merges:

```bash
make dev-version    # prints the tag, for example v0.2.0-dev.1a2b3c4
make dev-release    # tags the pushed commit; CI publishes a pre-release
```

The version is the next minor version after the latest release, plus the short commit hash
(`DEV_BASE=v0.2.1` overrides the first part). `make dev-release` refuses uncommitted changes and
commits that are not pushed. Pre-release tags can come from any branch.

Install one on a test server by hand:

```bash
tag=v0.2.0-dev.1a2b3c4 arch=amd64
base=https://github.com/flywp/server-cli/releases/download/$tag
curl -fsSLO "$base/fly-linux-$arch.tar.gz" && curl -fsSLO "$base/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "fly-linux-$arch.tar.gz" && sudo install -m 0755 "fly-linux-$arch" /usr/local/bin/fly
```

On a server that runs the agent, FlyWP can install a dev pre-release with `agent.update` instead.

## Roll back and stop

| Need | Do |
|---|---|
| Stop a signed release before agents take it | Within 24 hours of the signature, mark it as a pre-release on GitHub. |
| Fix a bad release | Release a newer, fixed version. The agent never downgrades. |
| Stop automatic updates on one server | Add `FLY_AGENT_AUTO_UPDATE=off` to `/etc/fly/agent.env` and restart `fly-agent`. FlyWP updates still work. |
| Stop the agent on one server | `sudo systemctl disable --now fly-agent` |
