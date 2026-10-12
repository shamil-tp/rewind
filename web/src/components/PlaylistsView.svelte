<script lang="ts">
  import { onDestroy, untrack } from 'svelte'
  import { ApiError, createPlaylist, listPlaylists, type Playlist, type PlaylistDetail as Detail } from '../services/api'
  import { formatTime } from '../services/format'
  import { sentence } from '../services/validation'
  import PlaylistDetail from './PlaylistDetail.svelte'

  interface Props {
    /** The playlist to show (from the URL), or null for the list. */
    playlistId: number | null
    /** Whether this view is on screen; data is refreshed each time it is shown. */
    active: boolean
    currentId: number | null
    playing: boolean
    onPlay: (playlist: Detail, index: number) => void
    onUnauthorized: () => void
  }
  let { playlistId, active, currentId, playing, onPlay, onUnauthorized }: Props = $props()

  const maxName = 100

  let playlists = $state<Playlist[] | null>(null)
  let loadError = $state('')
  let name = $state('')
  let nameError = $state('')
  let createError = $state('')
  let creating = $state(false)
  let notice = $state('')
  let controller: AbortController | undefined

  function failed(err: unknown, fallback: string): string | undefined {
    if (err instanceof ApiError && err.status === 401) {
      onUnauthorized()
      return undefined
    }
    return err instanceof Error ? sentence(err.message) : fallback
  }

  async function load() {
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    loadError = ''
    try {
      const items = await listPlaylists(signal)
      if (!signal.aborted) playlists = items
    } catch (err) {
      if (signal.aborted) return
      loadError = failed(err, 'Could not load playlists.') ?? ''
    }
  }

  $effect(() => {
    if (active && playlistId === null) untrack(() => void load())
  })

  onDestroy(() => controller?.abort())

  async function create(event: SubmitEvent) {
    event.preventDefault()
    notice = ''
    createError = ''
    const trimmed = name.trim()
    nameError = trimmed === '' ? 'Enter a name.' : [...trimmed].length > maxName ? `Use at most ${maxName} characters.` : ''
    if (nameError || creating) return
    creating = true
    try {
      const created = await createPlaylist(trimmed)
      name = ''
      notice = `Created “${created.name}”.`
      await load()
    } catch (err) {
      createError = failed(err, 'Could not create the playlist.') ?? ''
    } finally {
      creating = false
    }
  }

  function summary(p: Playlist): string {
    const parts = [`${p.track_count} ${p.track_count === 1 ? 'track' : 'tracks'}`]
    if (p.duration_ms > 0) parts.push(formatTime(p.duration_ms / 1000))
    if (p.unavailable_count > 0) parts.push(`${p.unavailable_count} unavailable`)
    return parts.join(' · ')
  }
</script>

{#if playlistId !== null}
  {#key playlistId}
    <PlaylistDetail id={playlistId} {active} {currentId} {playing} {onPlay} {onUnauthorized} />
  {/key}
{:else}
  <section class="view" aria-labelledby="playlists-title">
    <header class="view-header">
      <h1 id="playlists-title" class="view-title" tabindex="-1">Playlists</h1>
      {#if playlists}<p class="count">{playlists.length} {playlists.length === 1 ? 'playlist' : 'playlists'}</p>{/if}
    </header>

    <form class="create" onsubmit={create} novalidate>
      <label class="rw-label" for="new-playlist">New playlist</label>
      <div class="create-row">
        <input
          id="new-playlist"
          class="rw-input"
          type="text"
          maxlength={maxName}
          autocomplete="off"
          placeholder="Name"
          bind:value={name}
          aria-invalid={nameError ? 'true' : undefined}
          aria-describedby={nameError ? 'new-playlist-error' : undefined}
        />
        <button class="rw-button" type="submit" disabled={creating}>{creating ? 'Creating…' : 'Create'}</button>
      </div>
      {#if nameError}<p id="new-playlist-error" class="rw-field-error">{nameError}</p>{/if}
      {#if createError}<p class="rw-alert" role="alert">{createError}</p>{/if}
    </form>
    <p class="notice" role="status">{notice}</p>

    {#if loadError}
      <div class="state">
        <p class="rw-alert" role="alert">{loadError}</p>
        <button class="rw-button rw-button-secondary" type="button" onclick={load}>Try again</button>
      </div>
    {:else if playlists === null}
      <p class="state rw-muted" role="status">Loading playlists…</p>
    {:else if playlists.length === 0}
      <div class="state">
        <p>No playlists yet.</p>
        <p class="rw-muted">Create one above, then add tracks from the Library with the <strong>+</strong> button.</p>
      </div>
    {:else}
      <ul class="list" aria-label="Your playlists">
        {#each playlists as p (p.id)}
          <li>
            <a class="item" href={`#/playlists/${p.id}`}>
              <span class="name">{p.name}</span>
              <span class="meta">{summary(p)}</span>
            </a>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{/if}

<style>
  .create {
    display: grid;
    gap: var(--rw-space-1);
    max-width: 36rem;
  }

  .create-row {
    display: flex;
    gap: var(--rw-space-2);
  }

  .notice {
    min-height: 1.25em;
    margin: var(--rw-space-2) 0 var(--rw-space-4);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  .state {
    display: grid;
    gap: var(--rw-space-3);
    justify-items: start;
    padding: var(--rw-space-5) 0;
  }

  .list {
    margin: 0;
    padding: 0;
    border-top: var(--rw-border);
    list-style: none;
  }

  .item {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--rw-space-1) var(--rw-space-4);
    min-height: 3rem;
    padding: var(--rw-space-3);
    border-bottom: 1px solid var(--rw-color-surface);
    border-left: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-sm);
    color: var(--rw-color-text);
    text-decoration: none;
  }

  .item:hover {
    border-left-color: var(--rw-color-accent);
    background: var(--rw-color-surface);
  }

  .item:focus-visible {
    outline-offset: -3px;
  }

  .name {
    min-width: 0;
    font-weight: var(--rw-font-weight-bold);
    overflow-wrap: anywhere;
  }

  .meta {
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }
</style>
