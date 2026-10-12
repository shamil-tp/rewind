# Security policy

## Reporting a vulnerability

Please report security problems **privately**, not in a public issue:

- Use GitHub's **"Report a vulnerability"** button on the repository's
  **Security** tab (private vulnerability reporting).
- Include the Rewind version (`curl -fsS http://127.0.0.1:8090/api/health`),
  how Rewind is reached (loopback, `tailscale serve`, LAN or Tailscale IP),
  and the steps to reproduce.
- Do not include your password, session cookies, database files, setup
  codes or private music.

Rewind is maintained by volunteers, so there is no guaranteed response time.
You will get an acknowledgement when the report has been read. Fixes are
released as a new version, and the release notes credit the reporter unless
you ask not to be named.

## Supported versions

Only the latest release receives security fixes. Upgrade with
`sudo sh install.sh upgrade` ([docs/operations.md](docs/operations.md#upgrading)).

## Security model in brief

- **Local only by default.** Rewind listens on `127.0.0.1:8090`. LAN and
  Tailscale access have to be enabled explicitly, and wildcard addresses
  need a second, separate setting. Rewind never opens firewall ports, sets
  up port forwarding or exposes itself to the internet.
- **Every device signs in.** Being on the same Wi-Fi or tailnet grants
  nothing. All library, streaming and settings requests need a session.
- **One owner account**, created with a one-time setup code that only
  someone with access to the server's files can read. Setup only works from
  the server itself, through a loopback address.
- Passwords are stored as Argon2id hashes, and session tokens as SHA-256
  hashes. Cookies are `HttpOnly` and `SameSite=Strict`, and also `Secure`
  over HTTPS. State-changing requests have cross-origin protection, and
  sign-in is rate-limited.
- **Music is read-only.** Files are opened only through the database record
  of an indexed track, and only inside its configured folder: no path from a
  request, no symbolic links, no files outside the folder. The systemd unit
  also makes the whole file system read-only for Rewind, except its data
  directory.
- **Plugins are trusted code** compiled into Rewind. They are off unless
  the administrator enables them, and they are not sandboxed
  ([docs/plugins.md](docs/plugins.md)).
- Plain HTTP over a LAN sends your password unencrypted. Prefer HTTPS
  through `tailscale serve`.

The full model is in [docs/architecture.md, §8](docs/architecture.md#8-security-model).
The latest audit is in [docs/security-audit.md](docs/security-audit.md).
Neither is a guarantee: Rewind has not had an independent security review.
