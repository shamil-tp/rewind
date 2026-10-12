<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { ApiError, listTracks, type LibrarySortChoice, type Track, type TrackQuery } from '../services/api'
  import { formatTime } from '../services/format'
  import { LIBRARY_SORTS as sorts } from '../services/librarySort'
  import { sentence } from '../services/validation'
  import AddToPlaylist from './AddToPlaylist.svelte'

  interface Props {
    currentId: number | null
    playing: boolean
    /** Incremented by the parent when the library changed (e.g. after a scan). */
    reloadKey: number
    /** The saved default sort. The menu starts here and follows changes to it. */
    defaultSort: LibrarySortChoice
    onSelect: (track: Track) => void
    onUnauthorized: () => void
  }
  let { currentId, playing, reloadKey, defaultSort, onSelect, onUnauthorized }: Props = $props()

  const pageSize = 50
  const searchDelay = 300

  let items = $state<Track[]>([])
  let total = $state(0)
  let page = $state(0)
  let input = $state('')
  let activeQuery = $state('')
  // Only the initial value; the effect below follows later changes.
  // svelte-ignore state_referenced_locally
  let sortChoice = $state<LibrarySortChoice>(defaultSort)
  // The track whose "add to playlist" panel is open, and the last result.
  let addingId = $state<number | null>(null)
  let addedMessage = $state('')
  let loading = $state(false)
  let error = $state('')
  let loaded = $state(false)

  let controller: AbortController | undefined
  let requestSeq = 0
  let debounce: ReturnType<typeof setTimeout> | undefined

  async function load(reset: boolean) {
    controller?.abort()
    controller = new AbortController()
    const seq = ++requestSeq
    const choice = sorts.find((s) => s.value === sortChoice) ?? sorts[0]
    const next = reset ? 1 : page + 1
    const query: TrackQuery = { page: next, limit: pageSize, sort: choice.sort, order: choice.order }
    if (activeQuery) query.q = activeQuery
    loading = true
    error = ''
    try {
      const res = await listTracks(query, controller.signal)
      if (seq !== requestSeq) return // a newer request superseded this one
      items = reset ? res.items : [...items, ...res.items]
      total = res.pagination.total
      page = next
      loaded = true
    } catch (err) {
      if (seq !== requestSeq || (err instanceof DOMException && err.name === 'AbortError')) return
      if (err instanceof ApiError && err.status === 401) {
        onUnauthorized()
        return
      }
      error = err instanceof Error ? sentence(err.message) : 'Could not load tracks.'
    } finally {
      if (seq === requestSeq) loading = false
    }
  }

  /** Applies the search box after a pause in typing; pagination restarts. */
  function onInput() {
    clearTimeout(debounce)
    debounce = setTimeout(applySearch, searchDelay)
  }

  function applySearch() {
    clearTimeout(debounce)
    const q = input.trim()
    if (q === activeQuery && loaded) return
    activeQuery = q
    void load(true)
  }

  function onSubmit(event: SubmitEvent) {
    event.preventDefault()
    applySearch()
  }

  function clearSearch() {
    input = ''
    applySearch()
  }

  function onSortChange() {
    void load(true)
  }

  // A newly saved default sort applies at once.
  let lastDefaultSort: LibrarySortChoice | undefined
  $effect(() => {
    const next = defaultSort
    if (lastDefaultSort !== undefined && next !== lastDefaultSort && next !== sortChoice) {
      sortChoice = next
      void load(true)
    }
    lastDefaultSort = next
  })

  function toggleAdd(id: number) {
    addedMessage = ''
    addingId = addingId === id ? null : id
  }

  function added(message: string) {
    addingId = null
    addedMessage = message
  }

  let lastReloadKey: number | undefined
  $effect(() => {
    const key = reloadKey
    if (lastReloadKey !== undefined && key !== lastReloadKey) void load(true)
    lastReloadKey = key
  })

  onMount(() => {
    void load(true)
  })

  onDestroy(() => {
    clearTimeout(debounce)
    controller?.abort()
  })

  const countText = $derived(
    `${total} ${total === 1 ? 'track' : 'tracks'}${activeQuery ? ` matching “${activeQuery}”` : ''}`,
  )

  function trackLabel(t: Track): string {
    const parts = [`Play ${t.title}`]
    if (t.artist) parts.push(`by ${t.artist}`)
    if (!t.available) parts.push('(unavailable)')
    return parts.join(' ')
  }
</script>

<section class="view" aria-labelledby="library-title">
  <header class="view-header">
    <h1 id="library-title" class="view-title" tabindex="-1">Library</h1>
    {#if loaded}
      <p class="count" aria-live="polite">{countText}</p>
    {/if}
  </header>
  <p class="added" role="status">{addedMessage}</p>

  <div class="tools">
    <form class="search" role="search" onsubmit={onSubmit}>
      <label class="rw-label" for="library-search">Search</label>
      <div class="search-row">
        <input
          id="library-search"
          class="rw-input"
          type="search"
          placeholder="Title, artist or album"
          maxlength="200"
          autocomplete="off"
          bind:value={input}
          oninput={onInput}
        />
        {#if input}
          <button class="rw-button rw-button-secondary clear" type="button" onclick={clearSearch}>Clear</button>
        {/if}
      </div>
    </form>
    <div class="sort">
      <label class="rw-label" for="library-sort">Sort by</label>
      <select id="library-sort" class="rw-input" bind:value={sortChoice} onchange={onSortChange}>
        {#each sorts as s (s.value)}
          <option value={s.value}>{s.label}</option>
        {/each}
      </select>
    </div>
  </div>

  {#if error}
    <div class="state">
      <p class="rw-alert" role="alert">{error}</p>
      <button class="rw-button rw-button-secondary" type="button" onclick={() => load(items.length === 0)}>Try again</button>
    </div>
  {:else if !loaded}
    <p class="state rw-muted" role="status">Loading tracks…</p>
  {:else if items.length === 0}
    <div class="state">
      {#if activeQuery}
        <p>No tracks match “{activeQuery}”.</p>
        <button class="rw-button rw-button-secondary" type="button" onclick={clearSearch}>Clear search</button>
      {:else}
        <p>Your library is empty.</p>
        <p class="rw-muted">
          Set <code>REWIND_LIBRARY_ROOTS</code> to your music folder, then rescan from <a href="#/status">Status</a>.
        </p>
      {/if}
    </div>
  {:else}
    <div class="columns" aria-hidden="true">
      <span>Title</span><span>Artist</span><span>Album</span><span class="len">Time</span>
    </div>
    <ol class="tracks" aria-label="Tracks" aria-busy={loading}>
      {#each items as track (track.id)}
        {@const isCurrent = track.id === currentId}
        <li class="item">
          <button
            class="row"
            class:current={isCurrent}
            type="button"
            onclick={() => track.available && onSelect(track)}
            disabled={!track.available}
            aria-current={isCurrent ? 'true' : undefined}
            aria-label={trackLabel(track)}
          >
            <span class="cell title">
              {#if isCurrent}<span class="marker">{playing ? 'Playing' : 'Paused'}</span>{/if}
              <span class="title-text">{track.title}</span>
            </span>
            <span class="cell artist" class:unknown={!track.artist}>{track.artist ?? '—'}</span>
            <span class="cell album" class:unknown={!track.album}>{track.album ?? '—'}</span>
            <span class="cell len">
              {#if !track.available}<span class="badge">Unavailable</span>{/if}
              {formatTime(track.duration_ms ? track.duration_ms / 1000 : undefined)}
            </span>
          </button>
          <button
            class="add"
            type="button"
            onclick={() => toggleAdd(track.id)}
            aria-expanded={addingId === track.id}
            aria-label={`Add ${track.title} to a playlist`}
            title="Add to playlist">+</button
          >
          {#if addingId === track.id}
            <div class="add-panel">
              <AddToPlaylist {track} onDone={added} onCancel={() => (addingId = null)} {onUnauthorized} />
            </div>
          {/if}
        </li>
      {/each}
    </ol>
    {#if items.length < total}
      <div class="more">
        <button class="rw-button rw-button-secondary" type="button" onclick={() => load(false)} disabled={loading}>
          {loading ? 'Loading…' : `Load more (${total - items.length} left)`}
        </button>
      </div>
    {:else if loading}
      <p class="rw-muted" role="status">Loading…</p>
    {/if}
  {/if}
</section>

<style>
  .tools {
    display: grid;
    grid-template-columns: 1fr minmax(10rem, 14rem);
    gap: var(--rw-space-4);
    align-items: end;
    margin-bottom: var(--rw-space-5);
  }

  .search,
  .sort {
    display: grid;
    gap: var(--rw-space-1);
    min-width: 0;
  }

  .search-row {
    display: flex;
    gap: var(--rw-space-2);
  }

  .clear {
    padding-inline: var(--rw-space-3);
  }

  select.rw-input {
    appearance: auto;
  }

  .state {
    display: grid;
    gap: var(--rw-space-3);
    justify-items: start;
    padding: var(--rw-space-5) 0;
  }

  .columns,
  .row {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(0, 1.2fr) minmax(0, 1.2fr) 6.5rem;
    gap: var(--rw-space-4);
    align-items: center;
  }

  .columns {
    /* Right padding lines the Time column up with rows that have an add button. */
    padding: 0 calc(var(--rw-space-3) + 2.75rem) var(--rw-space-2) var(--rw-space-3);
    border-bottom: var(--rw-border);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }

  .tracks {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .item {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    border-bottom: 1px solid var(--rw-color-surface);
  }

  /* The add button is a 44px-wide touch target beside each row. */
  .add {
    min-width: 2.75rem;
    border: 0;
    border-left: 1px solid var(--rw-color-surface);
    background: transparent;
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 1.25rem;
    font-weight: var(--rw-font-weight-bold);
    cursor: pointer;
  }

  .add:hover,
  .add[aria-expanded='true'] {
    background: var(--rw-color-surface);
    color: var(--rw-color-primary);
  }

  .add:focus-visible {
    outline-offset: -3px;
  }

  .add-panel {
    grid-column: 1 / -1;
    margin-bottom: var(--rw-space-2);
    padding: var(--rw-space-3);
    border-left: var(--rw-border-width) solid var(--rw-color-accent);
    border-radius: var(--rw-radius-md);
    background: var(--rw-color-surface);
  }

  .added {
    min-height: 1.25em;
    margin-bottom: var(--rw-space-2);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  .row {
    width: 100%;
    min-height: 3rem;
    padding: var(--rw-space-2) var(--rw-space-3);
    border: 0;
    border-left: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-sm);
    background: transparent;
    color: var(--rw-color-text);
    text-align: left;
    cursor: pointer;
  }

  .row:hover:not(:disabled) {
    background: var(--rw-color-surface);
  }

  .row.current {
    border-left-color: var(--rw-color-accent);
    background: var(--rw-color-surface);
  }

  .row:disabled {
    cursor: not-allowed;
    color: var(--rw-color-muted);
  }

  .row:focus-visible {
    outline-offset: -3px;
  }

  .cell {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .title {
    display: flex;
    align-items: center;
    gap: var(--rw-space-2);
    font-weight: var(--rw-font-weight-bold);
  }

  .title-text {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .marker {
    flex: none;
    padding: 0 var(--rw-space-1);
    border-radius: var(--rw-radius-xs);
    background: var(--rw-color-accent);
    color: var(--rw-color-on-accent);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .artist,
  .album {
    color: var(--rw-color-muted);
  }

  .unknown {
    opacity: 0.6;
  }

  .len {
    text-align: right;
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    font-variant-numeric: tabular-nums;
  }

  .badge {
    margin-right: var(--rw-space-2);
    padding: 0 var(--rw-space-1);
    border: 1px solid var(--rw-color-danger);
    border-radius: var(--rw-radius-xs);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .more {
    padding: var(--rw-space-5) 0;
  }

  /* Narrow screens: two-line rows, no column headers. */
  @media (max-width: 48rem) {
    .tools {
      grid-template-columns: 1fr;
    }

    .columns {
      display: none;
    }

    .row {
      grid-template-columns: minmax(0, 1fr) auto;
      grid-template-areas: 'title len' 'artist len' 'album len';
      row-gap: 0;
      column-gap: var(--rw-space-3);
      min-height: 3.5rem;
    }

    .title {
      grid-area: title;
    }

    .artist {
      grid-area: artist;
      font-size: var(--rw-font-size-sm);
    }

    .album {
      grid-area: album;
      font-size: var(--rw-font-size-sm);
    }

    .album.unknown,
    .artist.unknown {
      display: none;
    }

    .len {
      grid-area: len;
    }
  }
</style>
