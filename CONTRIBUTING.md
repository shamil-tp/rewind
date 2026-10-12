# Contributing to Rewind

Thank you for helping. Rewind is a small, self-hosted music server, and it
aims to stay small: one binary, one SQLite file, no telemetry, and no
required cloud service. Please read [CLAUDE.md](CLAUDE.md) §2 (product
principles) and §9 (out of scope) before proposing a feature.

## Reporting bugs and asking for features

- **Security problems:** do not open an issue; see [SECURITY.md](SECURITY.md).
- **Bugs:** open an issue with the bug template. Include the version
  (`/api/health`), the OS, the browser and the relevant log lines
  (`journalctl -u rewind`). Remove music file names you do not want to
  share. Never attach `rewind.db`, `rewind.env` files or setup codes.
- **Features:** open an issue describing the problem first. Features outside
  the roadmap in [docs/architecture.md](docs/architecture.md) need
  discussion before a pull request.

## Development setup

See [docs/development.md](docs/development.md). In short: Go (version in
`go.mod`), and Node.js 24 with npm for the frontend.

```sh
go run ./cmd/rewind                  # backend on http://127.0.0.1:8090
cd web && npm ci && npm run dev      # frontend on http://localhost:5173
```

Use generated or freely licensed test audio only. The Go tests generate
their own (`internal/testaudio`). Never commit music, databases, `.env` files
or keys.

## Before you open a pull request

Run the checks that CI runs:

```sh
gofmt -l .            # must print nothing
go vet ./...
go test ./...
cd web && npm run check && npm test && npm run build
```

On Linux, also run `go test -race ./...` and, for packaging changes,
`sh scripts/release-check.sh` ([docs/deployment.md](docs/deployment.md#release-validation)).

Pull requests should:

- do one thing, with tests for new behavior and a regression test for a bug
  fix;
- update the documentation when behavior or configuration changes, and add
  a line under `Unreleased` in [CHANGELOG.md](CHANGELOG.md);
- explain every new dependency (what it is for, its license, why the
  standard library is not enough) in
  [docs/architecture.md §4](docs/architecture.md#4-dependencies-and-license-review).
  A new Go module must also be listed in `release/third-party-modules.txt`,
  or the release license check fails;
- never edit a database migration that has been released; add a new one;
- keep loopback-only the default, and never weaken authentication, host
  checks or file containment without a discussion first.

## Style

- Go: `gofmt`, the standard library first, and short comments that explain
  why.
- Frontend: Svelte 5 and TypeScript. `npm run check` treats warnings as
  errors.
- Shell scripts: POSIX `sh`, `set -eu`.
- New source files start with `// SPDX-License-Identifier: Apache-2.0`, or
  `# SPDX-License-Identifier: Apache-2.0` in scripts.

## License

Rewind is licensed under the [Apache License 2.0](LICENSE). By contributing,
you agree that your contribution is licensed under the same license
(Apache-2.0 §5). No contributor license agreement is required.
