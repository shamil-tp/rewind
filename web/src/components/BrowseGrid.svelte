<script lang="ts">
  // Albums or artists as a responsive grid of artwork cards, with search,
  // sort and paging. Stale responses (an older search finishing last) are
  // discarded.
  import { onDestroy } from 'svelte'
  import { ApiError, listAlbums, listArtists, type Album, type Artist } from '../services/api'
  import { formatTime } from '../services/format'
  import { listening } from '../services/listening.svelte'
  import { albumHref, artistHref } from '../services/ui.svelte'
  import { sentence } from '../services/validation'
  import Artwork from './Artwork.svelte'

  interface Props {
    kind: 'albums' | 'artists'
    /** Loads when first shown (views stay mounted while hidden). */
    active: boolean
    onUnauthorized: () => void
  }
  let { kind, active, onUnauthorized }: Props = $props()

  const SORTS = {
    albums: [
      { value: 'title', label: 'Title', order: 'asc' },
      { value: 'artist', label: 'Artist', order: 'asc' },
      { value: 'year', label: 'Newest', order: 'desc' },
      { value: 'added', label: 'Recently added', order: 'desc' },
    ],
    artists: [
      { value: 'name', label: 'Name', order: 'asc' },
      { value: 'tracks', label: 'Most tracks', order: 'desc' },
      { value: 'albums', label: 'Most albums', order: 'desc' },
    ],
  } as const
  const PAGE = 60

  type Item = Album | Artist
  let items = $state<Item[]>([])
  let total = $state(0)
  let page = $state(0)
  let input = $state('')
  let query = $state('')
  const firstSort = () => SORTS[kind][0].value as string
  let sort = $state<string>(firstSort())
  let loading = $state(false)
  let loaded = $state(false)
  let error = $state('')
  let seq = 0
  let controller: AbortController | undefined
  let debounce: ReturnType<typeof setTimeout> | undefined
  const title = $derived(kind === 'albums' ? 'Albums' : 'Artists')

  async function load(reset: boolean) {
    controller?.abort()
    controller = new AbortController()
    const mine = ++seq
    const next = reset ? 1 : page + 1
    const s = SORTS[kind].find((x) => x.value === sort) ?? SORTS[kind][0]
    const q = { page: next, limit: PAGE, q: query, sort: s.value, order: s.order }
    loading = true
    error = ''
    try {
      const res = kind === 'albums' ? await listAlbums(q, controller.signal) : await listArtists(q, controller.signal)
      if (mine !== seq) return
      items = reset ? res.items : [...items, ...res.items]
      total = res.pagination.total
      page = next
      loaded = true
    } catch (err) {
      if (mine !== seq || (err instanceof DOMException && err.name === 'AbortError')) return
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      else error = err instanceof Error ? sentence(err.message) : `Could not load ${title.toLowerCase()}.`
    } finally {
      if (mine === seq) loading = false
    }
  }

  // Load when first shown, and again after metadata edits.
  let lastVersion = -1
  $effect(() => {
    const v = listening.libraryVersion
    if (active && (!loaded || v !== lastVersion)) {
      lastVersion = v
      void load(true)
    }
  })

  function onInput() {
    clearTimeout(debounce)
    debounce = setTimeout(() => {
      if (input.trim() !== query) {
        query = input.trim()
        void load(true)
      }
    }, 300)
  }

  onDestroy(() => {
    clearTimeout(debounce)
    controller?.abort()
  })

  const isAlbum = (item: Item): item is Album => 'title' in item
  const id = $derived(`${kind}-search`)
</script>

<section class="view" aria-labelledby={`${kind}-title`}>
  <header class="view-header">
    <h1 id={`${kind}-title`} class="view-title" tabindex="-1">{title}</h1>
    {#if loaded}<p class="count" aria-live="polite">{total} {kind === 'albums' ? (total === 1 ? 'album' : 'albums') : total === 1 ? 'artist' : 'artists'}</p>{/if}
  </header>

  <div class="tools">
    <div class="rw-field">
      <label class="rw-label" for={id}>Search</label>
      <input {id} class="rw-input" type="search" maxlength="200" autocomplete="off" bind:value={input} oninput={onInput}
        placeholder={kind === 'albums' ? 'Album or artist' : 'Artist name'} />
    </div>
    <div class="rw-field">
      <label class="rw-label" for={`${kind}-sort`}>Sort by</label>
      <select id={`${kind}-sort`} class="rw-input" bind:value={sort} onchange={() => load(true)}>
        {#each SORTS[kind] as s (s.value)}<option value={s.value}>{s.label}</option>{/each}
      </select>
    </div>
  </div>

  {#if error}
    <div class="state">
      <p class="rw-alert" role="alert">{error}</p>
      <button class="rw-button rw-button-secondary" type="button" onclick={() => load(items.length === 0)}>Try again</button>
    </div>
  {:else if !loaded}
    <p class="state rw-muted" role="status">Loading {title.toLowerCase()}…</p>
  {:else if items.length === 0}
    <p class="state">{query ? `No ${title.toLowerCase()} match “${query}”.` : `No ${title.toLowerCase()} yet. Albums and artists come from your music's tags.`}</p>
  {:else}
    <ul class="grid" aria-busy={loading}>
      {#each items as item (item.id)}
        <li>
          <a class="card" href={isAlbum(item) ? albumHref(item.id) : artistHref(item.id)}>
            <Artwork trackId={item.artwork_track_id} kind={isAlbum(item) ? 'disc' : 'person'} shape={isAlbum(item) ? 'thumb' : 'round'} />
            <span class="name" title={isAlbum(item) ? item.title : item.name}>{isAlbum(item) ? item.title : item.name}</span>
            <span class="sub">
              {#if isAlbum(item)}
                {item.artist}{item.year ? ` · ${item.year}` : ''}
              {:else}
                {item.album_count} {item.album_count === 1 ? 'album' : 'albums'} · {item.track_count} {item.track_count === 1 ? 'track' : 'tracks'}
              {/if}
            </span>
            {#if isAlbum(item)}<span class="sub">{item.track_count} {item.track_count === 1 ? 'track' : 'tracks'} · {formatTime(item.duration_ms / 1000)}</span>{/if}
          </a>
        </li>
      {/each}
    </ul>
    {#if items.length < total}
      <div class="more">
        <button class="rw-button rw-button-secondary" type="button" onclick={() => load(false)} disabled={loading}>
          {loading ? 'Loading…' : `Load more (${total - items.length} left)`}
        </button>
      </div>
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

  select.rw-input {
    appearance: auto;
  }

  .state {
    display: grid;
    gap: var(--rw-space-3);
    justify-items: start;
    padding: var(--rw-space-5) 0;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 10.5rem), 1fr));
    gap: var(--rw-space-4);
    margin: 0;
    padding: 0;
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
    background: var(--rw-color-surface);
  }

  .name,
  .sub {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .name {
    margin-top: var(--rw-space-1);
    font-weight: var(--rw-font-weight-bold);
  }

  .sub {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .more {
    margin-top: var(--rw-space-5);
  }

  @media (max-width: 30rem) {
    .tools {
      grid-template-columns: minmax(0, 1fr);
    }

    .grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: var(--rw-space-2);
    }
  }
</style>
