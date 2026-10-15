<script lang="ts">
  // A list of tracks that plays through the shared player: choosing a row
  // plays the list from that track (Play now on the list). Rows show the
  // current track in text ("Playing"/"Paused"), favorites, durations and a
  // menu with the other actions (also on right-click or Shift+F10).
  import type { PlaybackContext, Track } from '../services/api'
  import { formatTime } from '../services/format'
  import { listening } from '../services/listening.svelte'
  import { player } from '../services/player.svelte'
  import { sentence } from '../services/validation'
  import Artwork from './Artwork.svelte'
  import Icon from './Icon.svelte'
  import TrackMenu, { type MenuAction } from './TrackMenu.svelte'

  interface Props {
    tracks: Track[]
    label: string
    context?: PlaybackContext | null
    onUnauthorized: () => void
    /** Number rows by track number (albums) or by position. */
    numbering?: 'track' | 'position' | 'none'
    /** Insert "Disc N" headings when the disc changes. */
    discs?: boolean
    showArtwork?: boolean
    showAlbum?: boolean
    showArtist?: boolean
    hideAlbumLink?: boolean
    hideArtistLink?: string
    /** Extra text under the title (e.g. when it was played). */
    detail?: (track: Track, index: number) => string
    extra?: (track: Track, index: number) => MenuAction[]
    /** Indexes of selected rows; shows checkboxes when given. */
    selected?: number[]
    /** Plays something other than this list (e.g. the album of a history row). */
    onPlay?: (index: number) => void
  }
  let {
    tracks,
    label,
    context = null,
    onUnauthorized,
    numbering = 'none',
    discs = false,
    showArtwork = false,
    showAlbum = true,
    showArtist = true,
    hideAlbumLink = false,
    hideArtistLink,
    detail,
    extra,
    selected = $bindable(),
    onPlay,
  }: Props = $props()

  let menuFor = $state<number | null>(null)
  let error = $state('')

  function play(i: number) {
    if (!tracks[i]?.available) return
    if (onPlay) onPlay(i)
    else player.playTracks(tracks, i, context)
  }

  async function toggleFavorite(t: Track) {
    error = ''
    try {
      await listening.toggleFavorite(t)
    } catch (err) {
      if ((err as { status?: number }).status === 401) onUnauthorized()
      else error = err instanceof Error ? sentence(err.message) : 'Could not change the favorite.'
    }
  }

  function onRowKey(event: KeyboardEvent, i: number) {
    if ((event.key === 'F10' && event.shiftKey) || event.key === 'ContextMenu') {
      event.preventDefault()
      menuFor = i
    }
  }

  function toggleSelected(i: number) {
    if (!selected) return
    selected = selected.includes(i) ? selected.filter((x) => x !== i) : [...selected, i]
  }

  const discOf = (t: Track) => t.disc_number ?? 1
  const multiDisc = $derived(discs && new Set(tracks.map(discOf)).size > 1)
  const artistsOf = (t: Track) => t.artist ?? t.album_artist ?? ''
</script>

{#if error}<p class="rw-alert" role="alert">{error}</p>{/if}
<ol class="list" aria-label={label}>
  {#each tracks as t, i (`${t.id}-${i}`)}
    {@const current = player.isCurrentTrack(t.id)}
    {#if multiDisc && (i === 0 || discOf(tracks[i - 1]) !== discOf(t))}
      <li class="disc" aria-hidden="true">Disc {discOf(t)}</li>
    {/if}
    <li
      class="row"
      class:current
      class:unavailable={!t.available}
      oncontextmenu={(e) => {
        e.preventDefault()
        menuFor = i
      }}
    >
      {#if selected}
        <input
          class="select"
          type="checkbox"
          checked={selected.includes(i)}
          onchange={() => toggleSelected(i)}
          aria-label={`Select ${t.title}`}
        />
      {/if}
      <button
        class="main"
        type="button"
        onclick={() => play(i)}
        onkeydown={(e) => onRowKey(e, i)}
        disabled={!t.available}
        aria-current={current ? 'true' : undefined}
        aria-label={`Play ${t.title}${artistsOf(t) ? ` by ${artistsOf(t)}` : ''}${t.available ? '' : ' (unavailable)'}`}
      >
        {#if numbering !== 'none'}
          <span class="num" aria-hidden="true">{numbering === 'track' ? (t.track_number ?? '–') : i + 1}</span>
        {/if}
        {#if showArtwork}
          <span class="thumb"><Artwork trackId={t.id} revision={t.revision} /></span>
        {/if}
        <span class="text">
          <span class="title">
            {#if current}<span class="marker">{player.playing ? 'Playing' : 'Paused'}</span>{/if}
            <span class="title-text" title={t.title}>{t.title}</span>
          </span>
          <span class="sub">
            {[showArtist ? artistsOf(t) || 'Unknown artist' : '', showAlbum ? t.album : '', detail?.(t, i)].filter(Boolean).join(' · ')}
          </span>
        </span>
        <span class="end">
          {#if t.status === 'missing'}<span class="badge">File missing</span>
          {:else if !t.available}<span class="badge">Unavailable</span>{/if}
          <span class="len">{formatTime(t.duration_ms ? t.duration_ms / 1000 : undefined)}</span>
        </span>
      </button>
      <button
        class="fav"
        type="button"
        onclick={() => toggleFavorite(t)}
        aria-pressed={listening.isFavorite(t)}
        aria-label={`Favorite: ${t.title}`}
        title={listening.isFavorite(t) ? 'Remove from favorites' : 'Add to favorites'}
      >
        <Icon name={listening.isFavorite(t) ? 'heartFilled' : 'heart'} />
      </button>
      <TrackMenu
        track={t}
        {context}
        {onUnauthorized}
        hideAlbum={hideAlbumLink}
        hideArtist={hideArtistLink}
        extra={extra?.(t, i) ?? []}
        bind:open={() => menuFor === i, (v) => (menuFor = v ? i : menuFor === i ? null : menuFor)}
      />
    </li>
  {/each}
</ol>

<style>
  .list {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .disc {
    margin-top: var(--rw-space-4);
    padding: var(--rw-space-2) var(--rw-space-3);
    border-bottom: var(--rw-border);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    font-weight: var(--rw-font-weight-bold);
    text-transform: uppercase;
  }

  .row {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto auto;
    align-items: center;
    border-bottom: 1px solid var(--rw-color-surface);
  }

  .row:not(:has(.select)) {
    grid-template-columns: minmax(0, 1fr) auto auto;
  }

  .select {
    width: 1.25rem;
    height: 1.25rem;
    margin: 0 var(--rw-space-2);
    accent-color: var(--rw-color-primary);
  }

  .main {
    display: flex;
    align-items: center;
    gap: var(--rw-space-3);
    min-width: 0;
    min-height: 3.25rem;
    padding: var(--rw-space-2) var(--rw-space-2);
    border: 0;
    border-left: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-sm);
    background: transparent;
    color: var(--rw-color-text);
    text-align: left;
    cursor: pointer;
  }

  .main:hover:not(:disabled) {
    background: var(--rw-color-surface);
  }

  .main:disabled {
    cursor: not-allowed;
    color: var(--rw-color-muted);
  }

  .current .main {
    border-left-color: var(--rw-color-accent);
  }

  .num {
    flex: none;
    width: 2rem;
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    text-align: right;
  }

  .thumb {
    flex: none;
    width: 2.75rem;
  }

  .text {
    display: grid;
    flex: 1;
    min-width: 0;
  }

  .title {
    display: flex;
    align-items: center;
    gap: var(--rw-space-2);
    min-width: 0;
    font-weight: var(--rw-font-weight-bold);
  }

  .title-text,
  .sub {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sub {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .marker,
  .badge {
    flex: none;
    padding: 0 var(--rw-space-1);
    border-radius: var(--rw-radius-xs);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    font-weight: normal;
    text-transform: uppercase;
  }

  .marker {
    background: var(--rw-color-accent);
    color: var(--rw-color-on-accent);
  }

  .badge {
    border: var(--rw-border-width) solid var(--rw-color-danger);
    color: var(--rw-color-danger);
  }

  .end {
    display: flex;
    flex: none;
    align-items: center;
    gap: var(--rw-space-2);
  }

  .len {
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    font-variant-numeric: tabular-nums;
  }

  .fav {
    display: grid;
    place-items: center;
    width: 2.75rem;
    height: 2.75rem;
    padding: 0;
    border: 0;
    border-radius: var(--rw-radius-sm);
    background: transparent;
    color: var(--rw-color-muted);
    cursor: pointer;
  }

  .fav[aria-pressed='true'] {
    color: var(--rw-color-accent);
  }

  @media (max-width: 30rem) {
    .len {
      display: none;
    }
  }
</style>
