<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte'
  import {
    ApiError,
    deletePlaylist,
    getPlaylist,
    removeFromPlaylist,
    reorderPlaylist,
    updatePlaylist,
    type PlaylistDetail,
    type PlaylistEntry,
  } from '../services/api'
  import { formatTime } from '../services/format'
  import { nextPlayable } from '../services/sequence'
  import { sentence } from '../services/validation'

  interface Props {
    id: number
    /** Whether the view is on screen; the playlist is reloaded when shown. */
    active: boolean
    currentId: number | null
    playing: boolean
    onPlay: (playlist: PlaylistDetail, index: number) => void
    onUnauthorized: () => void
  }
  let { id, active, currentId, playing, onPlay, onUnauthorized }: Props = $props()

  let detail = $state<PlaylistDetail | null>(null)
  let loadError = $state('')
  let missing = $state(false)
  let busy = $state(false)
  let error = $state('')
  let notice = $state('')
  let editing = $state(false)
  let editName = $state('')
  let editDescription = $state('')
  let confirmingDelete = $state(false)
  let controller: AbortController | undefined

  /** Reports err: signs out on 401, shows "not found" on 404. Returns a message or ''. */
  function failed(err: unknown, fallback: string): string {
    if (err instanceof ApiError && err.status === 401) {
      onUnauthorized()
      return ''
    }
    if (err instanceof ApiError && err.status === 404 && err.code === 'not_found') {
      missing = true
      return ''
    }
    return err instanceof Error ? sentence(err.message) : fallback
  }

  async function load() {
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    loadError = ''
    try {
      const d = await getPlaylist(id, signal)
      if (!signal.aborted) detail = d
    } catch (err) {
      if (signal.aborted) return
      loadError = failed(err, 'Could not load the playlist.')
    }
  }

  $effect(() => {
    if (active) untrack(() => void load())
  })

  onDestroy(() => controller?.abort())

  /** Runs one change at a time, reporting failures in the action area. */
  async function act(fallback: string, fn: () => Promise<void>) {
    if (busy) return
    busy = true
    error = ''
    notice = ''
    try {
      await fn()
    } catch (err) {
      if (err instanceof ApiError && err.code === 'order_mismatch') {
        await load()
        error = 'The playlist changed elsewhere and was reloaded. Try again.'
      } else {
        error = failed(err, fallback)
      }
    } finally {
      busy = false
    }
  }

  const firstPlayable = $derived(detail ? nextPlayable(detail.entries.map((e) => e.track), -1, 1) : -1)

  function play(index: number) {
    if (detail && detail.entries[index]?.track.available) onPlay(detail, index)
  }

  function move(index: number, by: -1 | 1) {
    const d = detail
    if (!d) return
    const target = index + by
    if (target < 0 || target >= d.entries.length) return
    const ids = d.entries.map((e) => e.track.id)
    ;[ids[index], ids[target]] = [ids[target], ids[index]]
    const title = d.entries[index].track.title
    void act('Could not reorder the playlist.', async () => {
      detail = await reorderPlaylist(id, ids)
      notice = `Moved “${title}” to position ${target + 1}.`
    })
  }

  function remove(entry: PlaylistEntry) {
    void act('Could not remove the track.', async () => {
      await removeFromPlaylist(id, entry.track.id)
      await load()
      notice = `Removed “${entry.track.title}”. It is still in your library.`
    })
  }

  async function startEdit() {
    if (!detail) return
    editName = detail.name
    editDescription = detail.description ?? ''
    confirmingDelete = false
    editing = true
    await tick()
    document.getElementById('playlist-name')?.focus()
  }

  function saveEdit(event: SubmitEvent) {
    event.preventDefault()
    const name = editName.trim()
    if (name === '') {
      error = 'Enter a name.'
      return
    }
    void act('Could not save the playlist.', async () => {
      const updated = await updatePlaylist(id, { name, description: editDescription.trim() })
      if (detail) detail = { ...detail, ...updated, description: updated.description }
      editing = false
      notice = 'Saved.'
    })
  }

  function confirmDelete() {
    void act('Could not delete the playlist.', async () => {
      try {
        await deletePlaylist(id)
      } catch (err) {
        // Already gone: the outcome the user asked for.
        if (!(err instanceof ApiError && err.status === 404)) throw err
      }
      location.hash = '#/playlists'
    })
  }

  function entryLabel(e: PlaylistEntry): string {
    const parts = [`Play ${e.track.title}`]
    if (e.track.artist) parts.push(`by ${e.track.artist}`)
    if (!e.track.available) parts.push('(unavailable)')
    return parts.join(' ')
  }

  const meta = $derived.by(() => {
    if (!detail) return ''
    const parts = [`${detail.track_count} ${detail.track_count === 1 ? 'track' : 'tracks'}`]
    if (detail.duration_ms > 0) parts.push(formatTime(detail.duration_ms / 1000))
    if (detail.unavailable_count > 0) parts.push(`${detail.unavailable_count} unavailable`)
    return parts.join(' · ')
  })
</script>

<section class="view" aria-labelledby="playlist-title">
  <p class="back"><a href="#/playlists">← Playlists</a></p>

  {#if missing}
    <h1 id="playlist-title" class="view-title" tabindex="-1">Not found</h1>
    <p class="state">This playlist does not exist. It may have been deleted.</p>
  {:else if loadError && !detail}
    <h1 id="playlist-title" class="view-title" tabindex="-1">Playlist</h1>
    <div class="state">
      <p class="rw-alert" role="alert">{loadError}</p>
      <button class="rw-button rw-button-secondary" type="button" onclick={load}>Try again</button>
    </div>
  {:else if !detail}
    <h1 id="playlist-title" class="view-title" tabindex="-1">Playlist</h1>
    <p class="state rw-muted" role="status">Loading playlist…</p>
  {:else}
    <header class="view-header">
      <h1 id="playlist-title" class="view-title name" tabindex="-1">{detail.name}</h1>
      <p class="count">{meta}</p>
    </header>
    {#if detail.description}<p class="description">{detail.description}</p>{/if}

    <div class="actions">
      <button class="rw-button" type="button" onclick={() => play(firstPlayable)} disabled={firstPlayable < 0}>Play</button>
      <button class="rw-button rw-button-secondary" type="button" onclick={startEdit} disabled={busy || editing}>Rename</button>
      <button
        class="rw-button rw-button-secondary"
        type="button"
        onclick={() => {
          editing = false
          confirmingDelete = true
        }}
        disabled={busy || confirmingDelete}>Delete</button
      >
    </div>

    {#if editing}
      <form class="edit" onsubmit={saveEdit}>
        <div class="rw-field">
          <label class="rw-label" for="playlist-name">Name</label>
          <input id="playlist-name" class="rw-input" type="text" maxlength="100" autocomplete="off" bind:value={editName} />
        </div>
        <div class="rw-field">
          <label class="rw-label" for="playlist-description">Description <span class="rw-hint">(optional)</span></label>
          <textarea id="playlist-description" class="rw-input" rows="2" maxlength="500" bind:value={editDescription}></textarea>
        </div>
        <div class="actions">
          <button class="rw-button" type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button>
          <button class="rw-button rw-button-secondary" type="button" onclick={() => (editing = false)} disabled={busy}>Cancel</button>
        </div>
      </form>
    {/if}

    {#if confirmingDelete}
      <div class="confirm" role="group" aria-label="Confirm deletion">
        <p>Delete “{detail.name}”? The tracks stay in your library; only the playlist is removed.</p>
        <div class="actions">
          <button class="rw-button danger" type="button" onclick={confirmDelete} disabled={busy}>
            {busy ? 'Deleting…' : 'Delete playlist'}
          </button>
          <button class="rw-button rw-button-secondary" type="button" onclick={() => (confirmingDelete = false)} disabled={busy}>
            Cancel
          </button>
        </div>
      </div>
    {/if}

    {#if error}<p class="rw-alert" role="alert">{error}</p>{/if}
    <p class="notice" role="status">{notice}</p>

    {#if detail.entries.length === 0}
      <div class="state">
        <p>This playlist is empty.</p>
        <p class="rw-muted">Add tracks from the <a href="#/library">Library</a> with the <strong>+</strong> button.</p>
      </div>
    {:else}
      <ol class="entries" aria-label="Tracks" aria-busy={busy}>
        {#each detail.entries as entry, i (entry.track.id)}
          {@const isCurrent = entry.track.id === currentId}
          <li class="entry" class:current={isCurrent}>
            <span class="pos" aria-hidden="true">{entry.position}</span>
            <button
              class="row"
              type="button"
              onclick={() => play(i)}
              disabled={!entry.track.available}
              aria-current={isCurrent ? 'true' : undefined}
              aria-label={entryLabel(entry)}
            >
              <span class="title">
                {#if isCurrent}<span class="marker">{playing ? 'Playing' : 'Paused'}</span>{/if}
                <span class="title-text">{entry.track.title}</span>
              </span>
              <span class="artist">{entry.track.artist ?? '—'}</span>
            </button>
            <span class="len">
              {#if !entry.track.available}<span class="badge">Unavailable</span>{/if}
              {formatTime(entry.track.duration_ms ? entry.track.duration_ms / 1000 : undefined)}
            </span>
            <span class="tools">
              <button type="button" onclick={() => move(i, -1)} disabled={busy || i === 0} aria-label={`Move ${entry.track.title} up`}>↑</button>
              <button
                type="button"
                onclick={() => move(i, 1)}
                disabled={busy || i === detail.entries.length - 1}
                aria-label={`Move ${entry.track.title} down`}>↓</button
              >
              <button type="button" onclick={() => remove(entry)} disabled={busy} aria-label={`Remove ${entry.track.title} from the playlist`}
                >✕</button
              >
            </span>
          </li>
        {/each}
      </ol>
    {/if}
  {/if}
</section>

<style>
  .back {
    margin-bottom: var(--rw-space-3);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  .name {
    overflow-wrap: anywhere;
  }

  .description {
    max-width: 40rem;
    margin-top: calc(-1 * var(--rw-space-3));
    margin-bottom: var(--rw-space-4);
    color: var(--rw-color-muted);
    white-space: pre-line;
    overflow-wrap: anywhere;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
  }

  .edit,
  .confirm {
    display: grid;
    gap: var(--rw-space-3);
    max-width: 36rem;
    margin-top: var(--rw-space-4);
    padding: var(--rw-space-4);
    border: var(--rw-border);
  }

  .confirm {
    border-color: var(--rw-color-danger);
  }

  .danger {
    background: var(--rw-color-danger);
  }

  textarea.rw-input {
    resize: vertical;
  }

  .rw-alert {
    margin-top: var(--rw-space-3);
  }

  .notice {
    min-height: 1.25em;
    margin: var(--rw-space-3) 0;
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

  .entries {
    margin: 0;
    padding: 0;
    border-top: var(--rw-border);
    list-style: none;
  }

  .entry {
    display: grid;
    grid-template-columns: 2.5rem minmax(0, 1fr) auto auto;
    align-items: center;
    gap: var(--rw-space-2);
    border-bottom: 1px solid var(--rw-color-surface);
    border-left: var(--rw-border-width) solid transparent;
  }

  .entry.current {
    border-left-color: var(--rw-color-accent);
    background: var(--rw-color-surface);
  }

  .pos {
    padding-left: var(--rw-space-2);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    font-variant-numeric: tabular-nums;
  }

  .row {
    display: grid;
    min-width: 0;
    min-height: 3rem;
    padding: var(--rw-space-2) 0;
    border: 0;
    background: transparent;
    color: var(--rw-color-text);
    text-align: left;
    cursor: pointer;
  }

  .row:disabled {
    color: var(--rw-color-muted);
    cursor: not-allowed;
  }

  .row:focus-visible {
    outline-offset: -3px;
  }

  .title {
    display: flex;
    align-items: center;
    gap: var(--rw-space-2);
    min-width: 0;
    font-weight: var(--rw-font-weight-bold);
  }

  .title-text,
  .artist {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .artist {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .marker {
    flex: none;
    padding: 0 var(--rw-space-1);
    background: var(--rw-color-accent);
    color: var(--rw-color-on-accent);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .len {
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    font-variant-numeric: tabular-nums;
    text-align: right;
  }

  .badge {
    margin-right: var(--rw-space-2);
    padding: 0 var(--rw-space-1);
    border: 1px solid var(--rw-color-danger);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .tools {
    display: flex;
  }

  /* 44px touch targets. */
  .tools button {
    min-width: 2.75rem;
    min-height: 2.75rem;
    border: 0;
    background: transparent;
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    cursor: pointer;
  }

  .tools button:hover:not(:disabled) {
    color: var(--rw-color-accent);
  }

  .tools button:disabled {
    opacity: 0.35;
    cursor: default;
  }

  @media (max-width: 48rem) {
    .entry {
      grid-template-columns: 2rem minmax(0, 1fr) auto;
      grid-template-areas: 'pos row len' 'pos tools tools';
      row-gap: 0;
    }

    .pos {
      grid-area: pos;
    }

    .row {
      grid-area: row;
    }

    .len {
      grid-area: len;
      padding-right: var(--rw-space-2);
    }

    .tools {
      grid-area: tools;
      justify-content: flex-end;
    }
  }
</style>
