# Rewind — Project Instructions for Claude Code

## 1. Project Overview

**Project name:** Rewind  
**Project type:** Open-source, self-hosted personal music streaming server  
**Primary goal:** Build a lightweight music player that lets users stream their own locally stored music to their personal devices.

Rewind is designed for people who own their music and want to access it anywhere without relying on commercial streaming services.

A user connects a USB pendrive containing music files to their personal server. Rewind scans the files, extracts metadata, builds a music library, and makes the music available through a web-based player.

Users can access Rewind:
- On the same home Wi-Fi network.
- Remotely through a private Tailscale network.
- On supported desktop and mobile browsers.
- Through an installable Progressive Web App (PWA).

Rewind must remain lightweight, privacy-focused, extensible, and easy to self-host.

## 2. Product Principles

All implementation decisions must follow these principles.

### 2.1 Lightweight

- Minimize CPU, memory, disk I/O, and network consumption.
- Avoid unnecessary services and background workers.
- Do not introduce Redis, Elasticsearch, message brokers, Kubernetes, or other infrastructure without a demonstrated requirement.
- Prefer simple, maintainable implementations over complex abstractions.
- Avoid unnecessary polling and repeated filesystem scans.
- Use pagination and lazy loading for large libraries.
- Stream audio rather than loading complete audio files into server memory.
- Keep the application usable on modest personal-server hardware.

### 2.2 Self-hosted and private

- Music files remain on the user's own storage.
- No mandatory cloud accounts or third-party music services.
- No telemetry, analytics, tracking, or external account dependencies by default.
- No public internet exposure required for remote access.
- Tailscale is the recommended remote-access method, but Rewind must not require it for local playback.
- Never assume that being on a private network automatically makes every request trustworthy.

### 2.3 Open source

- Use an OSI-approved open-source license selected by the project owner.
- Document installation, configuration, upgrades, backups, and troubleshooting.
- Provide an example environment configuration without secrets.
- Avoid proprietary dependencies and paid APIs in the core application.
- Respect licenses for dependencies, artwork, fonts, and other assets.
- Keep the installation process accessible to users who are not DevOps experts.

### 2.4 Minimal user interface

- Use a dark neo-brutalist design language.
- Prefer strong typography, hard edges, deliberate borders, and restrained accent colors.
- Keep the interface content-focused and free from unnecessary cards, widgets, gradients, and decorative elements.
- Prioritize album artwork, track information, playback controls, and library navigation.
- Design mobile-first, with touch-friendly controls.
- Support keyboard navigation and accessible contrast.

### 2.5 Extensible without overengineering

- Custom themes must not require modifying core source code.
- Plugins must use documented extension points.
- The core player must work without any third-party plugins.
- Theme and plugin failures must not break the music library or playback.
- Avoid building a marketplace, plugin cloud, or complex plugin dependency resolver in the initial release.

---

## 3. Required Workflow for Claude Code

Before implementing anything:

1. Read this entire `CLAUDE.md`.
2. Inspect the existing repository structure and relevant files.
3. Identify the existing framework, dependencies, conventions, and working features.
4. Check the Git status before modifying files.
5. Identify which phase is currently active.
6. Prepare a short implementation plan for the active phase.
7. Explain any significant architectural decisions before making irreversible changes.

### Mandatory rules

- Implement only the phase explicitly authorized by the project owner.
- Do not automatically continue to the next phase when the current phase is complete.
- Do not rewrite working components without a clear reason.
- Do not replace the existing stack merely because another stack is preferred.
- Do not add dependencies without explaining why they are necessary.
- Do not fabricate successful tests, API responses, or completed features.
- Do not leave placeholder implementations where functional behavior is required.
- Do not silently change the database schema or configuration format.
- Never overwrite user changes or discard uncommitted work.
- Keep secrets and local environment files out of Git.
- Use npm when the existing frontend project uses npm; do not introduce Yarn unnecessarily.
- Prefer small, reviewable changes.
- Update documentation when behavior or configuration changes.

At the end of each phase, report:

1. What was implemented.
2. Files created or modified.
3. Dependencies added or removed.
4. Tests and validation performed, including failures.
5. Security considerations.
6. Known limitations.
7. Manual verification steps.
8. Whether the phase acceptance criteria have been met.

**Stop after reporting phase completion and wait for explicit approval before proceeding.**

---

## 4. Recommended Architecture

Use a simple client-server architecture. Final technology choices must be made after inspecting the repository and confirming that the selected stack suits the hardware and requirements.

### Server responsibilities

- Discover configured music storage locations.
- Scan music files and extract metadata.
- Maintain a searchable music-library index.
- Expose a documented API.
- Stream audio efficiently.
- Handle playback-related library operations.
- Manage playlists and user preferences.
- Serve the PWA or support a separately hosted frontend.
- Provide safe extension points for themes and plugins.

### Client responsibilities

- Render the music library.
- Search and browse artists, albums, and tracks.
- Control audio playback.
- Manage the playback queue and playlists.
- Store appropriate client-side preferences.
- Support installation as a PWA.
- Load compatible themes and approved plugins.

### Storage responsibilities

- Music files remain in their original locations.
- The application database stores metadata, library indexes, playlists, preferences, and application state.
- Album artwork may be cached locally when needed, with clear cache-management rules.
- USB disconnection must not cause automatic deletion of the corresponding library records.
- Rescanning must be safe to repeat and must not create duplicate records.

### Architecture rules

- Keep the deployment to the minimum number of services practical.
- Prefer SQLite for the initial single-server deployment unless a demonstrated requirement justifies another database.
- Avoid storing audio blobs in the database.
- Separate filesystem scanning from HTTP request handling.
- Make scanning incremental where practical.
- Keep the core API independent of any particular theme.
- Keep Tailscale integration optional; remote networking should normally be handled by the deployment environment.
- Do not require a privileged container or root process for ordinary application operation.

---

## 5. Phased Development Roadmap

# Phase 0 — Repository Discovery and Technical Design

### Goal

Establish the project foundation without prematurely implementing every feature.

### Tasks

- Inspect the repository and identify existing code.
- Confirm the frontend and backend stack.
- Decide how the application will be packaged and installed.
- Define the initial API boundaries.
- Define the music-library data model.
- Determine how USB-mounted storage paths will be configured.
- Document how the server will be accessed locally and through Tailscale.
- Establish linting, formatting, testing, and environment-configuration conventions.
- Select an appropriate open-source license.

### Deliverables

- Architecture documentation.
- Initial directory structure or a documented plan for the existing structure.
- `.env.example` where environment configuration is needed.
- Basic development instructions.
- A list of dependencies and the reason for each major dependency.

### Acceptance criteria

- The project can be started in development mode using documented instructions.
- Architectural decisions and trade-offs are documented.
- No unnecessary infrastructure is introduced.
- No secrets are committed.

Do not implement the complete application in this phase.

# Phase 1 — Core Server Foundation

### Goal

Build a stable application server with configuration and basic health checks.

### Tasks

- Implement application startup and shutdown.
- Add configuration validation.
- Add a health endpoint.
- Configure logging with reasonable defaults.
- Implement the initial database connection and migrations.
- Add a basic API structure.
- Handle expected errors consistently.
- Support configurable library paths and data directories.
- Make startup failures understandable to the user.

### Acceptance criteria

- The server starts and stops cleanly.
- Health checks work.
- Invalid configuration produces useful errors.
- Database initialization is reproducible.
- Tests cover the basic application lifecycle.
- The server does not require access to the public internet.

# Phase 2 — USB Storage and Music Discovery

### Goal

Read music from a pendrive or other configured local music directory.

### Tasks

- Allow users to configure one or more music-library paths.
- Discover supported audio files recursively.
- Support common formats such as MP3, FLAC, M4A/AAC, Ogg Vorbis, and WAV where compatible libraries and the chosen playback architecture permit.
- Treat format support as an explicit capability rather than assuming every browser can play every codec.
- Extract metadata such as:
  - Title
  - Artist
  - Album
  - Album artist
  - Track number
  - Disc number
  - Duration
  - Genre
  - Year or release date
  - Embedded artwork when available
- Use tags as the primary metadata source.
- Use filenames as a fallback when tags are missing.
- Record file size, modification time, and a stable library identifier.
- Detect new, modified, missing, and inaccessible files.
- Avoid duplicate entries during repeated scans.
- Support manual rescanning.
- Provide scan status and useful error reporting.
- Handle filesystem permission errors and unsupported files gracefully.

### USB behavior

- Never format, modify, or delete files from the pendrive.
- Treat the source music directory as read-only.
- Do not assume a particular USB mount path.
- Do not automatically mount or unmount devices in the initial implementation.
- Detect missing storage when a scan or playback operation encounters it.
- Preserve library records when a device is temporarily disconnected.
- Make it possible to rescan the same directory safely.

### Acceptance criteria

- The application can index a real directory containing music.
- Metadata is displayed correctly for representative files.
- Repeated scans do not produce duplicate tracks.
- Missing or corrupt files do not crash the server.
- Disconnecting the pendrive does not erase the library.
- Source files remain unchanged.

# Phase 3 — Music Library API

### Goal

Make indexed music available through a clean, documented API.

### Tasks

- Implement endpoints for tracks, artists, albums, and genres.
- Support pagination for large libraries.
- Add search by title, artist, album, and other useful metadata.
- Add filtering and sorting where appropriate.
- Provide album and artist detail endpoints.
- Expose library scan status.
- Expose supported format information where useful.
- Implement local artwork retrieval or caching.
- Validate request parameters.
- Use stable identifiers instead of relying on array positions or filenames as database IDs.

### Acceptance criteria

- API responses are consistent and documented.
- Search works against the indexed library.
- Pagination does not load the entire library into memory.
- Missing records and invalid requests return appropriate errors.
- Large libraries can be browsed without unnecessary repeated database queries.

# Phase 4 — Audio Streaming and Playback Engine

### Goal

Enable reliable streaming from the server to supported clients.

### Tasks

- Implement a dedicated audio-streaming endpoint.
- Stream from disk in bounded chunks rather than buffering entire tracks in memory.
- Support HTTP Range requests and partial content responses where required for seeking.
- Implement correct content types and response headers.
- Handle client disconnects without leaving unnecessary resources open.
- Handle storage disconnection during playback.
- Implement playback, pause, seek, previous, next, and volume controls in the client.
- Implement queue management and repeat/shuffle behavior.
- Ensure audio resources are cleaned up appropriately.
- Avoid unnecessary server-side transcoding.

### Format policy

- Prefer direct streaming when the client's browser supports the source codec.
- Investigate optional transcoding only when there is a demonstrated compatibility requirement.
- Do not introduce a heavy transcoding service by default.
- If transcoding is added later, make it configurable and document its CPU and storage implications.

### Acceptance criteria

- Supported audio files play in a browser.
- Seeking works for compatible streaming responses.
- Streaming does not require loading entire tracks into server memory.
- Playback failures are reported clearly.
- Multiple clients can stream within the limits of available hardware and storage.
- The player remains usable while library operations occur.

# Phase 5 — Core User Interface

### Goal

Build the initial Rewind experience with a minimal dark neo-brutalist design.

### Design direction

- Dark background as the default.
- High-contrast text.
- One primary accent color, initially acid lime or another configurable accent.
- Strong, deliberate borders and mostly square corners.
- Restrained typography and spacing.
- Album artwork as the primary visual element.
- Minimal animation.
- No decorative gradients, excessive glass effects, or unnecessary dashboard cards.
- Responsive layouts that prioritize mobile usability.

### Main views

- Library
- Artists
- Albums
- Search
- Now Playing
- Queue
- Playlists
- Settings

Avoid creating every view as a separate complex page if a simpler navigation model works better.

### Playback interface

Prioritize:

- Artwork
- Track title and artist
- Playback progress
- Previous, play/pause, and next
- Queue access
- Volume where appropriate
- Clear playback status

Keep secondary actions accessible without cluttering the primary playback view.

### Acceptance criteria

- All essential playback controls work.
- The layout adapts to desktop and mobile screens.
- The UI remains usable with keyboard navigation.
- Long titles and missing artwork are handled correctly.
- Loading, empty, and error states are designed.
- The interface does not rely on unnecessary third-party UI services.

# Phase 6 — Progressive Web App and Device Access

### Goal

Make Rewind convenient to install and use on supported devices.

### Tasks

- Add a valid web app manifest.
- Configure suitable application icons.
- Implement service-worker behavior.
- Support installation on compatible browsers.
- Make navigation and static application assets available offline where appropriate.
- Provide a responsive mobile playback experience.
- Support appropriate mobile audio controls and background playback where the platform permits.
- Handle network interruptions and reconnection.
- Document local-network access.
- Document private remote access through Tailscale.

### Important PWA rules

- Do not cache entire music files by default.
- Do not cache authenticated or private API responses indiscriminately.
- Do not claim that streaming works offline unless an explicit offline-download feature is implemented.
- Keep the server authoritative for the music library.
- Avoid unnecessary polling to detect network changes.
- Use HTTPS for contexts requiring secure browser features.

### Network access

- The server should bind to an address appropriate for the chosen deployment.
- Document how to access Rewind from another device on the home network.
- Explain how to access Rewind through the user's Tailscale address or private DNS name.
- Do not expose the server publicly by default.
- Do not require Tailscale on the server's public interface or implement custom VPN functionality.
- Do not hardcode IP addresses, ports, or hostnames.

### Acceptance criteria

- The PWA installs on supported browsers.
- Static UI assets are cached appropriately.
- Remote access works when the user has configured Tailscale and network access correctly.
- Local access continues to work without Tailscale.
- Network failures do not corrupt playback state or the music library.

# Phase 7 — Playlists, Preferences, and Library Quality

### Goal

Make Rewind practical for daily personal use.

### Tasks

- Create, rename, reorder, and delete playlists.
- Add and remove tracks from playlists.
- Persist user preferences.
- Persist appropriate playback settings.
- Support queue manipulation.
- Add favorite tracks or equivalent lightweight library organization if useful.
- Improve search and sorting.
- Provide library refresh controls.
- Add configurable artwork caching and cleanup.
- Support empty libraries and disconnected storage.
- Document backup and restoration of application data.

### Acceptance criteria

- Playlists persist across restarts.
- Invalid or deleted tracks are handled gracefully.
- Library updates do not destroy playlists unnecessarily.
- Preferences persist as intended.
- Backups do not require copying music files when those files are already stored separately.

# Phase 8 — Custom Theme System

### Goal

Allow users to customize Rewind's appearance without modifying the core source code.

### Initial scope

Implement a lightweight theme system based on CSS custom properties and a documented theme format.

Themes may customize:

- Background and surface colors.
- Primary text and muted text.
- Accent color.
- Borders.
- Focus indicators.
- Typography settings within safe, documented limits.
- Selected spacing and corner-style variables where appropriate.

### Theme requirements

- Provide a default theme.
- Allow users to select and persist a theme.
- Support loading custom themes from a documented location or import mechanism.
- Validate theme configuration.
- Fall back to the default theme when a custom theme is invalid.
- Keep theme loading separate from music playback logic.
- Respect accessibility and reduced-motion preferences.
- Do not allow a theme to modify backend configuration or access music files.

### Safety

- Prefer CSS variables and declarative theme configuration.
- Do not execute arbitrary JavaScript supplied by a theme.
- Do not permit a theme to inject arbitrary HTML.
- Do not fetch remote theme assets automatically without an explicit user action.

### Acceptance criteria

- Users can create and install custom themes without changing core application code.
- Invalid themes cannot break the main player.
- Theme selection persists.
- The default theme works without additional downloads.

# Phase 9 — Plugin Architecture

### Goal

Introduce an optional, documented plugin system after the core application is stable.

### Design principles

- Plugins are optional.
- Core playback must not depend on plugins.
- Plugin APIs must be versioned.
- Plugins must have explicit extension points.
- Plugin failures must not crash the core server or corrupt the library.
- Plugin loading and installation must be documented.
- Plugins must be disableable and removable.
- Avoid creating a plugin marketplace in the initial release.

### Potential extension points

Subject to design review, plugins may extend:

- Additional metadata providers.
- Library views or navigation entries.
- Playback visualizations.
- Custom API integrations.
- Import and export workflows.
- Library scanning rules.
- Optional playback-related capabilities.

Do not implement every extension point immediately. Start with the smallest useful interface that can be tested.

### Security requirements

- Do not execute arbitrary plugin code without a clearly documented trust model.
- A server-side plugin must be treated as trusted code with potential access to the server's privileges.
- Do not describe an in-process plugin system as sandboxed unless real isolation exists.
- Keep plugin configuration separate from core configuration.
- Validate plugin manifests and compatibility.
- Require explicit administrator action to install or enable server-side plugins.
- Do not permit plugins to silently download or execute additional code.
- Document plugin permissions and risks.

### Acceptance criteria

- A simple example plugin demonstrates the documented extension mechanism.
- Disabling a plugin restores the core behavior.
- Invalid plugins fail safely.
- Plugin compatibility is checked.
- Plugin documentation explains installation, removal, permissions, and maintenance.

# Phase 10 — Security, Reliability, and Resource Optimization

### Goal

Prepare Rewind for dependable long-term use on a personal server.

### Tasks

- Review every API endpoint for inappropriate access.
- Validate filesystem paths and prevent path traversal.
- Prevent arbitrary file reads outside configured music directories.
- Keep secrets out of logs and responses.
- Implement appropriate CORS and host validation.
- Protect administrative actions from unauthorized access.
- Document the trust model for local-network and Tailscale access.
- Add rate limits or request limits where justified.
- Handle oversized requests and malformed metadata.
- Review database migrations and backup procedures.
- Audit dependencies and their licenses.
- Measure memory consumption and CPU use during scanning and streaming.
- Check for database connection leaks, file descriptor leaks, and unbounded caches.
- Add practical health checks and diagnostics.

### Resource policy

Do not optimize based only on assumptions. Measure first.

Prioritize:

1. Bounded-memory audio streaming.
2. Incremental scanning.
3. Efficient database queries and indexes.
4. Limited artwork caching.
5. Avoiding unnecessary background processes.
6. Avoiding excessive network requests.
7. Reasonable concurrency limits for modest hardware.

Do not introduce a benchmark suite or monitoring stack that consumes more resources than the application itself.

### Acceptance criteria

- Security-sensitive endpoints have tests.
- Path traversal attempts are rejected.
- Files outside configured library paths cannot be accessed through user-controlled paths.
- Disconnected storage and unavailable database scenarios produce controlled errors.
- Resource use is measured under representative workloads.
- Known limitations are documented.

# Phase 11 — Packaging and Open-Source Release

### Goal

Make Rewind straightforward for other people to install on their own servers.

### Tasks

- Finalize installation instructions.
- Provide a reproducible deployment configuration.
- Support a straightforward native or container-based deployment path based on the chosen architecture.
- Provide environment-variable documentation.
- Document persistent application-data directories.
- Document how to configure music storage.
- Explain permissions and USB mount requirements.
- Document local access and Tailscale access.
- Document backup, restore, upgrade, and rollback procedures.
- Add a license and contribution guidelines.
- Add an issue template and security-reporting instructions.
- Document supported operating systems and browsers.
- Provide an example configuration without credentials.
- Create a release checklist.

### Acceptance criteria

A new user following the documentation should be able to:

1. Install Rewind on a supported server.
2. Configure a music directory.
3. Scan a pendrive containing music.
4. Browse and play tracks.
5. Access the application from another device on the home network.
6. Access the application remotely through their configured Tailscale network.
7. Install the PWA on a supported device.
8. Create a theme and install a compatible plugin.
9. Back up and restore application data.

Do not declare the release ready until the documented installation process has been tested on a clean environment.

---

## 6. API and Data Model Guidelines

The following are initial design guidelines, not a mandate to implement all models in the first phase.

Potential entities:

- `Track`
- `Artist`
- `Album`
- `Genre`
- `Playlist`
- `PlaylistTrack`
- `LibraryRoot`
- `ScanJob` or equivalent scan state
- `UserPreference` or equivalent settings storage
- `PluginMetadata` only if persistent plugin management requires it

Avoid duplicating the same artist or album unnecessarily.

Track records should reference files using a validated path relative to a configured library root, or an equivalent safe storage identifier. Do not accept arbitrary absolute paths from API requests.

Keep database migrations versioned and repeatable.

For API design:

- Use predictable endpoint naming.
- Validate pagination and filter parameters.
- Return consistent errors.
- Avoid exposing internal filesystem paths unnecessarily.
- Avoid returning large binary data inside JSON responses.
- Document endpoints as they are introduced.
- Add tests for important API contracts.

---

## 7. Testing and Quality Standards

Every phase must include tests appropriate to its scope.

### Backend

Test:

- Configuration validation.
- Database operations.
- Filesystem scanning.
- Metadata extraction.
- Duplicate detection.
- Missing files and storage disconnection.
- Audio streaming and range requests.
- API validation.
- Playlist persistence.
- Security-sensitive filesystem access.
- Theme and plugin validation.

### Frontend

Test:

- Playback controls.
- Queue operations.
- Search and library browsing.
- Responsive behavior.
- Empty and error states.
- Theme selection.
- PWA behavior where supported.
- Keyboard and accessibility basics.

### General

- Run the existing lint and formatting commands.
- Run relevant tests after changes.
- Run the production build at meaningful milestones.
- Never report a test as passing if it was not executed successfully.
- Clearly distinguish automated tests from manual checks.
- Add regression tests when fixing bugs.
- Prefer deterministic tests and avoid requiring external services for ordinary unit tests.

---

## 8. Configuration and Deployment

Keep deployment configuration simple and documented.

Configuration should cover only the settings the application actually needs, such as:

- Application environment.
- Bind address and port.
- Database location or connection configuration.
- Music-library roots.
- Application-data directory.
- Artwork-cache limits.
- Logging level.
- Optional authentication settings if authentication is implemented.
- Optional plugin directory.

Do not hardcode a user's USB mount path or Tailscale hostname.

Never commit `.env` files containing secrets, private credentials, or deployment-specific sensitive information.

Provide safe defaults where possible, and fail clearly when required settings are missing.

---

## 9. Explicitly Out of Scope for the Initial Release

Do not implement the following unless the project owner explicitly approves a later phase:

- Public music streaming or a public account system.
- Social features, comments, or following other users.
- Music purchasing or subscriptions.
- Integration with Spotify, Apple Music, or other commercial streaming catalogs as a core dependency.
- AI-generated playlists or AI recommendations.
- Automatic downloading of music from the internet.
- Automatic USB formatting or file modification.
- Automatic mounting and unmounting of USB devices.
- Custom VPN implementation.
- Public internet exposure by default.
- Complex multi-server clustering.
- Kubernetes or microservices.
- Mandatory transcoding.
- A plugin marketplace.
- Arbitrary remote plugin execution.
- Arbitrary executable theme files.
- An elaborate analytics dashboard.
- Features that duplicate existing browser capabilities without a clear benefit.

Rewind is a personal music player, not an attempt to recreate a commercial streaming service.

---

## 10. Definition of Done

A phase is complete only when:

- Its defined scope is implemented.
- Relevant tests have been executed.
- Existing functionality remains intact.
- Security implications have been considered.
- Configuration and documentation are updated.
- No unexplained placeholders remain in required functionality.
- Failures and limitations are reported honestly.
- The owner has enough information to review the changes.

Completing a phase does not authorize work on the next phase.

## 11. Final Instruction to Claude Code

Build Rewind incrementally.

Prioritize a reliable music library and playback experience before advanced customization. Keep the architecture small, dependencies justified, storage access safe, and documentation practical.

When multiple solutions are viable, prefer the simplest solution that meets the requirements and remains maintainable.

Do not invent requirements, expand scope without approval, or sacrifice resource efficiency for unnecessary abstraction.

**Inspect first. Plan the active phase. Implement only the approved scope. Test. Document. Report. Stop and wait for approval.**