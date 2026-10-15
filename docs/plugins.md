# Plugins

Rewind has a small plugin system for **trusted extensions chosen by the
server's administrator**. It is optional: with no plugin settings, Rewind
works exactly as without it.

Visual themes are **not** plugins. A theme is a data-only JSON file (colors
and style values), installed in its own directory (`REWIND_THEME_DIR`),
validated as untrusted input and never executed. It gets no plugin
capabilities, and plugins cannot add themes. See [themes.md](themes.md).

## How plugins are loaded: compile time only

A Rewind plugin is a **Go package compiled into the Rewind binary**. Rewind
does not load code at run time:

- There is no dynamic loading. Go's `plugin` package is not used (it does not
  work on Windows and requires identical build settings), and nothing is
  downloaded, interpreted or executed from the plugin directory.
- Putting a manifest, a Go file or any program into the plugin directory runs
  nothing. A manifest only **describes and authorizes** a plugin that is
  already part of the binary.
- To add a plugin's code, you add its package to `cmd/rewind/plugins.go` and
  rebuild Rewind.

A plugin runs only if all three are true:

1. its code is compiled into this binary (registered with `plugin.Register`);
2. its manifest is installed as `<REWIND_PLUGIN_DIR>/<id>/plugin.json`, with
   the same ID and version as the compiled code;
3. its ID is listed in `REWIND_PLUGINS`.

Official release builds include one example plugin, `missing-tracks`. It does
nothing until you install its manifest and enable it.

## Security model

**A plugin is trusted code.** It runs inside the Rewind process with the
same operating-system user and privileges as Rewind. It could read the
database file, the music files and the network, whatever the interfaces
below offer. Manifest validation checks format, not intent: **a valid
manifest does not make a plugin safe.** Only enable plugins whose source you
have read or whose authors you trust, as you would any program you run.

What the design does guarantee:

- **Nothing runs by accident.** Plugins are off unless `REWIND_PLUGIN_DIR` is
  set, and each one must be listed in `REWIND_PLUGINS` by the administrator.
  An installed but unlisted plugin is never initialized.
- **No remote control.** There are no endpoints to install, upload, enable,
  disable, configure or run plugins. Changing plugins means editing the
  server's configuration and restarting it.
- **Narrow interfaces.** A plugin receives a logger and the extension points
  its manifest declares, nothing else: no database handle, HTTP routes,
  configuration, file system API or command execution. It cannot add or
  change routes, so authentication and authorization stay with Rewind's core.
  It cannot change the listen address or security settings.
- **Contained failures.** A plugin that fails, panics or hangs in `Init` is
  marked failed and stays off; Rewind starts anyway. Event handlers run on the
  plugin's own goroutine with a deadline and panic recovery, so a slow or
  crashing plugin cannot block scans, requests or other plugins.
- **Safe status.** The status API and the Settings page show manifest text as
  plain text, never as HTML, and show Rewind's own short error summaries. A
  plugin's error messages, paths and stack traces go only to the server log.
- **Contained files.** Manifests are read through Go's `os.Root`, so no path
  can leave the plugin directory. Symbolic links and Windows junctions are
  rejected, manifests are limited to 16 KiB, and at most 64 entries are
  examined. The plugin directory may not be, contain or be inside the data
  directory or a music folder.

What it does **not** do: it is not a sandbox. Go cannot stop compiled-in code
from using the standard library directly, and a goroutine that ignores its
context cannot be stopped. Isolated plugins (in a separate process or a
WebAssembly runtime) are a possible future addition; they do not exist today.

## Manifest

`plugin.json`, JSON (UTF-8, at most 16 KiB, one object, no unknown fields):

```json
{
  "schema": 1,
  "id": "missing-tracks",
  "name": "Missing tracks warning",
  "version": "1.0.0",
  "description": "Logs a warning when a library scan marks tracks as missing.",
  "rewind_api": 1,
  "capabilities": ["events.library_scan"],
  "author": "Rewind contributors",
  "homepage": "https://example.org/missing-tracks"
}
```

| Field | Required | Rules |
| --- | --- | --- |
| `schema` | yes | Manifest format version. Must be `1`. |
| `id` | yes | Lower-case letters, digits and single hyphens, starting with a letter, at most 40 characters. Must equal the directory name and the compiled plugin's ID. |
| `name` | yes | 1–64 characters, no control characters, no leading or trailing spaces. Plain text. |
| `version` | yes | `MAJOR.MINOR.PATCH`. Must equal the compiled plugin's version. |
| `description` | no | At most 280 characters. Plain text. |
| `rewind_api` | yes | The plugin API version the plugin was written for. Must equal the API this Rewind provides (currently `1`); otherwise the plugin is shown as incompatible and not started. |
| `capabilities` | yes | The extension points the plugin may use (`[]` for none). Unknown or repeated names are rejected. The plugin cannot register anything not listed. |
| `author` | no | At most 100 characters. Plain text. |
| `homepage` | no | An `https://` URL, at most 200 characters, without credentials. |

No field is ever executed, used as a path or rendered as HTML. Fields such as
`command`, `exec` or `script` do not exist and make the manifest invalid.

**Compatibility** is checked against the plugin API version, not Rewind's
release version, because the interfaces are what a plugin depends on. The API
version increases only when the interfaces in `internal/plugin/api.go` change
incompatibly; the release notes will say so.

## Extension points (plugin API 1)

| Capability | Interface | Called |
| --- | --- | --- |
| `events.library_scan` | `host.OnLibraryScan(func(ctx, plugin.ScanEvent) error)` | After every library scan (completed, partial, failed or interrupted). |

`ScanEvent` carries the root ID, scan ID, status, start and end times and the
scan's counts. It contains no paths, file names or error text.

There is deliberately only one extension point. Others (metadata providers,
library views, playback features) need interfaces designed with a real
consumer; they are not provided "just in case".

## Writing a plugin

See `plugins/missingtracks/` for a complete example:

```go
package myplugin

import (
	"context"

	"github.com/shamil-tp/rewind/internal/plugin"
)

func init() { plugin.Register(&Plugin{}) }

type Plugin struct{}

func (*Plugin) ID() string      { return "my-plugin" }   // = manifest id
func (*Plugin) Version() string { return "1.0.0" }       // = manifest version

func (p *Plugin) Init(ctx context.Context, host *plugin.Host) error {
	return host.OnLibraryScan(func(ctx context.Context, e plugin.ScanEvent) error {
		host.Logger().Info("scan finished", "scan_id", e.ScanID)
		return nil
	})
}

func (*Plugin) Close(ctx context.Context) error { return nil }
```

Then add `_ "github.com/shamil-tp/rewind/plugins/myplugin"` to
`cmd/rewind/plugins.go`, put a `plugin.json` next to the code, and rebuild.
Because plugins live in Rewind's module, they can use `internal/plugin`.
`plugin.Register` panics on a duplicate or invalid ID, so a build mistake
stops Rewind at startup instead of running the wrong code.

### Lifecycle and concurrency

1. **Startup** (after the database and library are ready, before serving):
   Rewind reads every manifest, decides each plugin's state, and calls
   `Init` on the enabled ones **one at a time, sorted by ID**, each with a
   10-second deadline. Registration is only possible during `Init`: whatever
   happens (error, panic, missed deadline), the host is sealed when `Init`
   ends, and registrations of a failed plugin are discarded. `Close` is not
   called for a plugin whose `Init` failed.
2. **Running:** each plugin with a scan handler gets its own goroutine and a
   queue of 16 events. Events are handled one at a time, in order, each with
   a 10-second deadline. If the queue is full, new events for that plugin are
   dropped and logged; publishing never waits. Handlers must return when
   their `ctx` is done and must not modify library files.
3. **Shutdown:** after the scanner has stopped, Rewind stops event delivery,
   cancels running handlers' contexts, waits for them, then calls `Close` on
   started plugins **in reverse order**, each with a 5-second deadline (10
   seconds in total). A plugin that ignores its deadline is logged and
   skipped so shutdown completes.

There is no hot reload: plugins are read once at startup.

Plugins must not start goroutines that outlive `Close`, and must honour every
`ctx` they receive. Go cannot stop code that ignores this, which is part of
why plugins must be trusted.

## Installing and enabling a plugin

1. Make sure the plugin is compiled into your Rewind binary (official builds
   include `missing-tracks`). Check the startup log or **Settings → Plugins**:
   a plugin that is installed and enabled but not compiled in is shown as
   "Not in this build".
2. Choose a plugin directory **outside** the data directory and the music
   folders, for example `/etc/rewind/plugins` or `/var/lib/rewind-plugins`.
   It should be writable only by the administrator (root), readable by the
   Rewind user.
3. Install the manifest:

   ```sh
   sudo install -d -m 0755 /etc/rewind/plugins/missing-tracks
   sudo install -m 0644 plugin.json /etc/rewind/plugins/missing-tracks/plugin.json
   ```

   The example's manifest is `plugins/missingtracks/plugin.json` in the
   source tree; each release's source archive contains it. (The binary
   archive does not include plugin manifests.)
4. Configure and restart:

   ```sh
   REWIND_PLUGIN_DIR=/etc/rewind/plugins
   REWIND_PLUGINS=missing-tracks
   ```

### Checking a plugin before enabling it

Set only `REWIND_PLUGIN_DIR` (not `REWIND_PLUGINS`) and restart. Rewind
validates every manifest and reports each plugin's state without running any
plugin code: "Installed, not enabled" means the manifest is valid and
compatible; "Invalid" and "Incompatible" explain what is wrong. Then add the
ID to `REWIND_PLUGINS` and restart again.

### Status and logs

- **Settings → Plugins** (and `GET /api/plugins`, see
  [api.md](api.md#get-apiplugins)) lists each plugin's name, version and
  state, with a short reason when it is not running.
- The server log has the details, with a `plugin=<id>` attribute on every
  record a plugin writes: `plugins ready`, `plugin started`,
  `plugin initialization failed` (with the plugin's error),
  `invalid plugin entry`, `scan event dropped`, `plugins stopped`.

| State | Meaning | What to do |
| --- | --- | --- |
| Running | Enabled and started. | — |
| Installed, not enabled | Valid manifest, not in `REWIND_PLUGINS`. Never initialized. | Add the ID to enable it. |
| Failed to start | `Init` failed, panicked or timed out, or the manifest's version differs from the compiled code. | Read the log; fix or disable it. |
| Invalid | The manifest or directory entry was rejected. | Fix the manifest; the reason is shown. |
| Incompatible | Written for another plugin API version. | Install a matching version of the plugin. |
| Not in this build | Enabled, but this binary does not contain its code. | Use a build that includes it, or disable it. |
| Enabled, not installed | Listed in `REWIND_PLUGINS`, but no manifest is installed. | Install the manifest or remove the ID. |

### Disabling a failing plugin

Remove its ID from `REWIND_PLUGINS` and restart. A failed plugin is already
off (it never receives events), but disabling it stops the startup warning.
To remove it completely, also delete its directory from the plugin directory.

## Backups, upgrades and deployment

- **Back up** the plugin directory (manifests only) and your
  `REWIND_PLUGIN_DIR`/`REWIND_PLUGINS` settings together with the rest of
  your configuration. Plugins store nothing in Rewind's database.
- **Upgrading Rewind** replaces the compiled-in plugins with the versions in
  the new binary. If a plugin's compiled version changes, update its manifest
  to match (the release notes say so); until then the plugin is shown as
  "Failed to start" and stays off, and Rewind itself runs normally. If the
  plugin API version changes, plugins written for the old API are shown as
  "Incompatible".
- **Rolling back** works the same way: keep the manifest that matches the
  binary you run.
- Release builds and deployment scripts are unchanged; plugins never cause
  downloads at startup.

## Testing plugins

```sh
go test ./internal/plugin ./plugins/...   # manifest, discovery, lifecycle, the example
go test ./internal/server -run Plugin      # status API: auth, safe output, read-only
go test ./cmd/rewind -run Plugin           # end to end: a scan event reaches the example plugin
```

Tests use fake plugins and temporary directories; they never run external
code. To test your own plugin, register it in a `plugin.NewRegistry()` and
drive a `plugin.Manager` as `plugins/missingtracks/missingtracks_test.go`
does.

## Limitations

- Compile-time registration only: adding a plugin means rebuilding Rewind.
- One extension point (`events.library_scan`).
- No isolation between plugins and Rewind; see the security model.
- No per-plugin settings, no hot reload, no web UI extensions. Plugins cannot
  add pages, scripts or styles to the web interface.
