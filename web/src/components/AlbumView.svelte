<script lang="ts">
  import { ApiError, editAlbum, getAlbum, type AlbumDetail, type MetadataValue } from '../services/api'
  import { formatTime } from '../services/format'
  import { listening } from '../services/listening.svelte'
  import { player } from '../services/player.svelte'
  import { albumHref, artistHref, navigate } from '../services/ui.svelte'
  import { sentence } from '../services/validation'
  import AddToPlaylist from './AddToPlaylist.svelte'
  import Artwork from './Artwork.svelte'
  import TrackList from './TrackList.svelte'

  interface Props {
    id: string
    active: boolean
    onUnauthorized: () => void
  }
  let { id, active, onUnauthorized }: Props = $props()

  let album = $state<AlbumDetail | null>(null)
  let missing = $state(false)
  let error = $state('')
  let adding = $state(false)
  let editing = $state(false)
  let saving = $state(false)
  let editError = $state('')
  let form = $state({ album: '', album_artist: '', year: '', genre: '' })
  let controller: AbortController | undefined
  let loadedKey = ''

  async function load() {
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    const want = id
    error = ''
    missing = false
    try {
      const a = await getAlbum(want, signal)
      if (!signal.aborted && want === id) album = a
    } catch (err) {
      if (signal.aborted) return
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      else if (err instanceof ApiError && err.status === 404) missing = true
      else error = err instanceof Error ? sentence(err.message) : 'Could not load the album.'
    }
  }

  // Reload when shown with another album, and after metadata edits.
  $effect(() => {
    const key = `${id}|${listening.libraryVersion}`
    if (active && key !== loadedKey) {
      if (!loadedKey.startsWith(`${id}|`)) {
        album = null
        editing = adding = false
      }
      loadedKey = key
      void load()
    }
  })

  const context = $derived(album ? { kind: 'album' as const, id: album.id, name: album.title } : null)
  const playable = $derived(album?.tracks.filter((t) => t.available) ?? [])
  const gaps = $derived.by(() => {
    if (!album) return [] as string[]
    const notes: string[] = []
    if (!album.year) notes.push('no year')
    if (!album.genre) notes.push('no genre')
    const unnumbered = album.tracks.filter((t) => !t.track_number).length
    if (unnumbered) notes.push(`${unnumbered} without a track number`)
    return notes
  })

  function startEdit() {
    if (!album) return
    form = { album: album.title, album_artist: album.tracks[0]?.album_artist ?? '', year: album.year ? String(album.year) : '', genre: album.genre ?? '' }
    editError = ''
    editing = true
  }

  async function saveEdit(event: SubmitEvent) {
    event.preventDefault()
    if (!album || saving) return
    const changes: Record<string, MetadataValue> = {}
    const title = form.album.trim()
    if (!title) {
      editError = 'The album title cannot be empty; edit single tracks to remove them from the album.'
      return
    }
    if (title !== album.title) changes.album = title
    const aa = form.album_artist.trim()
    if (aa !== (album.tracks[0]?.album_artist ?? '')) changes.album_artist = aa || null
    const year = form.year.trim()
    if (year && !/^\d{4}$/.test(year)) {
      editError = 'The year must have four digits.'
      return
    }
    if ((year ? Number(year) : undefined) !== album.year) changes.year = year ? Number(year) : null
    if (form.genre.trim() !== (album.genre ?? '')) changes.genre = form.genre.trim() || null
    if (Object.keys(changes).length === 0) {
      editing = false
      return
    }
    saving = true
    editError = ''
    try {
      const newId = await editAlbum(album.id, album.version, changes)
      editing = false
      listening.libraryChanged()
      listening.announce('Album details saved.')
      if (newId !== album.id) navigate(albumHref(newId))
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      else if (err instanceof ApiError && err.status === 409) {
        editError = 'The album changed meanwhile (an edit or a rescan). It was reloaded; check and save again.'
        void load()
      } else editError = err instanceof Error ? sentence(err.message) : 'Not saved.'
    } finally {
      saving = false
    }
  }
</script>

<section class="view" aria-labelledby="album-title">
  {#if missing}
    <h1 id="album-title" class="view-title" tabindex="-1">Album not found</h1>
    <p>This album is not in the library any more. Its tags may have changed. <a href="#/albums">All albums</a></p>
  {:else if error}
    <h1 id="album-title" class="view-title" tabindex="-1">Album</h1>
    <p class="rw-alert" role="alert">{error}</p>
    <button class="rw-button rw-button-secondary" type="button" onclick={load}>Try again</button>
  {:else if !album}
    <h1 id="album-title" class="view-title" tabindex="-1">Album</h1>
    <p class="rw-muted" role="status">Loading album…</p>
  {:else}
    <header class="hero">
      <span class="cover"><Artwork trackId={album.artwork_track_id} size="large" shape="large" kind="disc" eager /></span>
      <div class="facts">
        <p class="rw-label">Album</p>
        <h1 id="album-title" class="view-title" tabindex="-1">{album.title}</h1>
        <p class="by">
          {#if album.artists.length > 0}
            {#each album.artists as a, i (a.id)}{#if i > 0}, {/if}<a href={artistHref(a.id)}>{a.name}</a>{/each}
          {:else}Unknown artist{/if}
        </p>
        <p class="meta">
          {[album.year, album.genre, `${album.track_count} ${album.track_count === 1 ? 'track' : 'tracks'}`,
            album.disc_count > 1 ? `${album.disc_count} discs` : '', formatTime(album.duration_ms / 1000)].filter(Boolean).join(' · ')}
        </p>
        {#if album.unavailable_count > 0}<p class="note">{album.unavailable_count} {album.unavailable_count === 1 ? 'track is' : 'tracks are'} unavailable.</p>{/if}
        {#if gaps.length > 0}<p class="note">Missing details: {gaps.join(', ')}.</p>{/if}
        <p class="rw-hint folder">Folder: {album.folders.join(', ')}</p>

        <div class="actions">
          <button class="rw-button" type="button" disabled={playable.length === 0} onclick={() => player.playTracks(album!.tracks, 0, context)}>Play</button>
          <button class="rw-button rw-button-secondary" type="button" disabled={playable.length === 0}
            onclick={() => player.playTracks(album!.tracks, -1, context, { shuffle: true })}>Shuffle</button>
          <button class="rw-button rw-button-secondary" type="button" disabled={playable.length === 0} onclick={() => player.playNext(playable)}>Play next</button>
          <button class="rw-button rw-button-secondary" type="button" disabled={playable.length === 0} onclick={() => player.addToQueue(playable)}>Add to queue</button>
          <button class="rw-button rw-button-secondary" type="button" aria-expanded={adding} onclick={() => (adding = !adding)}>Add to playlist…</button>
          <button class="rw-button rw-button-secondary" type="button" aria-expanded={editing} onclick={() => (editing ? (editing = false) : startEdit())}>Edit album…</button>
        </div>
      </div>
    </header>

    {#if adding}
      <div class="panel">
        <AddToPlaylist
          track={album.tracks[0]}
          tracks={album.tracks}
          name={album.title}
          onDone={(m) => {
            listening.announce(m)
            adding = false
          }}
          onCancel={() => (adding = false)}
          {onUnauthorized}
        />
      </div>
    {/if}

    {#if editing}
      <form class="panel edit" onsubmit={saveEdit}>
        <p class="rw-hint">Changes apply to all {album.track_count} tracks of this album, in Rewind's library only. Your music files are not modified.</p>
        <div class="fields">
          <div class="rw-field"><label class="rw-label" for="album-edit-title">Album</label><input id="album-edit-title" class="rw-input" maxlength="512" bind:value={form.album} /></div>
          <div class="rw-field"><label class="rw-label" for="album-edit-artist">Album artist</label><input id="album-edit-artist" class="rw-input" maxlength="512" bind:value={form.album_artist} placeholder="For compilations, e.g. Various Artists" /></div>
          <div class="rw-field"><label class="rw-label" for="album-edit-year">Year</label><input id="album-edit-year" class="rw-input" inputmode="numeric" maxlength="4" bind:value={form.year} /></div>
          <div class="rw-field"><label class="rw-label" for="album-edit-genre">Genre</label><input id="album-edit-genre" class="rw-input" maxlength="512" bind:value={form.genre} /></div>
        </div>
        {#if editError}<p class="rw-alert" role="alert">{editError}</p>{/if}
        <div class="actions">
          <button class="rw-button" type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
          <button class="rw-button rw-button-secondary" type="button" onclick={() => (editing = false)}>Cancel</button>
        </div>
      </form>
    {/if}

    <TrackList tracks={album.tracks} label="Album tracks" {context} {onUnauthorized} numbering="track" discs showAlbum={false} hideAlbumLink />
  {/if}
</section>

<style>
  .hero {
    display: grid;
    grid-template-columns: minmax(0, 15rem) minmax(0, 1fr);
    gap: var(--rw-space-5);
    align-items: end;
    margin-bottom: var(--rw-space-5);
  }

  .facts {
    display: grid;
    gap: var(--rw-space-2);
    min-width: 0;
  }

  .facts .view-title {
    overflow-wrap: anywhere;
  }

  .by {
    font-size: var(--rw-font-size-lg);
    font-weight: var(--rw-font-weight-bold);
  }

  .by a {
    color: inherit;
  }

  .meta,
  .note {
    color: var(--rw-color-muted);
  }

  .note {
    border-left: var(--rw-border-width) solid var(--rw-color-accent);
    padding-left: var(--rw-space-2);
    font-size: var(--rw-font-size-sm);
  }

  .folder {
    overflow-wrap: anywhere;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
    margin-top: var(--rw-space-2);
  }

  .panel {
    display: grid;
    gap: var(--rw-space-3);
    margin-bottom: var(--rw-space-4);
    padding: var(--rw-space-3);
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-surface);
  }

  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 14rem), 1fr));
    gap: var(--rw-space-3);
  }

  @media (max-width: 40rem) {
    .hero {
      grid-template-columns: minmax(0, 1fr);
    }

    .cover {
      width: min(100%, 16rem);
    }
  }
</style>
