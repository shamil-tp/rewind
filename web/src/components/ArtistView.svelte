<script lang="ts">
  import { ApiError, getArtist, renameArtist, type Album, type ArtistPage } from '../services/api'
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

  let page = $state<ArtistPage | null>(null)
  let missing = $state(false)
  let error = $state('')
  let adding = $state(false)
  let renaming = $state(false)
  let newName = $state('')
  let renameError = $state('')
  let saving = $state(false)
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
      const p = await getArtist(want, signal)
      if (!signal.aborted && want === id) page = p
    } catch (err) {
      if (signal.aborted) return
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      else if (err instanceof ApiError && err.status === 404) missing = true
      else error = err instanceof Error ? sentence(err.message) : 'Could not load the artist.'
    }
  }

  // Reload when shown with another artist, after edits, and after new plays
  // (top and recent tracks).
  $effect(() => {
    const key = `${id}|${listening.libraryVersion}|${listening.historyVersion}`
    if (active && key !== loadedKey) {
      if (!loadedKey.startsWith(`${id}|`)) {
        page = null
        adding = renaming = false
      }
      loadedKey = key
      void load()
    }
  })

  const artist = $derived(page?.artist)
  const context = $derived(artist ? { kind: 'artist' as const, id: artist.id, name: artist.name } : null)
  const playable = $derived(artist?.tracks.filter((t) => t.available) ?? [])
  const byYear = $derived.by(() => {
    const groups = new Map<string, Album[]>()
    for (const a of artist?.albums ?? []) {
      const k = a.year ? String(a.year) : 'Unknown year'
      groups.set(k, [...(groups.get(k) ?? []), a])
    }
    return [...groups.entries()]
  })

  async function rename(event: SubmitEvent) {
    event.preventDefault()
    if (!artist || saving) return
    const name = newName.trim()
    if (!name || name === artist.name) {
      renaming = false
      return
    }
    saving = true
    renameError = ''
    try {
      const newId = await renameArtist(artist.id, artist.version, name)
      renaming = false
      listening.libraryChanged()
      listening.announce(`Renamed to “${name}”.`)
      if (newId !== artist.id) navigate(artistHref(newId))
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      else if (err instanceof ApiError && err.status === 409) {
        renameError = 'The artist changed meanwhile. It was reloaded; try again.'
        void load()
      } else renameError = err instanceof Error ? sentence(err.message) : 'Not saved.'
    } finally {
      saving = false
    }
  }
</script>

{#snippet albumGrid(albums: Album[], label: string)}
  <ul class="grid" aria-label={label}>
    {#each albums as a (a.id)}
      <li>
        <a class="card" href={albumHref(a.id)}>
          <Artwork trackId={a.artwork_track_id} kind="disc" />
          <span class="name" title={a.title}>{a.title}</span>
          <span class="sub">{[a.year, `${a.track_count} ${a.track_count === 1 ? 'track' : 'tracks'}`].filter(Boolean).join(' · ')}</span>
        </a>
      </li>
    {/each}
  </ul>
{/snippet}

<section class="view" aria-labelledby="artist-title">
  {#if missing}
    <h1 id="artist-title" class="view-title" tabindex="-1">Artist not found</h1>
    <p>No tracks credit this artist any more. <a href="#/artists">All artists</a></p>
  {:else if error}
    <h1 id="artist-title" class="view-title" tabindex="-1">Artist</h1>
    <p class="rw-alert" role="alert">{error}</p>
    <button class="rw-button rw-button-secondary" type="button" onclick={load}>Try again</button>
  {:else if !page || !artist}
    <h1 id="artist-title" class="view-title" tabindex="-1">Artist</h1>
    <p class="rw-muted" role="status">Loading artist…</p>
  {:else}
    <header class="hero">
      <span class="cover"><Artwork trackId={artist.artwork_track_id} size="large" shape="round" kind="person" eager /></span>
      <div class="facts">
        <p class="rw-label">Artist</p>
        <h1 id="artist-title" class="view-title" tabindex="-1">{artist.name}</h1>
        <p class="meta">
          {artist.album_count} {artist.album_count === 1 ? 'album' : 'albums'} · {artist.track_count} {artist.track_count === 1 ? 'track' : 'tracks'} ·
          {formatTime(artist.duration_ms / 1000)}{page.listening_ms > 0 ? ` · you listened ${formatTime(page.listening_ms / 1000)}` : ''}
        </p>
        <div class="actions">
          <button class="rw-button" type="button" disabled={playable.length === 0} onclick={() => player.playTracks(artist.tracks, 0, context)}>Play all</button>
          <button class="rw-button rw-button-secondary" type="button" disabled={playable.length === 0}
            onclick={() => player.playTracks(artist.tracks, -1, context, { shuffle: true })}>Shuffle</button>
          <button class="rw-button rw-button-secondary" type="button" disabled={playable.length === 0} onclick={() => player.addToQueue(playable)}>Add to queue</button>
          <button class="rw-button rw-button-secondary" type="button" aria-expanded={adding} onclick={() => (adding = !adding)}>Add to playlist…</button>
          {#if artist.id !== '~'}
            <button class="rw-button rw-button-secondary" type="button" aria-expanded={renaming}
              onclick={() => {
                newName = artist.name
                renameError = ''
                renaming = !renaming
              }}>Rename…</button>
          {/if}
        </div>
      </div>
    </header>

    {#if adding}
      <div class="panel">
        <AddToPlaylist track={artist.tracks[0]} tracks={artist.tracks} name={artist.name}
          onDone={(m) => {
            listening.announce(m)
            adding = false
          }}
          onCancel={() => (adding = false)} {onUnauthorized} />
      </div>
    {/if}
    {#if renaming}
      <form class="panel" onsubmit={rename}>
        <label class="rw-label" for="artist-rename">Artist name</label>
        <input id="artist-rename" class="rw-input" maxlength="200" bind:value={newName} />
        <p class="rw-hint">Changes the name in all {artist.track_count} tracks that credit it, in Rewind's library only. Other credited artists are kept. Music files are not modified.</p>
        {#if renameError}<p class="rw-alert" role="alert">{renameError}</p>{/if}
        <div class="actions">
          <button class="rw-button" type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
          <button class="rw-button rw-button-secondary" type="button" onclick={() => (renaming = false)}>Cancel</button>
        </div>
      </form>
    {/if}
    {#if artist.tracks_truncated}<p class="rw-notice">This artist has many tracks; the first {artist.tracks.length} are listed.</p>{/if}

    {#if page.top_tracks.length > 0}
      <h2 class="section">Your top tracks</h2>
      <TrackList tracks={page.top_tracks} label="Your top tracks" {context} {onUnauthorized} numbering="position" showArtwork hideArtistLink={artist.id}
        detail={(t) => `${t.play_count ?? 0} ${t.play_count === 1 ? 'play' : 'plays'}`} />
    {/if}
    {#if page.recent_tracks.length > 0}
      <h2 class="section">Recently played</h2>
      <TrackList tracks={page.recent_tracks} label="Recently played" {context} {onUnauthorized} showArtwork hideArtistLink={artist.id} />
    {/if}

    {#if artist.albums.length > 0}
      <h2 class="section">Albums</h2>
      {#each byYear as [year, albums] (year)}
        <h3 class="year">{year}</h3>
        {@render albumGrid(albums, `Albums from ${year}`)}
      {/each}
    {/if}
    {#if artist.appears_on.length > 0}
      <h2 class="section">Appears on</h2>
      {@render albumGrid(artist.appears_on, 'Appears on')}
    {/if}

    <h2 class="section">All tracks</h2>
    <TrackList tracks={artist.tracks} label="All tracks" {context} {onUnauthorized} hideArtistLink={artist.id} />
  {/if}
</section>

<style>
  .hero {
    display: grid;
    grid-template-columns: minmax(0, 12rem) minmax(0, 1fr);
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

  .meta {
    color: var(--rw-color-muted);
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
    margin-top: var(--rw-space-2);
  }

  .panel {
    display: grid;
    gap: var(--rw-space-2);
    margin-bottom: var(--rw-space-4);
    padding: var(--rw-space-3);
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-surface);
  }

  .section {
    margin: var(--rw-space-6) 0 var(--rw-space-3);
    font-size: var(--rw-font-size-lg);
    font-weight: var(--rw-font-weight-black);
    text-transform: uppercase;
  }

  .year {
    margin: var(--rw-space-4) 0 var(--rw-space-2);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 9.5rem), 1fr));
    gap: var(--rw-space-3);
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
    font-weight: var(--rw-font-weight-bold);
  }

  .sub {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  @media (max-width: 40rem) {
    .hero {
      grid-template-columns: minmax(0, 1fr);
    }

    .cover {
      width: min(100%, 12rem);
    }
  }
</style>
