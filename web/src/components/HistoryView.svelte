<script lang="ts">
  import { onDestroy } from 'svelte'
  import {
    ApiError,
    clearHistory,
    deleteHistory,
    getRecent,
    getTopTracks,
    listHistory,
    type HistoryEntry,
    type Recent,
    type Track,
  } from '../services/api'
  import { listening } from '../services/listening.svelte'
  import { player } from '../services/player.svelte'
  import { albumHref, artistHref } from '../services/ui.svelte'
  import { sentence } from '../services/validation'
  import AddToPlaylist from './AddToPlaylist.svelte'
  import Artwork from './Artwork.svelte'
  import TrackList from './TrackList.svelte'

  interface Props {
    active: boolean
    onUnauthorized: () => void
  }
  let { active, onUnauthorized }: Props = $props()

  const PAGE = 50
  let recent = $state<Recent | null>(null)
  let top = $state<Track[]>([])
  let entries = $state<HistoryEntry[]>([])
  let total = $state(0)
  let page = $state(0)
  let loaded = $state(false)
  let loading = $state(false)
  let error = $state('')
  let input = $state('')
  let query = $state('')
  let from = $state('')
  let to = $state('')
  let order = $state<'desc' | 'asc'>('desc')
  let selected = $state<number[]>([])
  let adding = $state(false)
  let confirmClear = $state(false)
  let busy = $state(false)
  let seq = 0
  let debounce: ReturnType<typeof setTimeout> | undefined

  function failed(err: unknown, fallback: string) {
    if (err instanceof ApiError && err.status === 401) onUnauthorized()
    else error = err instanceof Error ? sentence(err.message) : fallback
  }

  /** Local date (yyyy-mm-dd) to the start of that day in RFC 3339. */
  const dayStart = (d: string, plus = 0) => {
    const t = new Date(`${d}T00:00:00`)
    t.setDate(t.getDate() + plus)
    return t.toISOString()
  }

  async function loadHistory(reset: boolean) {
    const mine = ++seq
    const next = reset ? 1 : page + 1
    loading = true
    error = ''
    try {
      const res = await listHistory({
        page: next, limit: PAGE, q: query, order,
        from: from ? dayStart(from) : undefined, to: to ? dayStart(to, 1) : undefined,
      })
      if (mine !== seq) return
      entries = reset ? res.items : [...entries, ...res.items]
      total = res.pagination.total
      page = next
      loaded = true
      if (reset) selected = []
    } catch (err) {
      if (mine === seq) failed(err, 'Could not load your history.')
    } finally {
      if (mine === seq) loading = false
    }
  }

  async function loadSummary() {
    try {
      const [r, t] = await Promise.all([getRecent(12), getTopTracks(10)])
      recent = r
      top = t
    } catch (err) {
      failed(err, 'Could not load your recent plays.')
    }
  }

  // Load when shown, and again after new plays.
  let lastVersion = -1
  $effect(() => {
    const v = listening.historyVersion + listening.libraryVersion * 100000
    if (active && v !== lastVersion) {
      lastVersion = v
      void loadSummary()
      void loadHistory(true)
    }
  })

  function onInput() {
    clearTimeout(debounce)
    debounce = setTimeout(() => {
      if (input.trim() !== query) {
        query = input.trim()
        void loadHistory(true)
      }
    }, 300)
  }
  onDestroy(() => clearTimeout(debounce))

  const tracks = $derived(entries.map((e) => e.track))
  const selectedTracks = $derived(selected.map((i) => entries[i]?.track).filter((t): t is Track => !!t))
  const when = (iso: string) => new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })

  async function removeSelected() {
    if (selected.length === 0 || busy) return
    busy = true
    try {
      const n = await deleteHistory(selected.map((i) => entries[i].id))
      listening.announce(`Removed ${n} ${n === 1 ? 'play' : 'plays'} from your history.`)
      listening.historyChanged()
    } catch (err) {
      failed(err, 'Could not remove the plays.')
    } finally {
      busy = false
    }
  }

  async function clearAll() {
    busy = true
    try {
      const n = await clearHistory()
      confirmClear = false
      listening.announce(`Cleared your history (${n} ${n === 1 ? 'play' : 'plays'}).`)
      listening.historyChanged()
    } catch (err) {
      failed(err, 'Could not clear your history.')
    } finally {
      busy = false
    }
  }
</script>

<section class="view" aria-labelledby="history-title">
  <header class="view-header">
    <h1 id="history-title" class="view-title" tabindex="-1">History</h1>
    {#if loaded}<p class="count" aria-live="polite">{total} {total === 1 ? 'play' : 'plays'}</p>{/if}
  </header>
  {#if !player.recordHistory}
    <p class="rw-notice">History is turned off in Settings, so new plays are not recorded.</p>
  {/if}

  {#if recent && (recent.albums.length > 0 || recent.artists.length > 0)}
    <h2 class="section">Recently played</h2>
    <ul class="strip" aria-label="Recently played albums and artists">
      {#each recent.albums as a (a.id)}
        <li><a class="card" href={albumHref(a.id)}><Artwork trackId={a.artwork_track_id} kind="disc" /><span class="name">{a.title}</span><span class="sub">{a.artist}</span></a></li>
      {/each}
      {#each recent.artists as a (a.id)}
        <li><a class="card" href={artistHref(a.id)}><Artwork trackId={a.artwork_track_id} kind="person" shape="round" /><span class="name">{a.name}</span><span class="sub">Artist</span></a></li>
      {/each}
    </ul>
  {/if}

  {#if top.length > 0}
    <h2 class="section">Most played</h2>
    <TrackList tracks={top} label="Most played" context={{ kind: 'history', name: 'Most played' }} {onUnauthorized} numbering="position" showArtwork
      detail={(t) => `${t.play_count ?? 0} ${t.play_count === 1 ? 'play' : 'plays'}`} />
  {/if}

  <h2 class="section">All plays</h2>
  <form class="filters" role="search" onsubmit={(e) => { e.preventDefault(); query = input.trim(); void loadHistory(true) }}>
    <div class="rw-field grow">
      <label class="rw-label" for="history-search">Search</label>
      <input id="history-search" class="rw-input" type="search" maxlength="200" bind:value={input} oninput={onInput} placeholder="Title, artist or album" />
    </div>
    <div class="rw-field">
      <label class="rw-label" for="history-from">From</label>
      <input id="history-from" class="rw-input" type="date" bind:value={from} onchange={() => loadHistory(true)} />
    </div>
    <div class="rw-field">
      <label class="rw-label" for="history-to">To</label>
      <input id="history-to" class="rw-input" type="date" bind:value={to} onchange={() => loadHistory(true)} />
    </div>
    <div class="rw-field">
      <label class="rw-label" for="history-order">Order</label>
      <select id="history-order" class="rw-input" bind:value={order} onchange={() => loadHistory(true)}>
        <option value="desc">Newest first</option>
        <option value="asc">Oldest first</option>
      </select>
    </div>
  </form>

  {#if error}
    <p class="rw-alert" role="alert">{error}</p>
    <button class="rw-button rw-button-secondary" type="button" onclick={() => loadHistory(true)}>Try again</button>
  {:else if !loaded}
    <p class="rw-muted" role="status">Loading history…</p>
  {:else if entries.length === 0}
    <p class="state">{query || from || to ? 'No plays match these filters.' : 'Nothing played yet. Tracks appear here once you have listened to enough of them.'}</p>
  {:else}
    <div class="bulk" aria-live="polite">
      <span class="count">{selected.length} selected</span>
      <button class="rw-button rw-button-secondary small" type="button" disabled={selectedTracks.length === 0}
        onclick={() => player.playTracks(selectedTracks, 0, { kind: 'history', name: 'History' })}>Play selected</button>
      <button class="rw-button rw-button-secondary small" type="button" disabled={selectedTracks.length === 0} aria-expanded={adding}
        onclick={() => (adding = !adding)}>Add selected to playlist…</button>
      <button class="rw-button rw-button-secondary small" type="button" disabled={selected.length === 0 || busy} onclick={removeSelected}>Remove from history</button>
      {#if confirmClear}
        <span class="confirm" role="alertdialog" aria-label="Clear all history?">
          Clear all {total} plays? Play counts reset too.
          <button class="rw-button small" type="button" disabled={busy} onclick={clearAll}>Clear all</button>
          <button class="rw-button rw-button-secondary small" type="button" onclick={() => (confirmClear = false)}>Keep</button>
        </span>
      {:else}
        <button class="rw-button rw-button-secondary small" type="button" onclick={() => (confirmClear = true)}>Clear all history…</button>
      {/if}
    </div>
    {#if adding && selectedTracks.length > 0}
      <div class="panel">
        <AddToPlaylist track={selectedTracks[0]} tracks={selectedTracks} name={`${selectedTracks.length} selected tracks`}
          onDone={(m) => { listening.announce(m); adding = false }} onCancel={() => (adding = false)} {onUnauthorized} />
      </div>
    {/if}
    <TrackList {tracks} label="All plays" context={{ kind: 'history', name: 'History' }} {onUnauthorized} bind:selected showArtwork
      detail={(_, i) => when(entries[i].played_at)} />
    {#if entries.length < total}
      <div class="more">
        <button class="rw-button rw-button-secondary" type="button" onclick={() => loadHistory(false)} disabled={loading}>
          {loading ? 'Loading…' : `Load more (${total - entries.length} left)`}
        </button>
      </div>
    {/if}
  {/if}
</section>

<style>
  .section {
    margin: var(--rw-space-5) 0 var(--rw-space-3);
    font-size: var(--rw-font-size-lg);
    font-weight: var(--rw-font-weight-black);
    text-transform: uppercase;
  }

  .strip {
    display: grid;
    grid-auto-flow: column;
    grid-auto-columns: minmax(8.5rem, 9.5rem);
    gap: var(--rw-space-3);
    margin: 0;
    padding: 0 0 var(--rw-space-2);
    overflow-x: auto;
    list-style: none;
  }

  .card {
    display: grid;
    gap: var(--rw-space-1);
    padding: var(--rw-space-2);
    border: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-lg);
    color: var(--rw-color-text);
    text-decoration: none;
  }

  .card:hover {
    border-color: var(--rw-color-border);
  }

  .name,
  .sub {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .name {
    font-weight: var(--rw-font-weight-bold);
  }

  .sub {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .filters {
    display: grid;
    grid-template-columns: minmax(0, 2fr) repeat(3, minmax(0, 1fr));
    gap: var(--rw-space-3);
    align-items: end;
    margin-bottom: var(--rw-space-4);
  }

  select.rw-input {
    appearance: auto;
  }

  .bulk {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2);
    margin-bottom: var(--rw-space-3);
  }

  .confirm {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2);
    padding: var(--rw-space-2);
    border: var(--rw-border-width) solid var(--rw-color-danger);
    border-radius: var(--rw-radius-md);
  }

  .small {
    min-height: 2.5rem;
    padding-inline: var(--rw-space-3);
  }

  .panel {
    margin-bottom: var(--rw-space-3);
    padding: var(--rw-space-3);
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-surface);
  }

  .state {
    padding: var(--rw-space-4) 0;
  }

  .more {
    margin-top: var(--rw-space-4);
  }

  @media (max-width: 48rem) {
    .filters {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .grow {
      grid-column: 1 / -1;
    }
  }
</style>
