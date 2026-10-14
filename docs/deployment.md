# Building, releasing and deploying Rewind

Rewind is built and shipped with three GitHub Actions workflows in
`.github/workflows/`:

| Workflow | Runs on | Does | Secrets |
| --- | --- | --- | --- |
| `ci.yml` | pull requests to `main`, pushes to `main`, manual | tests, type checks, embedded production build, smoke test, license check, installation test | none |
| `release.yml` | pushes of tags `v*` only | verifies the tag, builds and packages Linux amd64, publishes a GitHub Release | none (uses the run's own token, write access only in the publish job) |
| `deploy.yml` | manual only (`workflow_dispatch`) | builds and verifies, then joins your tailnet and deploys over SSH, through Tailscale only, after approval | SSH key and Tailscale federated-identity settings (`production` environment) |

All third-party actions are pinned to full commit SHAs (the comment next to
each pin gives the release tag). They are the official `actions/*` actions,
plus Tailscale's official `tailscale/github-action` in `deploy.yml`. The Go
toolchain comes from the `toolchain` line in `go.mod` (currently go1.27.1),
and Node.js 24 is used for the frontend.

**Status:** the workflows have **not yet run on GitHub**. Their steps were run
locally (see [Release validation](#release-validation)). The Phase 12
changes to the workflow files were reviewed by hand, but not re-checked with
actionlint.

## Continuous integration (`ci.yml`)

Four parallel jobs, all read-only (`contents: read`):

- **Go:** `gofmt`, `go mod tidy -diff`, `go mod verify`, `go vet ./...`,
  `go test -race ./...`, and a CGO-free linux/amd64 build.
- **Web:** `npm ci`, `npm run check` (svelte-check with warnings treated as
  errors, plus TypeScript), `npm test` (Vitest) and `npm run build`. The
  frontend has no lint script; `check` is its static check.
- **Embedded production build:**
  1. Builds with `scripts/build.sh`, the same script releases and deploys
     use.
  2. Runs the `rewind_embed` vet and tests.
  3. Runs `scripts/smoke-test.sh`, which starts the binary on loopback and
     checks that `/api/health` works, that `/` serves the real frontend (not
     the placeholder), and that assets have the right content type.
  4. Runs `scripts/third-party.sh`, so a new unreviewed dependency fails CI
     long before a release.
- **Installation test:** packages two binary-only release archives
  (`PACKAGE_BINARY_ONLY=1`) and runs `scripts/install-test.sh` with `sudo`
  on the disposable runner. That test installs a real systemd service with
  `install.sh` and completes first-run setup. It then indexes and streams
  generated WAV files, upgrades, and checks the database backup. It rejects
  tampered and mismatched archives, rolls back, and removes everything
  again.

Superseded runs for the same pull request are cancelled. Runs on `main`
always finish. To run CI manually: **Actions → CI → Run workflow**, or
`gh workflow run ci.yml --ref main`.

## Releases (`release.yml`)

### Creating a release

Releases are created only by pushing a version tag that points to a commit
already on `main`:

```sh
git checkout main
git pull --ff-only
git tag -a v0.1.0 -m "Rewind v0.1.0"
git push origin v0.1.0
```

Tags must look like `v1.2.3` or `v1.2.3-rc.1`. A tag with a suffix (such as
`-rc.1`) is published as a pre-release. The workflow never creates or moves
tags, and it fails instead of overwriting an existing release. To redo a
failed run, use **Re-run jobs** in the Actions tab. If a release must be
replaced, delete the release (and, if needed, the tag) on GitHub first.

There is deliberately no manual trigger for releases: every release
corresponds to exactly one pushed tag.

### What the workflow does

1. **verify:**
   - validates the tag format and checks the tagged commit is on `main`;
   - runs all Go and frontend checks (`gofmt`, tidy, `go mod verify`, vet,
     race tests, `npm run check`, `npm test`).
2. **build:**
   - builds `bin/rewind` for linux/amd64 with the frontend embedded and the
     tag as the version;
   - smoke-tests it;
   - runs `scripts/package-release.sh`. It collects license texts, builds
     the source bundles, checks that the archive contains everything an
     installation needs, adds `install.sh`, and writes checksums. It fails on
     any licensing problem;
   - extracts the packaged binary, checks that it is identical to the one
     built, and smoke-tests it again.
   - The Go module cache is not used for release builds.
3. **publish:** the only job with write access (`contents: write`). It
   verifies the checksums and creates the GitHub Release with `gh release
   create --verify-tag`.

### Release assets

| File | Contents |
| --- | --- |
| `rewind-<version>-linux-amd64.tar.gz` | `rewind` (single static binary, web interface embedded), `rewind.service` (systemd unit), `rewind.env.example` (server configuration template, from `release/rewind.env`), `LICENSE`, `README.md`, `CHANGELOG.md`, `SECURITY.md`, `THIRD_PARTY_NOTICES.md`, `LICENSES/`, `docs/` (install, configuration, operations, library, deployment, api, themes, plugins) |
| `install.sh` | the installer (`scripts/install.sh`): the interactive wizard and the scripted commands, see [install.md](install.md) |
| `uninstall.sh` | the uninstaller (`scripts/uninstall.sh`); runs the installer's uninstall wizard |
| `rewind-<version>-source.tar.gz` | Rewind's source at the tag (`git archive`) |
| `rewind-<version>-lgpl-sources.tar.gz` | corresponding source for the LGPL-2.1 components and relinking instructions (see below) |
| `SHA256SUMS` | SHA-256 checksums of the three archives, `install.sh` and `uninstall.sh` |

Only **Linux x86-64 (amd64)** is built. arm64 is a future roadmap item and
is not claimed.

The packaging script copies an explicit list of files and then refuses to
publish if an archive contains `.env` files, databases, `setup-code`, keys or
audio files.

### Verifying a download

```sh
VERSION=v0.1.0
base="https://github.com/shamil-tp/rewind/releases/download/$VERSION"
curl -fLO "$base/rewind-$VERSION-linux-amd64.tar.gz"
curl -fLO "$base/install.sh"
curl -fLO "$base/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing   # must print: ... OK for each file
```

Then install with `install.sh`, or by hand ([install.md](install.md)).

The checksum file comes from the same release page, so it detects corrupted
downloads, not a compromised GitHub account. Releases are not signed yet.

### Licensing of release binaries

Rewind is Apache-2.0. The binary also contains third-party code, and
distributing it carries obligations that the release process implements:

- **BSD, MIT and Apache-2.0 modules.** Their license texts, and wazero's
  Apache-2.0 `NOTICE`, are copied into `LICENSES/` and summarized in
  `THIRD_PARTY_NOTICES.md`. The module list is read from the built binary
  itself.
- **Go standard library and runtime.** The Go license is included.
- **Frontend code.** The Svelte runtime and Vite's preload helper (MIT) are
  compiled into the embedded web interface; their licenses are included.
- **`go.senan.xyz/taglib` (LGPL-2.1)**, and TagLib 2.1.1 inside its
  `taglib.wasm`:
  - The LGPL text and notices are in `LICENSES/go.senan.xyz/taglib/`.
  - The **complete corresponding source** is published on the same release
    page in `rewind-<version>-lgpl-sources.tar.gz`. It contains the exact
    go-taglib module zip, plus TagLib and utfcpp at the pinned commits, which
    go-taglib's module download does not include.
  - It also contains instructions for rebuilding `taglib.wasm` and
    relinking Rewind.
  - Rewind's full source is published too, and Apache-2.0 permits
    modification and reverse engineering.
- **Components inside `taglib.wasm`** (utfcpp, wasi-libc including musl
  code, libc++) have license texts reviewed and stored in `release/notices/`,
  with their exact upstream commits listed.

**Automated checks that stop a release:**

- `scripts/third-party.sh` fails if the binary links a module that is not
  listed in `release/third-party-modules.txt` (an unreviewed dependency),
  if a linked module has no license file, or if the npm license files are
  missing.
- `scripts/lgpl-sources.sh` fails if `go.mod` requires a go-taglib version
  other than the reviewed `v0.14.0`, or if a downloaded source archive does
  not match its pinned SHA-256.

These checks verify *that the materials exist and match what was reviewed*.
They do not decide legal questions. This is an engineering implementation of
the obligations identified in `docs/architecture.md` §4, not legal advice.

### Release checklist (manual)

Before tagging:

- [ ] CI is green on the commit you will tag, including the installation
  test.
- [ ] `sh scripts/release-check.sh vX.Y.Z-rc.N` passes on a Linux machine
  from a clean checkout of that commit (see
  [Release validation](#release-validation)). Run it with
  `RELEASE_CHECK_INSTALL=1` on a disposable VM, if you have one.
- [ ] `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` reports no
  vulnerability that affects Rewind, and `npm audit` in `web/` is reviewed
  (the frontend has build-time dependencies only).
- [ ] If dependencies changed (`go.mod`, `go.sum`, `web/package-lock.json`):
  - [ ] For each new Go module, read its license, then add it to
    `release/third-party-modules.txt`.
  - [ ] If **go-taglib** changed: check its new TagLib submodule commit and
    wasm build toolchain, refresh `release/notices/` from the exact upstream
    commits, and update the pins and checksums in `scripts/lgpl-sources.sh`.
    Also check that it still caches compiled code under `os.TempDir()`
    (security-audit.md, F8).
  - [ ] If a frontend runtime dependency besides Svelte was added, add its
    license to `scripts/third-party.sh`.
- [ ] Database migrations added since the last release are listed in
  `CHANGELOG.md`, with a note that older releases cannot open a migrated
  database.
- [ ] `CHANGELOG.md`: rename **Unreleased** to the version and date.
- [ ] `README.md` and `docs/` describe the release accurately; no
  unimplemented feature or untested platform is claimed.
- [ ] If `release/rewind.service` changed, the changelog says so: upgrades
  keep the installed unit and only print a `diff` command.

After the release is published:

- [ ] Download the four assets and `SHA256SUMS`, and run
  `sha256sum -c SHA256SUMS`.
- [ ] Unpack the binary archive and check `THIRD_PARTY_NOTICES.md` and
  `LICENSES/`.
- [ ] Open `rewind-<version>-lgpl-sources.tar.gz` and confirm it contains
  `go-taglib-v0.14.0.zip`, the TagLib and utfcpp archives, and `README.md`.
- [ ] Install the release on a clean Ubuntu or Debian machine with
  `install.sh` and follow [install.md](install.md) once, by hand: setup
  through an SSH tunnel, a real USB drive, playback in a browser.

### Release validation

`scripts/release-check.sh VERSION [OUTDIR]` runs on Linux what CI and the
release workflow run, without tagging or publishing:

1. gofmt, `go mod tidy -diff`, `go mod verify`, `go vet`;
2. Go tests, with `-race` when a C compiler is available (the summary says
   when it is not);
3. `npm ci`, `npm run check` and `npm test`;
4. the release build (`scripts/build.sh`), the `rewind_embed` tests, and the
   smoke test;
5. `scripts/package-release.sh`, `sha256sum -c`, a listing of the archive,
   and a smoke test of the binary taken from the archive;
6. `sh -n` (or `bash -n` for bash scripts) on every script, shellcheck if
   it is installed, and `scripts/install-unit-test.sh`;
7. with `RELEASE_CHECK_INSTALL=1`, `scripts/install-test.sh` and
   `scripts/wizard-test.sh` through `sudo`. These tests change the machine;
   they refuse to run if Rewind is already installed, and remove what they
   created.

`RELEASE_CHECK_BINARY_ONLY=1` skips the source and LGPL archives, which
lets it run with uncommitted changes.

**Record for Phase 12 (2026-10-12).** Run on a development machine, not on
GitHub. Linux checks ran in WSL2 Ubuntu 26.04 (kernel 6.6, systemd 259,
Go 1.27.2, Node.js 24.12.0), on a committed copy of the working tree:

| Check | Result |
| --- | --- |
| `release-check.sh v0.0.3-check.1`: gofmt, tidy, `go mod verify`, vet, `go test -race ./...`, `npm run check`, `npm test` (182 tests), release build, `rewind_embed` tests, smoke test, full packaging with LGPL sources, `sha256sum -c`, smoke test of the archived binary, `sh -n` | passed |
| LGPL source downloads (TagLib, utfcpp) against their pinned SHA-256 | matched |
| `install-test.sh` as root (21 checks: install, setup, scan and stream under the hardened unit, a late mount, refused tampered and mismatched archives, upgrade with backup, rollback, clean stop), from a v0.0.1 archive to v0.0.3 | passed; nothing was left on the machine |
| Go tests, vet, frontend check and tests, on Windows 11 | passed |
| `govulncheck` v1.9.0 (source mode) | no vulnerability affecting Rewind. It lists GO-2026-5932 (`x/crypto/openpgp`), whose package is not linked: only `argon2` and `blake2b` are. Binary mode reports it as affecting the binary, because the stripped release binary has no symbol table. |
| `npm audit` (build-time dependencies only) | 0 vulnerabilities |
| shellcheck | **not run** (not installed) |
| `ci.yml`, `release.yml` and `deploy.yml` on GitHub | **not run yet** |
| Ubuntu 24.04, Debian 12 and 13, a real USB drive, real browsers, LAN, Tailscale, `tailscale serve` | **not tested** |

**Record for the installation wizard (2026-10-14).** Run on the same
development machine, in WSL2 Ubuntu 26.04 (systemd 259), against the
published v1.1.0 and v1.2.0 archives (downloaded and checked with
`sha256sum -c`), with the new `install.sh` placed next to them:

| Check | Result |
| --- | --- |
| shellcheck 0.10.0 on every changed script (`install.sh`, `uninstall.sh`, the three installer tests, `release-check.sh`, `package-release.sh`) | no findings. `deploy-remote.sh`, unchanged, has three info-level SC2015 findings |
| `bash -n` on `install.sh` and `uninstall.sh` | passed |
| `install-unit-test.sh` as a normal user (validation, configuration editing, network-mode detection, drop-in, installation record, uninstall planning on a scratch tree, prompts, non-root command line) | 261 passed |
| `install-test.sh` as root, scripted subcommands run with `sh` (re-run with bash), v1.1.0 to v1.2.0 | passed (22 checks) |
| `wizard-test.sh` as root, with `WIZARD_TEST_DOWNLOAD=v1.1.0` (install from a local archive, group access, LAN reconfiguration, a failing reconfiguration rolled back, update, repair, all three uninstall options, reinstall over kept data, download of v1.1.0 and update to the latest release from GitHub) | passed (24 checks); nothing was left on the machine |
| package installation through `apt-get` (no package was missing) | **not exercised** |
| Ubuntu 24.04 (systemd 255: no `WantsMountsFor=` question), a real Ubuntu Server, a real USB drive, Tailscale modes with a real tailnet | **not tested** |
| `release-check.sh`, the CI jobs with the new steps, packaging with `uninstall.sh` | **not run** (no Go or Node.js in that WSL distribution) |

One detail came up on Ubuntu 26.04: its `/usr/bin/test` (rust-coreutils)
ignores supplementary groups for `-r` and `-x`, so the installer checks
folder access with bash's built-in `test`, which asks the kernel.

Two details are specific to WSL. `/` is not a shared mount there, so a
drive mounted after start never reaches a sandboxed service; the test uses
a folder under `/run`, which is shared. The race detector worked because
WSL had gcc installed.

## Installing a release

See [install.md](install.md): `install.sh install`, or the equivalent manual
steps. Upgrades, rollback and backups are in [operations.md](operations.md).
`install.sh` and `deploy.yml` use the same layout (`/opt/rewind/releases/`,
`/opt/rewind/current`, `/etc/rewind/rewind.env`, `/var/lib/rewind`), so a
server installed with `install.sh` can later be deployed to by the workflow,
once the [server preparation](#server-preparation-for-deployyml) below is
done. For that, give the deploy user ownership of the deployment
directories, non-recursively: `sudo chown rewind-deploy /opt/rewind
/opt/rewind/releases`.

## Network access

By default Rewind listens on `127.0.0.1:8090` and **only the server itself
can open it**. Other devices need one of the opt-in layouts below. Rewind
never opens firewall ports, configures port forwarding or a public reverse
proxy, or talks to Tailscale. You stay in control of the network.

**Before enabling any of them, complete the first-run setup on the server
itself** (`http://localhost:8090`). Setup only works from the server, with a
loopback address in the browser's address bar. It is never available from
another device, through `tailscale serve` or through a LAN address.

Being on your Wi-Fi or tailnet does **not** sign anyone in: every library,
stream and admin request still needs your Rewind password and session.

| Layout | Encryption | Installable app (PWA) | Settings |
| --- | --- | --- | --- |
| **A. Tailscale with HTTPS (recommended)** | HTTPS, plus WireGuard | yes | loopback `REWIND_ADDR`, `REWIND_TRUSTED_HOSTS`, `REWIND_COOKIE_SECURE=true` |
| B. Tailscale IP address | WireGuard (plain HTTP inside the tunnel) | no (not a secure context) | `REWIND_REMOTE_ACCESS=true`, Tailscale IP in `REWIND_ADDR` |
| C. Home network (LAN) | **none**: plain HTTP over your Wi-Fi | no | `REWIND_REMOTE_ACCESS=true`, LAN IP in `REWIND_ADDR` |

### A. Tailscale with HTTPS (recommended)

Rewind stays on loopback. `tailscale serve`, running on the server, accepts
HTTPS connections from your tailnet and forwards them to Rewind. Nothing is
reachable from outside your tailnet.

1. In the Tailscale admin console, enable **MagicDNS** and **HTTPS
   certificates** (see Tailscale's documentation for `tailscale serve`; the
   exact steps and command syntax depend on your Tailscale version).
2. Find the server's tailnet name, for example `rewind.tail1234.ts.net`
   (`tailscale status` on the server).
3. On the server, proxy that name to Rewind, for example:

   ```sh
   sudo tailscale serve --bg http://127.0.0.1:8090
   tailscale serve status      # shows https://rewind.tail1234.ts.net/ -> http://127.0.0.1:8090
   ```

   Use `tailscale serve`, **not** `tailscale funnel`: Funnel publishes the
   service on the public internet.
4. In `/etc/rewind/rewind.env`:

   ```sh
   REWIND_ADDR=127.0.0.1:8090
   REWIND_TRUSTED_HOSTS=rewind.tail1234.ts.net
   REWIND_COOKIE_SECURE=true
   ```

   Then `sudo systemctl restart rewind`.
5. On a device in your tailnet, open `https://rewind.tail1234.ts.net/` and
   sign in. Local access on the server through `http://localhost:8090` keeps
   working in Chromium-based browsers and Firefox, which accept `Secure`
   cookies on localhost.

Notes:

- `tailscale serve` forwards the browser's `Host` (your `*.ts.net` name),
  which is why that name must be in `REWIND_TRUSTED_HOSTS`. If you get
  `forbidden_host`, Rewind's log names the host it received.
- Rewind sees every proxied request as coming from `127.0.0.1`. It ignores
  forwarding headers on purpose, so sign-in rate limiting is shared by all
  devices using the proxy (see [architecture.md](architecture.md), "Login
  abuse controls").
- With `REWIND_COOKIE_SECURE=true`, existing sessions use a different cookie
  name, so everyone signs in once more after the change.

### B. Tailscale IP address (plain HTTP inside the tunnel)

Rewind listens directly on the server's Tailscale address. Traffic is
encrypted by Tailscale (WireGuard), but browsers treat the page as plain
HTTP, so the app cannot be installed.

```sh
REWIND_REMOTE_ACCESS=true
REWIND_ADDR=127.0.0.1:8090,100.101.102.103:8090   # your address from `tailscale ip -4`
REWIND_TRUSTED_HOSTS=rewind.tail1234.ts.net       # optional: to use the MagicDNS name instead of the IP
REWIND_COOKIE_SECURE=false
```

- Keep `127.0.0.1:8090` in the list so the server itself can still reach
  Rewind (and run setup or the deployment health check).
- The listen IP itself is trusted automatically; names must be listed.
- The Tailscale address must exist when Rewind starts, otherwise Rewind
  exits with "cannot listen on …". Add `After=tailscaled.service` and
  `Wants=tailscaled.service` under `[Unit]` in a drop-in
  (`sudo systemctl edit rewind`). The unit's `Restart=on-failure` retries
  if the address appears a little later.
- For a LAN address (layout C), add `After=network-online.target` and
  `Wants=network-online.target` the same way.
- Rewind prints a startup warning about plain HTTP. Over Tailscale the
  tunnel is encrypted, so this is expected.

### C. Home network (LAN, plain HTTP)

```sh
REWIND_REMOTE_ACCESS=true
REWIND_ADDR=127.0.0.1:8090,192.168.1.20:8090   # the server's LAN address
REWIND_COOKIE_SECURE=false
```

Then open `http://192.168.1.20:8090` on another device on the same network.

- **Your password and session cookie travel unencrypted over the LAN.**
  Anyone who can watch your Wi-Fi traffic could capture them. Use this only
  on a network you trust, and prefer layout A when you can.
- Give the server a fixed LAN address (a DHCP reservation in your router),
  because Rewind exits at startup if the address is not assigned to the
  machine.
- A local name such as `music-box.lan` works if you add it to
  `REWIND_TRUSTED_HOSTS`.
- Your OS firewall may block the port. Rewind does not change firewall
  rules. If you open the port, allow it only from your LAN.
- The app cannot be installed and has no offline page here, because
  browsers allow service workers only over HTTPS or on localhost.
  Playback works normally.

### Listening on all interfaces

`0.0.0.0:8090` or `[::]:8090` listens on every interface, including any
public one the server has. Rewind refuses this unless both
`REWIND_REMOTE_ACCESS=true` and `REWIND_LISTEN_ALL_INTERFACES=true` are set,
and then logs a warning at every start. A specific address (layouts B and C)
is safer. With a wildcard, the server's IP addresses are **not** trusted
automatically: list every name and address you use in
`REWIND_TRUSTED_HOSTS`.

### Settings reference

| Variable | Default | Meaning |
| --- | --- | --- |
| `REWIND_ADDR` | `127.0.0.1:8090` | comma-separated `host:port` list (max 8). Hosts must be IP addresses or `localhost` |
| `REWIND_REMOTE_ACCESS` | `false` | allow non-loopback addresses in `REWIND_ADDR` |
| `REWIND_LISTEN_ALL_INTERFACES` | `false` | also allow `0.0.0.0`, `[::]` or `:port` (needs `REWIND_REMOTE_ACCESS=true`) |
| `REWIND_TRUSTED_HOSTS` | (none) | comma-separated host names or IPs accepted in the `Host` header, besides loopback and the listen IPs. Exact names only: no `*`, ports, schemes or paths |
| `REWIND_COOKIE_SECURE` | `false` | `true` when remote clients use HTTPS (layout A) |

Every invalid value stops startup with an error that names the setting.
Rewind never falls back to a different address.

### Installing the app (PWA)

Installation needs HTTPS (layout A) or `http://localhost`.

- **Android (Chrome):** open Rewind, then use the menu's **Install app** or
  **Add to Home screen**.
- **iPhone and iPad (Safari):** Share → **Add to Home Screen**.
- **Desktop (Chrome or Edge):** use the install icon in the address bar.

The installed app caches only Rewind's own interface files, so it opens
quickly and shows a "Can't reach your Rewind server" page when the server is
offline. **Music is never stored on the device**, so playback needs the
server. Sign-in, library data and audio are never cached. After an
upgrade, the new interface is used from the next page load.

### Troubleshooting network access

| Symptom | Cause and fix |
| --- | --- |
| Startup error "… requires REWIND_REMOTE_ACCESS=true" | A LAN or Tailscale address is in `REWIND_ADDR`. Set `REWIND_REMOTE_ACCESS=true` deliberately, or use layout A. |
| Startup error "… listens on all network interfaces …" | Use a specific address, or set both flags (see above). |
| Startup error "use an IP address; host names other than localhost are not accepted" | Put the IP address in `REWIND_ADDR`, and the name in `REWIND_TRUSTED_HOSTS`. |
| Startup error "cannot listen on …" | The address is not assigned yet (Tailscale not up, DHCP changed it) or the port is in use. |
| Browser shows `{"error":{"code":"forbidden_host",…}}` | The name in the address bar is not trusted. Add it to `REWIND_TRUSTED_HOSTS`. The log line "rejected request for an untrusted host name" shows the name. |
| Sign-in succeeds but you are signed out again at once | `REWIND_COOKIE_SECURE=true` over plain HTTP: the browser drops `Secure` cookies. Use HTTPS (layout A) or set it to `false`. |
| "setup is only available from the server itself" | Run setup on the server at `http://localhost:8090`. |
| No install option | The page is not HTTPS or localhost, or the browser does not support installing web apps. |
| Connection times out from another device | Firewall, wrong address, or the device is not on the LAN or tailnet. Rewind logs "remote access is enabled" with the reachable addresses at startup. |

Rewind's automated tests cover these rules with loopback connections and
simulated LAN and Tailscale client addresses. They do not exercise a real
LAN, a real tailnet or `tailscale serve`. Check your own setup with the
steps above.

## Deployment (`deploy.yml`)

Deployment is **manual**. Nothing deploys on push. Automatic deployment can
be enabled later by adding a `push` trigger; that has not been done.

### Network path: Tailscale

The server is reached **only through your Tailscale network (tailnet)**. Its
SSH port never needs to be exposed to the internet, and no firewall changes
are needed.

- The deploy job runs on a GitHub-hosted runner (`ubuntu-24.04`), like the
  rest of the project's workflows. That runner briefly joins your tailnet
  with Tailscale's official action, `tailscale/github-action` v4.2.0, pinned
  to commit `d1b6cd20…`.
- **Authentication uses workload identity federation (OIDC):**
  - GitHub issues a short-lived token for this job (`permissions: id-token:
    write`).
  - Tailscale exchanges it for a node key, according to a trust credential
    you create.
  - No long-lived Tailscale key or OAuth secret is stored anywhere.
- The runner joins as an **ephemeral, pre-approved node** with the tag
  `tag:rewind-deploy` (or `TS_TAGS`). It logs out in the action's post step
  and is then removed from the tailnet.
- The Tailscale client version is **pinned (1.104.1) and its download is
  verified** against its published SHA-256. The binary cache is turned off,
  so a cached copy can't skip that check.
- `DEPLOY_HOST` must be the server's Tailscale name (`*.ts.net`) or Tailscale
  IP (`100.64.0.0/10`); anything else is rejected.
  - The workflow resolves the name with `tailscale ip -4` and connects to
    that **Tailscale IP**, so the traffic cannot take a non-tailnet route.
  - The SSH host key is still verified against `DEPLOY_KNOWN_HOSTS` under the
    configured name (`HostKeyAlias`), with `StrictHostKeyChecking yes`.
- Normal OpenSSH key authentication is used over the tailnet (the
  `DEPLOY_SSH_KEY` deploy key). Tailscale SSH is not used.

**Why not a self-hosted runner on the server?** It was considered:

- A runner would be a permanent extra service on the personal server, with
  access to the deployment directory.
- It would need continuous runner updates.
- A mistake in workflow triggers could let it run untrusted code.

The GitHub-hosted runner with an ephemeral, tag-restricted tailnet node keeps
the server unchanged apart from SSH access for one dedicated user, and
matches the existing workflow. If you later prefer a self-hosted runner, it
should get a dedicated label (for example `rewind-deploy`), be used only by
`deploy.yml` (never by `ci.yml` or pull requests), and run as an unprivileged
user. That would be a separate change.

### What it does

1. **Build and verify** (no secrets, no SSH):
   - only runs for `main` or a `v*` tag, and fails for any other ref;
   - runs vet, race tests, `npm run check` and `npm test`;
   - builds the binary with the embedded frontend, smoke-tests it, and
     uploads it with its checksum and `scripts/deploy-remote.sh`.
2. **Deploy to production** (runs in the `production` environment, so it
   waits for approval if you configure reviewers; its only token permission
   is `id-token: write`, used for the Tailscale OIDC exchange):
   1. Checks that every required variable and secret is present and
      well-formed, including that `DEPLOY_HOST` is a tailnet address. Missing
      configuration fails the run before anything connects. Values are not
      printed, and each secret is visible only to the step that uses it.
   2. Verifies the artifact checksum.
   3. **Joins the tailnet** (see above), waiting until the server answers
      `tailscale ping`, then resolves the server's Tailscale IP.
   4. Writes the SSH key and the trusted `known_hosts` entry to files.
      `StrictHostKeyChecking yes` is always on; host-key checking is never
      disabled.
   5. **Preflight** on the server, changing nothing. It checks that:
      - the deployment directory and its `releases/` directory exist and are
        writable;
      - `current`, if present, is a symlink;
      - the systemd unit exists and its `ExecStart` runs
        `<DEPLOY_DIR>/current/rewind`;
      - the deploy user may run `sudo systemctl restart <service>` without a
        password;
      - `curl`, `sha256sum` and (for backups) `sqlite3` are available.
   6. **Upload** the binary to `releases/.incoming-<release-id>`.
   7. **Activate:**
      - verify the uploaded file's SHA-256 and move it to
        `releases/<release-id>/rewind`;
      - optionally back up the database with `sqlite3 .backup`;
      - atomically repoint the `current` symlink and restart the service;
      - poll the loopback health URL until it reports `"status":"ok"` and
        the **new version**.
   8. **Automatic rollback** if the new release is not healthy within about
      30 seconds: `current` is pointed back to the previous release, the
      service is restarted and checked, and the run fails with a clear
      message.
   9. The SSH key files are removed from the runner, and the Tailscale node
      logs out (the action's post step).

Deployment **never deletes anything** on the server: not the data
directory, the database, configuration, music, or old releases. It does not
change the bind address, authentication, firewall, or network settings, and
uploads only the binary (no music, no databases).

### Required configuration

Create an environment named **`production`** (Settings → Environments) and
add these to it:

| Name | Kind | Required | Example (not real) | Purpose |
| --- | --- | --- | --- | --- |
| `DEPLOY_SSH_KEY` | secret | yes | contents of a dedicated `id_ed25519` | private key of the deploy user (no passphrase) |
| `TS_OAUTH_CLIENT_ID` | secret | yes | client ID of the Tailscale trust credential | federated identity client ID (the action's `oauth-client-id` input) |
| `TS_AUDIENCE` | secret | yes | audience shown for that trust credential | federated identity audience (the action's `audience` input) |
| `TS_TAGS` | variable | no | `tag:rewind-deploy` (default) | tag(s) for the runner's ephemeral node; must match the trust credential |
| `DEPLOY_KNOWN_HOSTS` | variable | yes | `rewind.tail1234.ts.net ssh-ed25519 AAAA...` | trusted host key line(s), keyed by the **same name as `DEPLOY_HOST`** |
| `DEPLOY_HOST` | variable | yes | `rewind.tail1234.ts.net` or `100.101.102.103` | the server's Tailscale MagicDNS name or Tailscale IPv4 address |
| `DEPLOY_USER` | variable | yes | `rewind-deploy` | SSH user |
| `DEPLOY_DIR` | variable | yes | `/opt/rewind` | directory containing `releases/` and `current` |
| `DEPLOY_SERVICE` | variable | yes | `rewind.service` | systemd unit to restart |
| `DEPLOY_PORT` | variable | no | `22` | SSH port (default 22) |
| `DEPLOY_HEALTH_URL` | variable | no | `http://127.0.0.1:8090/api/health` | loopback health URL on the server; must match `REWIND_ADDR` (default shown) |
| `DEPLOY_DB_PATH` | variable | no | `/var/lib/rewind/rewind.db` | if set, back up this database before switching |
| `DEPLOY_DB_BACKUP_DIR` | variable | with `DEPLOY_DB_PATH` | `/var/backups/rewind` | where backups are written (writable by the deploy user) |

**Approval and branch rules:**

- In the `production` environment, enable **Required reviewers** and add
  yourself. The deploy job then waits for approval. Environment protection
  rules are available for public repositories; for private repositories,
  availability depends on your GitHub plan (check GitHub's "Managing
  environments for deployment" documentation). Without reviewers the job runs
  as soon as the build succeeds.
- Also set **Deployment branches and tags** to `main` and `v*`.
- Pull requests from forks never receive these secrets, and `deploy.yml`
  cannot be triggered by a pull request.

`DEPLOY_KNOWN_HOSTS` must come from a channel you trust: run `ssh-keyscan`
**on the server itself**, or compare fingerprints with
`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the server. The line
must start with exactly the value of `DEPLOY_HOST`:

```sh
# On the server (replace with your server's Tailscale name):
ssh-keyscan -t ed25519 localhost | sed "s/^localhost/rewind.tail1234.ts.net/"
```

### Tailscale setup

Do this once in the Tailscale admin console. Follow Tailscale's current
documentation for the exact screens:
[workload identity federation](https://tailscale.com/kb/1581/workload-identity-federation),
[trust credential scopes](https://tailscale.com/kb/1623/trust-credentials#scopes),
[tags](https://tailscale.com/kb/1068/tags).

1. **Put the server on the tailnet** and note its MagicDNS name (for example
   `rewind.tail1234.ts.net`, from `tailscale status` on the server) or its
   Tailscale IPv4 address (`tailscale ip -4`). Optionally give the server a
   tag such as `tag:rewind-server`, and disable key expiry for it so it stays
   reachable.
2. **Define the tags and access rule** in the tailnet policy file. Allow the
   CI tag to reach **only** SSH on the server. For example, using the
   policy's `tagOwners` and `acls` sections:

   ```jsonc
   "tagOwners": {
     "tag:rewind-deploy": ["autogroup:admin"],
     "tag:rewind-server": ["autogroup:admin"],
   },
   "acls": [
     // ...your existing rules...
     {"action": "accept", "src": ["tag:rewind-deploy"], "dst": ["tag:rewind-server:22"]},
   ],
   ```

   If your policy uses `grants` instead, write the equivalent grant. Make sure
   no other rule gives `tag:rewind-deploy` broader access.
3. **Create a trust credential** of the OpenID Connect (federated identity)
   type for GitHub Actions:
   - Issuer: GitHub (`https://token.actions.githubusercontent.com`).
   - Restrict the subject to this repository's **production environment**,
     for example `repo:shamil-tp/rewind:environment:production`, so that only
     the approved deploy job can obtain a node.
   - Give it the writable `auth_keys` scope with the tag `tag:rewind-deploy`.

   Tailscale shows a **client ID** and an **audience**. Store them as the
   `TS_OAUTH_CLIENT_ID` and `TS_AUDIENCE` secrets in the `production`
   environment.
4. If your tailnet uses **Tailnet Lock**, Tailscale's action documents using
   a pre-signed auth key (and a state directory) instead of an OAuth client.
   This workflow does not implement that variant.

### Server preparation for `deploy.yml`

The repository does not install anything on your server. Prepare it once, as
an administrator. The paths below are examples; use your own and set the
variables to match.

1. **Users and directories:**

   ```sh
   sudo useradd --system --home /var/lib/rewind --shell /usr/sbin/nologin rewind
   sudo useradd --create-home --shell /bin/sh rewind-deploy
   sudo install -d -o rewind -g rewind -m 0700 /var/lib/rewind          # data: never touched by deploys
   sudo install -d -o rewind-deploy -g rewind-deploy -m 0755 /opt/rewind /opt/rewind/releases
   ```

2. **Deploy key:** generate a dedicated key pair (`ssh-keygen -t ed25519 -N ""
   -f rewind-deploy`). Put the public key in
   `/home/rewind-deploy/.ssh/authorized_keys` and the private key in the
   `DEPLOY_SSH_KEY` secret. Do not reuse a personal key.

3. **Configuration:** `/etc/rewind/rewind.env`, owned by root, mode 0640, group
   `rewind`, based on `.env.example`. Set `REWIND_DATA_DIR=/var/lib/rewind`
   and `REWIND_LIBRARY_ROOTS`. Keep `127.0.0.1:8090` in `REWIND_ADDR` (the
   deployment health check uses it); add remote access only as described in
   [Network access](#network-access).

4. **systemd unit** `/etc/systemd/system/rewind.service`: use
   [`release/rewind.service`](../release/rewind.service), which is also
   included in every release archive as `rewind.service`. Its `ExecStart` is
   `/opt/rewind/current/rewind`, as the preflight check requires. It runs
   as `rewind` with `ProtectSystem=strict`, so everything except
   `/var/lib/rewind` is read-only for Rewind, including the music mount.
   It also uses `PrivateTmp`, and restricts kernel access, namespaces,
   capabilities and address families. Change it with
   `sudo systemctl edit rewind`.

5. **Restart permission:** allow exactly one command, with `sudo visudo -f
   /etc/sudoers.d/rewind-deploy`:

   ```
   rewind-deploy ALL=(root) NOPASSWD: /usr/bin/systemctl restart rewind.service
   ```

   Use the same unit name in `DEPLOY_SERVICE` (here `rewind.service`).

6. **First release:** either run the deploy workflow (it creates `current` on
   the first deployment), or install one manually:

   ```sh
   sudo -u rewind-deploy install -d /opt/rewind/releases/manual-1
   sudo -u rewind-deploy install -m 0755 rewind /opt/rewind/releases/manual-1/rewind
   sudo -u rewind-deploy ln -sfn releases/manual-1 /opt/rewind/current
   sudo systemctl daemon-reload && sudo systemctl enable --now rewind.service
   ```

7. **Optional database backups:** `sudo apt install sqlite3`, create
   `/var/backups/rewind` owned by `rewind-deploy` with mode 0700 (`sudo
   install -d -o rewind-deploy -m 0700 /var/backups/rewind`; backups contain
   the password hash, and the deploy script writes them with mode 0600), and
   add `rewind-deploy`
   to the `rewind` group with the database readable by the group. Alternatively,
   leave `DEPLOY_DB_PATH` unset and back up as described in `architecture.md`.

8. **Packages:** `curl` and `coreutils` (`sha256sum`) must be installed.

SSH only needs to be reachable **over Tailscale**; do not expose it publicly
for deployments. To restrict OpenSSH further, you can make it listen only on
the Tailscale address (`ListenAddress 100.x.y.z` in `sshd_config`), or rely
on your existing host firewall. The workflow does not change either. Rewind
itself stays on loopback; the health check runs on the server through SSH.

### Deploying

**Actions → Deploy → Run workflow**, choose `main` or a tag, then approve the
`production` deployment if reviewers are configured. Or:

```sh
gh workflow run deploy.yml --ref main
gh workflow run deploy.yml --ref v0.1.0
```

### Troubleshooting the Tailscale connection

| Symptom | Likely cause and fix |
| --- | --- |
| "Missing secret TS_OAUTH_CLIENT_ID / TS_AUDIENCE" | Secrets are not set **in the `production` environment** (repository-level secrets with other names are not used). |
| "DEPLOY_HOST must be the server's Tailscale name…" | Use the MagicDNS name (`*.ts.net`) or the `100.x.y.z` address, not a LAN IP or public name. |
| Tailscale step: `requested tags [...] are invalid or not permitted` | `TS_TAGS` does not match the tags on the trust credential, or the tag is missing from `tagOwners`. |
| Tailscale step fails to authenticate | The trust credential's issuer or subject does not match (the subject must allow `repo:<owner>/<repo>:environment:production`), the audience is wrong, or Tailnet Lock is enabled. |
| `ping` in the Tailscale step times out | The server is offline in the tailnet, or the ACL does not allow `tag:rewind-deploy` to reach it. New nodes can also take a moment to propagate; rerun once. |
| "Could not resolve … to a Tailscale IPv4 address" | The name is not a peer visible to the runner node (check the ACL and the exact MagicDNS name with `tailscale status` on the server). |
| SSH `Host key verification failed` | `DEPLOY_KNOWN_HOSTS` does not start with exactly the `DEPLOY_HOST` value, or the server's host key changed. Re-create the line on the server, never by disabling checks. |
| SSH `Connection refused` / timeout after ping works | sshd is not listening on the Tailscale interface or port `DEPLOY_PORT`, or the ACL allows ping but not TCP 22. |
| SSH `Permission denied (publickey)` | The deploy public key is not in `~rewind-deploy/.ssh/authorized_keys`, or `DEPLOY_USER` is wrong. |

### Recovering from a failed deployment

- **Failure before "Activate"** (configuration, preflight, upload): nothing
  on the server was switched. Fix the reported problem and run again. A
  leftover `releases/.incoming-*` file can be deleted by hand.
- **New release unhealthy:** the workflow rolls back automatically and says
  whether the previous release is healthy again. Investigate with
  `journalctl -u rewind.service` on the server. Logs are not copied into
  GitHub Actions, because they can contain file names from your library.
- **Rollback reported "NOT healthy":** the failed release probably applied a
  database migration that the previous release cannot open. Restore the
  backup made just before the switch (`DEPLOY_DB_BACKUP_DIR/rewind-before-<id>.db`)
  while the service is stopped, as described in `architecture.md`, "Backup
  and restore". Then restart.
- **Manual rollback** at any time:

  ```sh
  ls -l /opt/rewind/releases/            # pick a previous release
  sudo -u rewind-deploy ln -sfn releases/<id> /opt/rewind/.current-new
  sudo -u rewind-deploy mv -T /opt/rewind/.current-new /opt/rewind/current
  sudo systemctl restart rewind.service
  curl -fsS http://127.0.0.1:8090/api/health
  ```

- **First deployment failed:** there is no previous release to roll back to.
  The service is left on the new release. Check `journalctl`, fix, and deploy
  again.

Old releases accumulate in `releases/`. Remove ones you no longer need by
hand; the workflow never deletes them.

### What remains manual

- Preparing the server (users, directories, unit, sudoers rule, configuration,
  music mount).
- Creating the `production` environment, its reviewers, branch rules,
  variables and secrets.
- Tailscale: putting the server on the tailnet, defining the tags and the
  ACL rule, and creating the federated-identity trust credential (see
  [Tailscale setup](#tailscale-setup)).
- Cleaning up old releases and backups.
- Enabling automatic deployment on push, if wanted later.
