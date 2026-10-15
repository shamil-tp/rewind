<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte'
  import {
    ApiError,
    DEFAULT_PREFERENCES,
    getPreferences,
    getThemes,
    type Preferences,
    type ThemeState,
    type User,
  } from '../services/api'
  import { listening } from '../services/listening.svelte'
  import { player } from '../services/player.svelte'
  import { handleShortcut } from '../services/shortcuts'
  import { applyTheme, rememberTheme, resolveShown, type ThemePreview } from '../services/themes'
  import { navigate, ui } from '../services/ui.svelte'
  import { sentence } from '../services/validation'
  import AlbumView from './AlbumView.svelte'
  import ArtistView from './ArtistView.svelte'
  import BrowseGrid from './BrowseGrid.svelte'
  import FavoritesView from './FavoritesView.svelte'
  import HistoryView from './HistoryView.svelte'
  import Icon, { type IconName } from './Icon.svelte'
  import LibraryView from './LibraryView.svelte'
  import MetadataEditor from './MetadataEditor.svelte'
  import Player from './Player.svelte'
  import PlaylistsView from './PlaylistsView.svelte'
  import QueuePanel from './QueuePanel.svelte'
  import SettingsView from './SettingsView.svelte'
  import ShortcutsDialog from './ShortcutsDialog.svelte'
  import StatusView from './StatusView.svelte'

  interface Props {
    user: User
    signingOut: boolean
    signOutError: string
    onSignOut: () => void
    onUnauthorized: () => void
  }
  let { user, signingOut, signOutError, onSignOut, onUnauthorized }: Props = $props()

  type View = 'library' | 'albums' | 'artists' | 'playlists' | 'favorites' | 'history' | 'settings' | 'status'
  const routes: { id: View; label: string; icon: IconName; primary: boolean }[] = [
    { id: 'library', label: 'Library', icon: 'note', primary: true },
    { id: 'albums', label: 'Albums', icon: 'disc', primary: true },
    { id: 'artists', label: 'Artists', icon: 'person', primary: true },
    { id: 'playlists', label: 'Playlists', icon: 'list', primary: true },
    { id: 'favorites', label: 'Favorites', icon: 'heartFilled', primary: false },
    { id: 'history', label: 'History', icon: 'clock', primary: false },
    { id: 'settings', label: 'Settings', icon: 'gear', primary: false },
    { id: 'status', label: 'Status', icon: 'info', primary: false },
  ]

  interface Route {
    view: View
    playlistId: number | null
    albumId: string | null
    artistId: string | null
  }

  /**
   * #/library, #/albums[/{id}], #/artists[/{id}], #/playlists[/{id}],
   * #/favorites, #/history, #/settings, #/status. Anything else is the library.
   */
  function parseRoute(hash: string): Route {
    const r: Route = { view: 'library', playlistId: null, albumId: null, artistId: null }
    let m = /^#\/playlists\/([1-9][0-9]{0,15})$/.exec(hash)
    if (m) return { ...r, view: 'playlists', playlistId: Number(m[1]) }
    m = /^#\/(albums|artists)\/([A-Za-z0-9_~-]{1,4096})$/.exec(hash)
    if (m) return m[1] === 'albums' ? { ...r, view: 'albums', albumId: m[2] } : { ...r, view: 'artists', artistId: m[2] }
    const view = hash.replace(/^#\//, '') as View
    return routes.some((x) => x.id === view) ? { ...r, view } : r
  }

  const initial = parseRoute(location.hash)
  let current = $state<Route>(initial)
  const route = $derived(current.view)
  // Views are created the first time they are opened, then kept (hidden), so
  // switching keeps search text, loaded pages and scroll positions.
  let opened = $state<Record<View, boolean>>({
    library: true, albums: false, artists: false, playlists: false, favorites: false, history: false, settings: false, status: false,
    [initial.view]: true,
  })
  let libraryVersion = $state(0)
  let moreOpen = $state(false)

  // Sidebar collapse (tablets and small laptops), remembered per device.
  let collapsed = $state(false)
  try {
    collapsed = localStorage.getItem('rewind.sidebar') === 'collapsed'
  } catch {
    // Storage unavailable: expanded.
  }
  function toggleSidebar() {
    collapsed = !collapsed
    try {
      localStorage.setItem('rewind.sidebar', collapsed ? 'collapsed' : 'expanded')
    } catch {
      // Optional.
    }
  }

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

  // The player follows the playback preferences.
  $effect(() => {
    player.thresholdPercent = preferences.play_threshold_percent
    player.recordHistory = preferences.record_history
    player.autoAdvance = preferences.autoplay_next
  })

  // Themes: the server's theme state is the single source of truth for what
  // is shown. Until it loads (and if loading fails) the theme remembered at
  // startup stays. Settings may preview a theme or unsaved edits; the
  // preview is shown only while Settings is open, so leaving it never leaves
  // unsaved styles on other pages.
  let themes = $state<ThemeState | null>(null)
  let themesStatus = $state<'loading' | 'ready' | 'error'>('loading')
  let themesError = $state('')
  let themePreview = $state<ThemePreview | null>(null)

  async function loadThemes() {
    themesStatus = 'loading'
    try {
      themes = await getThemes()
      themesStatus = 'ready'
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        expired()
        return
      }
      themesError = err instanceof Error ? sentence(err.message) : 'Unknown error.'
      themesStatus = 'error'
    }
  }

  $effect(() => {
    if (!themes) return
    const preview = route === 'settings' ? themePreview : null
    const resolved = resolveShown(themes, preview)
    applyTheme(resolved)
    if (!preview) rememberTheme(resolved)
  })

  function headingFor(r: Route): string {
    if (r.view === 'playlists' && r.playlistId !== null) return 'playlist-title'
    if (r.view === 'albums' && r.albumId) return 'album-title'
    if (r.view === 'artists' && r.artistId) return 'artist-title'
    return `${r.view}-title`
  }

  async function onHashChange() {
    const next = parseRoute(location.hash)
    if (JSON.stringify(next) === JSON.stringify(current)) return
    current = next
    opened[next.view] = true
    moreOpen = false
    // Move focus to the new page's heading for keyboard and screen-reader users.
    await tick()
    document.getElementById(headingFor(next))?.focus()
  }

  async function focusSearch() {
    navigate('#/library')
    await tick()
    await tick()
    document.getElementById('library-search')?.focus()
  }

  function onKeydown(event: KeyboardEvent) {
    if (handleShortcut(event, focusSearch)) event.preventDefault()
  }

  onMount(() => {
    player.attach(expired)
    void player.restore()
    void loadPreferences()
    void loadThemes()
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  })

  onDestroy(() => {
    // Signing out or an expired session ends playback; the queue is saved.
    player.flush(false)
    player.detach()
    ui.queueOpen = ui.nowPlayingOpen = ui.shortcutsOpen = false
    ui.editTrackId = null
  })

  function expired() {
    player.detach()
    onUnauthorized()
  }

  const blocked = $derived(ui.nowPlayingOpen || ui.modalOpen)
  const isCurrent = (id: View) => route === id
</script>

<svelte:window onkeydown={onKeydown} />

<div class="app" class:has-player={player.current !== null} class:collapsed>
  <a class="skip" inert={blocked} href={`#${headingFor(current)}`}>Skip to content</a>

  <header class="rail" inert={blocked}>
    <div class="rail-head">
      <p class="brand">{collapsed ? 'R' : 'Rewind'}</p>
      <button class="collapse-btn" type="button" onclick={toggleSidebar} aria-pressed={collapsed} aria-label="Collapse the sidebar"
        title={collapsed ? 'Expand the sidebar' : 'Collapse the sidebar'}><Icon name="menu" /></button>
    </div>
    <nav aria-label="Main">
      <ul>
        {#each routes as r (r.id)}
          <li>
            <a href={`#/${r.id}`} aria-current={isCurrent(r.id) ? 'page' : undefined} title={collapsed ? r.label : undefined}>
              <Icon name={r.icon} /><span class="label">{r.label}</span>
            </a>
          </li>
        {/each}
      </ul>
    </nav>
    <div class="account">
      <button class="link-btn" type="button" onclick={() => ui.openShortcuts()} title="Keyboard shortcuts (?)">
        <span class="label">Keyboard shortcuts</span><span class="short" aria-hidden="true">?</span>
      </button>
      <span class="who">Signed in as <strong>{user.username}</strong></span>
      <button class="rw-button rw-button-secondary signout" type="button" onclick={onSignOut} disabled={signingOut}>
        {signingOut ? 'Signing out…' : 'Sign out'}
      </button>
      {#if signOutError}<p class="rw-alert" role="alert">{signOutError}</p>{/if}
    </div>
  </header>

  <main class="main" inert={blocked}>
    <div hidden={route !== 'library'}>
      {#if libraryStarted}
        <LibraryView reloadKey={libraryVersion + listening.libraryVersion} defaultSort={preferences.library_sort} onUnauthorized={expired} />
      {:else}
        <p class="rw-muted" role="status">Loading…</p>
      {/if}
    </div>
    {#if opened.albums}
      <div hidden={route !== 'albums' || current.albumId !== null}>
        <BrowseGrid kind="albums" active={route === 'albums' && current.albumId === null} onUnauthorized={expired} />
      </div>
      {#if current.albumId}
        <div hidden={route !== 'albums'}>
          <AlbumView id={current.albumId} active={route === 'albums'} onUnauthorized={expired} />
        </div>
      {/if}
    {/if}
    {#if opened.artists}
      <div hidden={route !== 'artists' || current.artistId !== null}>
        <BrowseGrid kind="artists" active={route === 'artists' && current.artistId === null} onUnauthorized={expired} />
      </div>
      {#if current.artistId}
        <div hidden={route !== 'artists'}>
          <ArtistView id={current.artistId} active={route === 'artists'} onUnauthorized={expired} />
        </div>
      {/if}
    {/if}
    {#if opened.playlists}
      <div hidden={route !== 'playlists'}>
        <PlaylistsView playlistId={current.playlistId} active={route === 'playlists'} onUnauthorized={expired} />
      </div>
    {/if}
    {#if opened.favorites}
      <div hidden={route !== 'favorites'}>
        <FavoritesView active={route === 'favorites'} onUnauthorized={expired} />
      </div>
    {/if}
    {#if opened.history}
      <div hidden={route !== 'history'}>
        <HistoryView active={route === 'history'} onUnauthorized={expired} />
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
          {themes}
          {themesStatus}
          {themesError}
          onRetryThemes={loadThemes}
          onThemesChanged={(t) => (themes = t)}
          onPreviewTheme={(p) => (themePreview = p)}
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

  <!-- Phones: primary destinations at the bottom, the rest under More. -->
  <nav class="bottom-nav" aria-label="Main (compact)" inert={blocked}>
    {#each routes.filter((r) => r.primary) as r (r.id)}
      <a href={`#/${r.id}`} aria-current={isCurrent(r.id) ? 'page' : undefined}><Icon name={r.icon} /><span>{r.label}</span></a>
    {/each}
    <button type="button" aria-expanded={moreOpen} aria-current={routes.some((r) => !r.primary && isCurrent(r.id)) ? 'page' : undefined}
      onclick={() => (moreOpen = !moreOpen)}><Icon name="menu" /><span>More</span></button>
  </nav>
  {#if moreOpen}
    <button class="sheet-backdrop" type="button" tabindex="-1" aria-hidden="true" onclick={() => (moreOpen = false)}></button>
    <div class="more-sheet" role="menu" aria-label="More">
      {#each routes.filter((r) => !r.primary) as r (r.id)}
        <a role="menuitem" href={`#/${r.id}`} onclick={() => (moreOpen = false)}><Icon name={r.icon} />{r.label}</a>
      {/each}
      <button role="menuitem" type="button" onclick={() => { moreOpen = false; ui.openShortcuts() }}>Keyboard shortcuts</button>
      <button role="menuitem" type="button" onclick={onSignOut} disabled={signingOut}>{signingOut ? 'Signing out…' : `Sign out (${user.username})`}</button>
    </div>
  {/if}

  <Player onUnauthorized={expired} />
  <QueuePanel onUnauthorized={expired} />
  {#if ui.editTrackId !== null}
    {#key ui.editTrackId}<MetadataEditor trackId={ui.editTrackId} onUnauthorized={expired} />{/key}
  {/if}
  {#if ui.shortcutsOpen}<ShortcutsDialog />{/if}
  <p class="announce" role="status" aria-live="polite">{listening.announcement}</p>
</div>

<style>
  .app {
    --rail-width: 13rem;
    --playbar-space: 0px;
    --rw-bottom-nav: 0px;
    display: grid;
    grid-template-columns: var(--rail-width) minmax(0, 1fr);
    min-height: 100vh;
    min-height: 100dvh;
  }

  .app.collapsed {
    --rail-width: 4.5rem;
  }

  /* Keep the last rows clear of the floating playback bar. */
  .app.has-player {
    --playbar-space: calc(8rem + env(safe-area-inset-bottom));
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
    padding: var(--rw-space-5) var(--rw-space-3);
    padding-bottom: calc(var(--rw-space-5) + var(--playbar-space));
    overflow-y: auto;
    border-right: var(--rw-border);
  }

  .rail-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--rw-space-2);
    padding-left: var(--rw-space-1);
  }

  .brand {
    font-size: 1.75rem;
    font-weight: var(--rw-font-weight-black);
    line-height: 1;
    letter-spacing: -0.04em;
    text-transform: uppercase;
  }

  .collapse-btn,
  .link-btn {
    display: grid;
    place-items: center;
    min-width: 2.75rem;
    min-height: 2.75rem;
    padding: 0;
    border: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-sm);
    background: transparent;
    color: var(--rw-color-muted);
    cursor: pointer;
  }

  .collapse-btn:hover,
  .link-btn:hover {
    border-color: var(--rw-color-border);
    color: var(--rw-color-text);
  }

  .link-btn {
    justify-items: start;
    padding-inline: var(--rw-space-2);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  .short {
    display: none;
  }

  nav ul {
    display: grid;
    gap: var(--rw-space-1);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .rail nav a {
    display: flex;
    align-items: center;
    gap: var(--rw-space-2);
    min-height: 2.75rem;
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

  .rail nav a:hover {
    color: var(--rw-color-text);
    background: var(--rw-color-surface);
  }

  /* The current page is marked by a filled shape, not colour alone. */
  .rail nav a[aria-current='page'] {
    border-color: var(--rw-color-border);
    background: var(--rw-color-primary);
    color: var(--rw-color-on-primary);
  }

  .collapsed .label,
  .collapsed .who,
  .collapsed .signout {
    display: none;
  }

  .collapsed .short {
    display: inline;
  }

  .collapsed .rail-head {
    flex-direction: column;
  }

  .collapsed .rail nav a {
    justify-content: center;
    padding-inline: 0;
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

  .bottom-nav,
  .more-sheet,
  .sheet-backdrop {
    display: none;
  }

  .announce {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  /* Tablets: a narrow sidebar by default-sized layout, collapsible. */
  @media (max-width: 64rem) {
    .main {
      padding: var(--rw-space-5) var(--rw-space-4);
      padding-bottom: calc(var(--rw-space-5) + var(--playbar-space));
    }
  }

  /* Phones: no sidebar; bottom navigation with the player above it. */
  @media (max-width: 48rem) {
    .app {
      --rw-bottom-nav: calc(3.75rem + env(safe-area-inset-bottom));
      grid-template-columns: minmax(0, 1fr);
    }

    .app.has-player {
      --playbar-space: calc(5.5rem + env(safe-area-inset-bottom));
    }

    .rail {
      display: none;
    }

    .main {
      padding: calc(var(--rw-space-4) + env(safe-area-inset-top)) var(--rw-space-3);
      padding-bottom: calc(var(--rw-space-5) + var(--playbar-space) + var(--rw-bottom-nav));
    }

    .bottom-nav {
      position: fixed;
      inset: auto 0 0 0;
      z-index: 11;
      display: grid;
      grid-template-columns: repeat(5, minmax(0, 1fr));
      padding-bottom: env(safe-area-inset-bottom);
      border-top: var(--rw-border);
      background: var(--rw-color-bg);
    }

    .bottom-nav a,
    .bottom-nav button {
      display: grid;
      justify-items: center;
      gap: 2px;
      min-height: 3.75rem;
      padding: var(--rw-space-2) 0;
      border: 0;
      background: transparent;
      color: var(--rw-color-muted);
      font: inherit;
      font-family: var(--rw-font-mono);
      font-size: 0.65rem;
      text-decoration: none;
      text-transform: uppercase;
      cursor: pointer;
    }

    .bottom-nav [aria-current='page'] {
      color: var(--rw-color-text);
      box-shadow: inset 0 3px 0 var(--rw-color-primary);
    }

    .sheet-backdrop {
      position: fixed;
      inset: 0;
      z-index: 38;
      display: block;
      border: 0;
      background: var(--rw-color-bg);
      opacity: 0.7;
    }

    .more-sheet {
      position: fixed;
      inset: auto 0 0 0;
      z-index: 39;
      display: grid;
      padding: var(--rw-space-2) var(--rw-space-2) calc(var(--rw-space-3) + env(safe-area-inset-bottom));
      border: var(--rw-border);
      border-radius: var(--rw-radius-lg) var(--rw-radius-lg) 0 0;
      background: var(--rw-color-surface);
    }

    .more-sheet a,
    .more-sheet button {
      display: flex;
      align-items: center;
      gap: var(--rw-space-3);
      min-height: 3rem;
      padding: var(--rw-space-2) var(--rw-space-3);
      border: 0;
      border-radius: var(--rw-radius-sm);
      background: transparent;
      color: var(--rw-color-text);
      font: inherit;
      text-align: left;
      text-decoration: none;
      cursor: pointer;
    }
  }
</style>
