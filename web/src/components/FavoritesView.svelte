<script lang="ts">
  import { ApiError, listFavorites, type Track } from '../services/api'
  import { listening } from '../services/listening.svelte'
  import { player } from '../services/player.svelte'
  import { sentence } from '../services/validation'
  import TrackList from './TrackList.svelte'

  interface Props {
    active: boolean
    onUnauthorized: () => void
  }
  let { active, onUnauthorized }: Props = $props()

  const PAGE = 100
  let tracks = $state<Track[]>([])
  let total = $state(0)
  let page = $state(0)
  let loaded = $state(false)
  let loading = $state(false)
  let error = $state('')
  let seq = 0

  async function load(reset: boolean) {
    const mine = ++seq
    const next = reset ? 1 : page + 1
    loading = true
    error = ''
    try {
      const res = await listFavorites({ page: next, limit: PAGE })
      if (mine !== seq) return
      tracks = reset ? res.items : [...tracks, ...res.items]
      total = res.pagination.total
      page = next
      loaded = true
    } catch (err) {
      if (mine !== seq) return
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      else error = err instanceof Error ? sentence(err.message) : 'Could not load your favorites.'
    } finally {
      if (mine === seq) loading = false
    }
  }

  // Reload each time the page is shown (favorites change from anywhere).
  let wasActive = false
  $effect(() => {
    void listening.libraryVersion
    if (active && !wasActive) void load(true)
    wasActive = active
  })

  // Tracks unfavorited on this page stay listed until the next visit, so a
  // misclick can be undone.
  const context = { kind: 'favorites' as const, name: 'Favorites' }
  const playable = $derived(tracks.filter((t) => t.available))
</script>

<section class="view" aria-labelledby="favorites-title">
  <header class="view-header">
    <h1 id="favorites-title" class="view-title" tabindex="-1">Favorites</h1>
    {#if loaded}<p class="count">{total} {total === 1 ? 'track' : 'tracks'}</p>{/if}
  </header>
  {#if error}
    <p class="rw-alert" role="alert">{error}</p>
    <button class="rw-button rw-button-secondary" type="button" onclick={() => load(true)}>Try again</button>
  {:else if !loaded}
    <p class="rw-muted" role="status">Loading favorites…</p>
  {:else if tracks.length === 0}
    <p>No favorites yet. Use the heart on any track to add it here.</p>
  {:else}
    <div class="actions">
      <button class="rw-button" type="button" disabled={playable.length === 0} onclick={() => player.playTracks(tracks, 0, context)}>Play all</button>
      <button class="rw-button rw-button-secondary" type="button" disabled={playable.length === 0}
        onclick={() => player.playTracks(tracks, -1, context, { shuffle: true })}>Shuffle</button>
    </div>
    <TrackList {tracks} label="Favorite tracks" {context} {onUnauthorized} showArtwork />
    {#if tracks.length < total}
      <button class="rw-button rw-button-secondary more" type="button" onclick={() => load(false)} disabled={loading}>Load more</button>
    {/if}
  {/if}
</section>

<style>
  .actions {
    display: flex;
    gap: var(--rw-space-2);
    margin-bottom: var(--rw-space-4);
  }

  .more {
    margin-top: var(--rw-space-4);
  }
</style>
