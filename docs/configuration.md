# Configuration reference

Rewind is configured with environment variables only. It reads no
configuration file of its own and **does not load `.env` files**.

- **Installed as a service:** the variables come from
  `/etc/rewind/rewind.env`, which systemd reads through `EnvironmentFile=`
  in `rewind.service`. Write plain `KEY=value` lines. Lines starting with `#`
  are comments. Run `sudo systemctl restart rewind` after a change: settings
  are read only at startup. `sudo rewind-ctl --reconfigure` changes the
  network, music, logging, scanning and session settings for you, backs up
  the file first, and restores it if Rewind does not become healthy
  ([install.md](install.md#managing-an-installation-rewind-ctl)). It keeps
  every other line as it is. Values must stay on one line, without quotes.
- **In development:** set the variables in your shell, or load a copy of
  `.env.example` into it (see [development.md](development.md#configuration)).

Every setting is optional. Leaving a setting out, or setting it to an empty
value, means the default. Invalid values stop startup, and the error names
every invalid setting, so all of them can be fixed in one pass.
Rewind never falls back to another value. With the service, the error is in
`journalctl -u rewind`.

Two templates are provided:

| File | For |
| --- | --- |
| `rewind.env.example` in release archives (`release/rewind.env` in the source) | a server: data in `/var/lib/rewind`, loopback only |
| `.env.example` in the source | development: every setting with its default |

Neither contains secrets. Rewind has no secret settings: the owner account
is created in the browser, and its password is stored only as a hash in the
database.

## Storage

| Variable | Default | Values |
| --- | --- | --- |
| `REWIND_DATA_DIR` | `data`, relative to the working directory (`/var/lib/rewind` for the service) | Directory for `rewind.db` and, until the owner account exists, `setup-code`. Created with mode 0700 if missing. An existing directory's permissions are never changed. Recommended for a server: `/var/lib/rewind`. |
| `REWIND_LIBRARY_ROOTS` | *(none)* | Absolute music folders, separated by `:` (`;` on Windows). A relative path stops startup. See [library.md](library.md). |

`rewind.service` lets Rewind write only to `/var/lib/rewind`. A data
directory elsewhere also needs a matching `ReadWritePaths=` drop-in
(`sudo systemctl edit rewind`).

## Network

Rewind is reachable **from the server only** unless you change these
settings. Read [deployment.md, "Network access"](deployment.md#network-access)
first.

| Variable | Default | Values |
| --- | --- | --- |
| `REWIND_ADDR` | `127.0.0.1:8090` | Comma-separated `host:port` list, at most 8 entries. Hosts must be IP addresses or `localhost`. Non-loopback addresses need `REWIND_REMOTE_ACCESS=true`. |
| `REWIND_REMOTE_ACCESS` | `false` | `true` allows LAN or Tailscale IP addresses in `REWIND_ADDR`. |
| `REWIND_LISTEN_ALL_INTERFACES` | `false` | `true` also allows `0.0.0.0`, `[::]` or `:port` (all interfaces, including public ones). Needs `REWIND_REMOTE_ACCESS=true`. |
| `REWIND_TRUSTED_HOSTS` | *(none)* | Comma-separated host names or IPs accepted in the browser's address bar, besides loopback names and the IPs Rewind listens on. Exact names only: no `*`, ports, schemes or paths. |
| `REWIND_COOKIE_SECURE` | `false` | `true` when remote browsers use HTTPS (for example `tailscale serve`). With `true` over plain HTTP, browsers drop the cookie and you cannot stay signed in. |

## Sessions

| Variable | Default | Values |
| --- | --- | --- |
| `REWIND_SESSION_LIFETIME` | `720h` (30 days) | Go duration from `5m` to `8760h`. A session ends after this time, however much it is used. |
| `REWIND_SESSION_IDLE_TIMEOUT` | `168h` (7 days) | Go duration of at least `5m`. An unused session ends after this time. Capped at the lifetime. |

## Library scanning and API

| Variable | Default | Values |
| --- | --- | --- |
| `REWIND_SCAN_ON_STARTUP` | `true` | `false` skips the background scan at startup. Rescans can still be started from the Status page. |
| `REWIND_SCAN_WORKERS` | `2` | Files read in parallel during a scan, `1`–`8`. More rarely helps on USB drives. |
| `REWIND_API_MAX_PAGE_SIZE` | `200` | Largest `limit` accepted by paginated API endpoints, `10`–`1000`. |

## Logging

| Variable | Default | Values |
| --- | --- | --- |
| `REWIND_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `REWIND_LOG_FORMAT` | `text` | `text`, `json` |

Logs go to standard error, and so to the journal for the service. They
never contain passwords, session tokens or the setup code. They do contain
the names of music folders, and of files that could not be read.

## Plugins

| Variable | Default | Values |
| --- | --- | --- |
| `REWIND_PLUGIN_DIR` | *(none: plugins off)* | Absolute directory of plugin manifests, outside the data directory and the music folders, writable only by the administrator, for example `/etc/rewind/plugins`. |
| `REWIND_PLUGINS` | *(none)* | Comma-separated IDs of plugins to enable. Requires `REWIND_PLUGIN_DIR`. |

Plugins are compiled into Rewind and trusted. See [plugins.md](plugins.md).

## Themes

| Variable | Default | Values |
| --- | --- | --- |
| `REWIND_THEME_DIR` | *(none: bundled themes only)* | Absolute directory of theme packages (`<id>/theme.json`), outside the data directory, the music folders and the plugin directory, writable only by the administrator, for example `/etc/rewind/themes`. |

Themes are data only (colors and style values, never code) and are read at
startup. A missing directory or an invalid theme is logged and never stops
Rewind. See [themes.md](themes.md#installing-a-theme-administrator).

## Boolean and duration syntax

- Booleans accept `true`/`false`, `1`/`0`, `t`/`f`, and `TRUE`/`FALSE`.
- Durations use Go syntax: `90m`, `36h`, `720h`. Days (`30d`) are not
  accepted.
- Values are trimmed of surrounding spaces. In `rewind.env`, do not quote
  values unless they contain spaces. systemd removes the quotes.

## Development-only variable

`REWIND_DEV_API_TARGET` is read by the Vite development server, not by
Rewind. It tells Vite where to forward `/api` requests, and defaults to
`http://127.0.0.1:8090`. See [development.md](development.md#development-mode).
