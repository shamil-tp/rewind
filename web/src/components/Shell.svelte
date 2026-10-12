<script lang="ts">
  import { onMount, tick } from 'svelte'
  import {
    ApiError,
    DEFAULT_PREFERENCES,
    getPreferences,
    type PlaylistDetail,
    type Preferences,
    type Track,
    type User,
  } from '../services/api'
  import { nextPlayable, type Sequence } from '../services/sequence'
  import { applyTheme, rememberTheme, resolveTheme, type CustomColors } from '../services/themes'
  import { sentence } from '../services/validation'
  import LibraryView from './LibraryView.svelte'
  import Player from './Player.svelte'
  import PlaylistsView from './PlaylistsView.svelte'
  import SettingsView from './SettingsView.svelte'
  import StatusView from './StatusView.svelte'

  interface Props {
    user: User
    signingOut: boolean
    signOutError: string
    onSignOut: () => void
    onUnauthorized: () => void
  }
  let { user, signingOut, signOutError, onSignOut, onUnauthorized }: Props = $props()

  type View = 'library' | 'playlists' | 'settings' | 'status'
  const routes: { id: View; label: string }[] = [
    { id: 'library', label: 'Library' },
    { id: 'playlists', label: 'Playlists' },
    { id: 'settings', label: 'Settings' },
    { id: 'status', label: 'Status' },
  ]

  /** #/library, #/playlists, #/playlists/{id}, #/settings, #/status. Anything else is the library. */
  function parseRoute(hash: string): { view: View; playlistId: number | null } {
    const m = /^#\/playlists\/([1-9][0-9]{0,15})$/.exec(hash)
    if (m) return { view: 'playlists', playlistId: Number(m[1]) }
    switch (hash) {
      case '#/playlists':
        return { view: 'playlists', playlistId: null }
      case '#/settings':
        return { view: 'settings', playlistId: null }
      case '#/status':
        return { view: 'status', playlistId: null }
      default:
        return { view: 'library', playlistId: null }
    }
  }

  const initial = parseRoute(location.hash)
  let route = $state<View>(initial.view)
  let playlistId = $state<number | null>(initial.playlistId)
  let currentTrack = $state<Track | null>(null)
  let playing = $state(false)
  // While the Now Playing screen is open, the page behind it is inert.
  let playerExpanded = $state(false)
  let libraryVersion = $state(0)
  // Views other than the library are created the first time they are opened, then kept.
  let opened = $state<Record<View, boolean>>({ library: true, playlists: false, settings: false, status: false, [initial.view]: true })

  // Saved preferences. The library waits for them so it loads only once, in
  // the saved order; if they cannot be loaded, defaults are used and the
  // settings view says so.
  let preferences = $state<Preferences>({ ...DEFAULT_PREFERENCES })
  let prefsStatus = $state<'loading' | 'ready' | 'error'>('loading')
  let prefsError = $state('')
  // Set once the first preferences request finished; the library then stays mounted.
  let libraryStarted = $state(false)

  async function loadPreferences() {
    prefsStatus = 'loading'
    try {
      preferences = { ...DEFAULT_PREFERENCES, ...(await getPreferences()) }
      prefsStatus = 'ready'
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        expired()
        return
      }
      prefsError = err instanceof Error ? sentence(err.message) : 'Unknown error.'
      prefsStatus = 'error'
    } finally {
      libraryStarted = true
    }
  }

  // The theme follows the saved preference once it has loaded; until then
  // (and if loading fails) the theme remembered at startup stays. Settings
  // may preview a theme while saving it.
  let themePreview = $state<string | null>(null)
  const customColors = $derived<CustomColors>({
    primary: preferences.primary_color,
    secondary: preferences.secondary_color,
    accent: preferences.accent_color,
  })
  $effect(() => {
    if (prefsStatus !== 'ready') return
    const resolved = resolveTheme(themePreview ?? preferences.theme, customColors)
    applyTheme(resolved)
    if (themePreview === null) {
      // The hint holds only the colors actually shown with this theme.
      const shown = { ...customColors }
      for (const role of resolved.ignored) shown[role] = ''
      rememberTheme(resolved.theme.id, shown)
    }
  })

  // A playlist being played in order: its tracks as they were when Play was
  // pressed, and the position of the current one.
  let queue = $state<{ name: string; tracks: Track[]; index: number } | null>(null)

  function playTrack(track: Track) {
    queue = null
    currentTrack = track
  }

  function playPlaylist(playlist: PlaylistDetail, index: number) {
    const tracks = playlist.entries.map((e) => e.track)
    if (!tracks[index]?.available) return
    queue = { name: playlist.name, tracks, index }
    currentTrack = tracks[index]
  }

  function step(by: 1 | -1) {
    if (!queue) return
    const i = nextPlayable(queue.tracks, queue.index, by)
    if (i < 0) return
    queue.index = i
    currentTrack = queue.tracks[i]
  }

  function ended() {
    if (queue && preferences.autoplay_next) step(1)
  }

  const sequence = $derived<Sequence | null>(
    queue
      ? {
          label: `${queue.name} · ${queue.index + 1} of ${queue.tracks.length}`,
          hasPrevious: nextPlayable(queue.tracks, queue.index, -1) >= 0,
          hasNext: nextPlayable(queue.tracks, queue.index, 1) >= 0,
          onPrevious: () => step(-1),
          onNext: () => step(1),
        }
      : null,
  )

  async function onHashChange() {
    const next = parseRoute(location.hash)
    if (next.view === route && next.playlistId === playlistId) return
    route = next.view
    playlistId = next.playlistId
    opened[next.view] = true
    // Move focus to the new page's heading for keyboard and screen-reader users.
    await tick()
    const heading = next.view === 'playlists' && next.playlistId !== null ? 'playlist-title' : `${next.view}-title`
    document.getElementById(heading)?.focus()
  }

  onMount(() => {
    void loadPreferences()
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  })

  function expired() {
    queue = null
    currentTrack = null
    onUnauthorized()
  }
</script>

<div class="app" class:has-player={currentTrack !== null}>
  <a class="skip" inert={playerExpanded} href={`#${route === 'playlists' && playlistId !== null ? 'playlist' : route}-title`}>Skip to content</a>

  <header class="rail" inert={playerExpanded}>
    <p class="brand">Rewind</p>
    <nav aria-label="Main">
      <ul>
        {#each routes as r (r.id)}
          <li>
            <a href={`#/${r.id}`} aria-current={route === r.id ? 'page' : undefined}>{r.label}</a>
          </li>
        {/each}
      </ul>
    </nav>
    <div class="account">
      <span class="who">Signed in as <strong>{user.username}</strong></span>
      <button class="rw-button rw-button-secondary signout" type="button" onclick={onSignOut} disabled={signingOut}>
        {signingOut ? 'Signing out…' : 'Sign out'}
      </button>
      {#if signOutError}<p class="rw-alert" role="alert">{signOutError}</p>{/if}
    </div>
  </header>

  <main class="main" inert={playerExpanded}>
    <!-- Views stay mounted while hidden, so switching keeps search, scroll and loaded pages. -->
    <div hidden={route !== 'library'}>
      {#if libraryStarted}
        <LibraryView
          currentId={currentTrack?.id ?? null}
          {playing}
          reloadKey={libraryVersion}
          defaultSort={preferences.library_sort}
          onSelect={playTrack}
          onUnauthorized={expired}
        />
      {:else}
        <p class="rw-muted" role="status">Loading…</p>
      {/if}
    </div>
    {#if opened.playlists}
      <div hidden={route !== 'playlists'}>
        <PlaylistsView
          {playlistId}
          active={route === 'playlists'}
          currentId={currentTrack?.id ?? null}
          {playing}
          onPlay={playPlaylist}
          onUnauthorized={expired}
        />
      </div>
    {/if}
    {#if opened.settings}
      <div hidden={route !== 'settings'}>
        <SettingsView
          {preferences}
          status={prefsStatus}
          loadError={prefsError}
          onRetry={loadPreferences}
          onSaved={(p) => (preferences = { ...DEFAULT_PREFERENCES, ...p })}
          onPreviewTheme={(id) => (themePreview = id)}
          onUnauthorized={expired}
        />
      </div>
    {/if}
    {#if opened.status}
      <div hidden={route !== 'status'}>
        <StatusView onUnauthorized={expired} onScanFinished={() => libraryVersion++} />
      </div>
    {/if}
  </main>

  <Player
    track={currentTrack}
    {sequence}
    onEnded={ended}
    onUnauthorized={expired}
    onPlayingChange={(p) => (playing = p)}
    onExpandedChange={(e) => (playerExpanded = e)}
  />
</div>

<style>
  .app {
    --rail-width: 13rem;
    --playbar-space: 0px;
    display: grid;
    grid-template-columns: var(--rail-width) minmax(0, 1fr);
    min-height: 100vh;
    min-height: 100dvh;
  }

  /* Keep the last rows clear of the floating playback bar. */
  .app.has-player {
    --playbar-space: calc(7.5rem + env(safe-area-inset-bottom));
  }

  .skip {
    position: absolute;
    left: -100vw;
    z-index: 20;
    padding: var(--rw-space-2) var(--rw-space-3);
    border-radius: var(--rw-radius-md);
    background: var(--rw-color-primary);
    color: var(--rw-color-on-primary);
  }

  .skip:focus {
    left: var(--rw-space-3);
    top: var(--rw-space-3);
  }

  .rail {
    position: sticky;
    top: 0;
    display: flex;
    flex-direction: column;
    gap: var(--rw-space-5);
    height: 100vh;
    height: 100dvh;
    padding: var(--rw-space-5) var(--rw-space-4);
    padding-bottom: calc(var(--rw-space-5) + var(--playbar-space));
    border-right: var(--rw-border);
  }

  .brand {
    font-size: 1.75rem;
    font-weight: var(--rw-font-weight-black);
    line-height: 1;
    letter-spacing: -0.04em;
    text-transform: uppercase;
  }

  nav ul {
    display: grid;
    gap: var(--rw-space-1);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  nav a {
    display: block;
    padding: var(--rw-space-2) var(--rw-space-3);
    border: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-md);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-weight: var(--rw-font-weight-bold);
    text-decoration: none;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  nav a:hover {
    color: var(--rw-color-text);
    background: var(--rw-color-surface);
  }

  /* The current page is marked by a filled shape and an arrow, not colour alone. */
  nav a[aria-current='page'] {
    border-color: var(--rw-color-border);
    background: var(--rw-color-primary);
    color: var(--rw-color-on-primary);
  }

  nav a[aria-current='page']::after {
    content: ' ←';
  }

  .account {
    display: grid;
    gap: var(--rw-space-2);
    margin-top: auto;
  }

  .who {
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    overflow-wrap: anywhere;
  }

  .main {
    min-width: 0;
    padding: var(--rw-space-6);
    padding-bottom: calc(var(--rw-space-6) + var(--playbar-space));
  }

  @media (max-width: 48rem) {
    .app {
      /* minmax(0, 1fr): the column never grows wider than the screen to fit content. */
      grid-template-columns: minmax(0, 1fr);
      grid-template-rows: auto 1fr;
    }

    .app.has-player {
      --playbar-space: calc(10.5rem + env(safe-area-inset-bottom));
    }

    .rail {
      position: static;
      flex-direction: row;
      flex-wrap: wrap;
      align-items: center;
      gap: var(--rw-space-2) var(--rw-space-4);
      height: auto;
      padding: var(--rw-space-3) var(--rw-space-3);
      padding-top: calc(var(--rw-space-3) + env(safe-area-inset-top));
      border-right: 0;
      border-bottom: var(--rw-border);
    }

    .brand {
      font-size: 1.4rem;
    }

    /* Four links do not fit on one line on small phones; they wrap. */
    nav ul {
      display: flex;
      flex-wrap: wrap;
      gap: 0 var(--rw-space-2);
    }

    nav a {
      padding: var(--rw-space-1) var(--rw-space-2);
    }

    nav a[aria-current='page']::after {
      content: '';
    }

    .account {
      grid-auto-flow: column;
      align-items: center;
      margin: 0 0 0 auto;
    }

    .who {
      display: none;
    }

    .signout {
      padding-inline: var(--rw-space-3);
    }

    .main {
      padding: var(--rw-space-4) var(--rw-space-3);
      padding-bottom: calc(var(--rw-space-5) + var(--playbar-space));
    }
  }
</style>
