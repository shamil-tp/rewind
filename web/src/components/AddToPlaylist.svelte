<script lang="ts">
  import { onMount, tick } from 'svelte'
  import { addToPlaylist, addTracksToPlaylist, ApiError, listPlaylists, type Playlist, type Track } from '../services/api'
  import { sentence } from '../services/validation'

  interface Props {
    track: Track
    /** Several tracks to add at once (album, artist, playlist, selection); track names the group. */
    tracks?: Track[]
    /** A playlist not to offer (the one the tracks come from). */
    exclude?: number
    /** Names the group of tracks in labels, e.g. "Album title". */
    name?: string
    /** Called with a confirmation message after the track was added. */
    onDone: (message: string) => void
    onCancel: () => void
    onUnauthorized: () => void
  }
  let { track, tracks, exclude, name, onDone, onCancel, onUnauthorized }: Props = $props()

  // Unique per panel, so several panels never share an input ID.
  const uid = `${Math.random().toString(36).slice(2, 9)}`
  let playlists = $state<Playlist[] | null>(null)
  let choice = $state<number | null>(null)
  let loadError = $state('')
  let error = $state('')
  let busy = $state(false)
  let select: HTMLSelectElement | undefined = $state()
  let root: HTMLElement | undefined = $state()

  function message(err: unknown, fallback: string): string {
    return err instanceof Error ? sentence(err.message) : fallback
  }

  async function load() {
    loadError = ''
    playlists = null
    try {
      playlists = (await listPlaylists()).filter((p) => p.id !== exclude)
      choice = playlists[0]?.id ?? null
      await tick()
      ;(select ?? root?.querySelector<HTMLElement>('a, button'))?.focus()
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        onUnauthorized()
        return
      }
      loadError = message(err, 'Could not load your playlists.')
    }
  }

  async function add(event: SubmitEvent) {
    event.preventDefault()
    const target = playlists?.find((p) => p.id === choice)
    if (!target || busy) return
    busy = true
    error = ''
    try {
      if (tracks) {
        const res = await addTracksToPlaylist(target.id, tracks.map((t) => t.id))
        const already = res.skipped > 0 ? ` (${res.skipped} already there)` : ''
        onDone(`Added ${res.added} ${res.added === 1 ? 'track' : 'tracks'} to “${target.name}”${already}.`)
      } else {
        await addToPlaylist(target.id, track.id)
        onDone(`Added “${track.title}” to “${target.name}”.`)
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        onUnauthorized()
        return
      }
      error = message(err, 'Could not add the track.')
    } finally {
      busy = false
    }
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.stopPropagation()
      onCancel()
    }
  }

  onMount(() => {
    void load()
  })
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions (Escape closes the panel) -->
<div class="panel" role="group" aria-label={`Add ${name ?? track.title} to a playlist`} bind:this={root} onkeydown={onKeydown}>
  {#if loadError}
    <p class="rw-alert" role="alert">{loadError}</p>
    <div class="actions">
      <button class="rw-button rw-button-secondary" type="button" onclick={load}>Try again</button>
      <button class="rw-button rw-button-secondary" type="button" onclick={onCancel}>Cancel</button>
    </div>
  {:else if playlists === null}
    <p class="rw-muted" role="status">Loading playlists…</p>
  {:else if playlists.length === 0}
    <p>You have no playlists yet. <a href="#/playlists">Create one in Playlists</a>, then add tracks here.</p>
    <div class="actions">
      <button class="rw-button rw-button-secondary" type="button" onclick={onCancel}>Close</button>
    </div>
  {:else}
    <form class="form" onsubmit={add}>
      <label class="rw-label" for={`add-${uid}`}>Add to playlist</label>
      <div class="actions">
        <select id={`add-${uid}`} class="rw-input" bind:value={choice} bind:this={select} disabled={busy}>
          {#each playlists as p (p.id)}
            <option value={p.id}>{p.name} ({p.track_count})</option>
          {/each}
        </select>
        <button class="rw-button" type="submit" disabled={busy || choice === null}>{busy ? 'Adding…' : 'Add'}</button>
        <button class="rw-button rw-button-secondary" type="button" onclick={onCancel}>Cancel</button>
      </div>
      {#if error}<p class="rw-alert" role="alert">{error}</p>{/if}
    </form>
  {/if}
</div>

<style>
  .panel,
  .form {
    display: grid;
    gap: var(--rw-space-2);
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
  }

  select.rw-input {
    flex: 1 1 12rem;
    width: auto;
    min-width: 0;
    appearance: auto;
  }
</style>
