<script lang="ts">
  import { onDestroy } from 'svelte'
  import { streamStatus, streamUrl, type Track } from '../services/api'
  import { formatTime } from '../services/format'
  import type { Sequence } from '../services/sequence'

  interface Props {
    /** The selected track, or null when nothing is selected. */
    track: Track | null
    /** Called when the stream reports that the session has expired. */
    onUnauthorized: () => void
    /** Called when playback starts or stops (for "Playing" labels elsewhere). */
    onPlayingChange?: (playing: boolean) => void
    /** Called when the track plays to its end. */
    onEnded?: () => void
    /** Previous/next controls while playing a playlist; absent otherwise. */
    sequence?: Sequence | null
  }
  let { track, onUnauthorized, onPlayingChange, onEnded, sequence = null }: Props = $props()

  // One <audio> element for the lifetime of the component; selecting another
  // track only changes its source, so no elements or listeners accumulate.
  let audio: HTMLAudioElement | undefined = $state()
  let loadedId: number | null = null

  let playing = $state(false)
  let loading = $state(false)
  let error = $state('')
  let notice = $state('')
  let current = $state(0)
  let duration = $state(0)

  $effect(() => {
    const t = track
    if (!audio) return
    if (!t) {
      release()
    } else if (t.id !== loadedId) {
      load(t)
    }
  })

  $effect(() => {
    onPlayingChange?.(playing)
  })

  onDestroy(release)

  function load(t: Track) {
    if (!audio) return
    error = ''
    notice = ''
    current = 0
    duration = t.duration_ms ? t.duration_ms / 1000 : 0
    playing = false
    loading = true
    loadedId = t.id
    // The browser fetches this URL itself, using Range requests to seek; the
    // session cookie is sent automatically (same origin).
    audio.src = streamUrl(t.id)
    audio.load()
    void start()
  }

  /** Stops playback and lets the browser release the previous media. */
  function release() {
    loadedId = null
    playing = false
    loading = false
    if (audio) {
      audio.pause()
      audio.removeAttribute('src')
      audio.load()
    }
  }

  async function start() {
    if (!audio) return
    notice = ''
    try {
      await audio.play()
    } catch (err) {
      const name = err instanceof DOMException ? err.name : ''
      if (name === 'NotAllowedError') {
        loading = false
        notice = 'Press play to start.'
      }
      // AbortError: a newer load replaced this one. NotSupportedError and
      // network problems are reported through the element's error event.
    }
  }

  function toggle() {
    if (!audio || !track || error) return
    if (playing) {
      audio.pause()
    } else {
      void start()
    }
  }

  function seek(event: Event) {
    if (!audio) return
    const value = Number((event.currentTarget as HTMLInputElement).value)
    if (Number.isFinite(value)) {
      audio.currentTime = value
      current = value
    }
  }

  function updateDuration() {
    if (audio && Number.isFinite(audio.duration) && audio.duration > 0) {
      duration = audio.duration
    }
  }

  async function onError() {
    const id = loadedId
    const mediaError = audio?.error
    if (id === null || !mediaError || mediaError.code === mediaError.MEDIA_ERR_ABORTED) return
    loading = false
    playing = false
    // The media element does not expose HTTP status codes; ask the server.
    const status = await streamStatus(id)
    if (id !== loadedId) return // another track was selected meanwhile
    switch (status) {
      case 401:
        error = 'Your session has expired. Sign in again.'
        onUnauthorized()
        break
      case 404:
        error = "This track's file was not found. It may have been moved or deleted; rescan the library."
        break
      case 409:
        error = "This track's library folder is no longer configured."
        break
      case 503:
        error = 'The library drive is not available. Check that it is plugged in and mounted.'
        break
      case 0:
        error = 'Cannot reach the Rewind server.'
        break
      case 200:
      case 206: {
        const kind = track ? `${track.format.toUpperCase()}${track.codec ? ` (${track.codec})` : ''}` : 'this'
        error = `This browser cannot play ${kind} files.`
        break
      }
      default:
        error = 'Playback failed.'
    }
  }
</script>

<!--
  The bar stays mounted while nothing is selected (only hidden), so the single
  <audio> element survives view changes and is never recreated.
-->
<section class="playbar" aria-label="Player" aria-busy={loading} hidden={!track}>
  <!-- svelte-ignore a11y_media_has_caption (music has no captions) -->
  <audio
    bind:this={audio}
    preload="none"
    onloadstart={() => (loading = !!loadedId)}
    onwaiting={() => (loading = true)}
    oncanplay={() => (loading = false)}
    onplaying={() => {
      loading = false
      playing = true
    }}
    onplay={() => (playing = true)}
    onpause={() => (playing = false)}
    onended={() => {
      playing = false
      current = duration
      onEnded?.()
    }}
    ontimeupdate={() => {
      if (audio) current = audio.currentTime
    }}
    ondurationchange={updateDuration}
    onloadedmetadata={updateDuration}
    onerror={onError}
  ></audio>

  {#if track}
    <div class="now">
      <p class="title" title={track.title}>{track.title}</p>
      <p class="meta">
        {#if track.artist || track.album}{[track.artist, track.album].filter(Boolean).join(' · ')}{:else}&nbsp;{/if}
      </p>
      {#if sequence}<p class="meta sequence">{sequence.label}</p>{/if}
    </div>

    <div class="transport">
      <div class="buttons">
        {#if sequence}
          <button
            class="rw-button rw-button-secondary step"
            type="button"
            onclick={sequence.onPrevious}
            disabled={!sequence.hasPrevious}
            aria-label="Previous track">&lt;</button
          >
        {/if}
        <button class="rw-button play" type="button" onclick={toggle} disabled={!!error} aria-label={playing ? 'Pause' : 'Play'}>
          {playing ? 'Pause' : 'Play'}
        </button>
        {#if sequence}
          <button
            class="rw-button rw-button-secondary step"
            type="button"
            onclick={sequence.onNext}
            disabled={!sequence.hasNext}
            aria-label="Next track">&gt;</button
          >
        {/if}
      </div>
      <span class="time" aria-label="Elapsed time">{formatTime(current)}</span>
      <input
        class="seek"
        type="range"
        min="0"
        max={duration || 0}
        step="1"
        value={current}
        oninput={seek}
        disabled={!duration || !!error}
        aria-label="Seek"
        aria-valuetext={`${formatTime(current)} of ${formatTime(duration || undefined)}`}
      />
      <span class="time" aria-label="Duration">{formatTime(duration || undefined)}</span>
    </div>

    <div class="message">
      {#if error}
        <p class="error" role="alert">{error}</p>
      {:else}
        <p class="status" role="status" aria-live="polite">{#if loading}Loading…{:else if notice}{notice}{/if}</p>
      {/if}
    </div>
  {/if}
</section>

<style>
  .playbar {
    position: fixed;
    inset: auto 0 0 0;
    z-index: 10;
    display: grid;
    grid-template-columns: minmax(10rem, 1fr) minmax(16rem, 2fr);
    grid-template-areas: 'now transport' 'now message';
    column-gap: var(--rw-space-5);
    align-items: center;
    padding: var(--rw-space-3) var(--rw-space-5);
    padding-bottom: calc(var(--rw-space-3) + env(safe-area-inset-bottom));
    border-top: var(--rw-border);
    background: var(--rw-color-bg);
  }

  .playbar[hidden] {
    display: none;
  }

  .now {
    grid-area: now;
    min-width: 0;
    border-left: var(--rw-border-width) solid var(--rw-color-accent);
    padding-left: var(--rw-space-3);
  }

  .title {
    font-weight: var(--rw-font-weight-black);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .meta {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .transport {
    grid-area: transport;
    display: grid;
    grid-template-columns: auto auto 1fr auto;
    align-items: center;
    gap: var(--rw-space-3);
    min-width: 0;
  }

  .buttons {
    display: flex;
    gap: var(--rw-space-1);
  }

  .play {
    min-width: 6.5rem;
  }

  .step {
    min-width: 2.75rem;
    padding-inline: var(--rw-space-2);
  }

  .sequence {
    font-family: var(--rw-font-mono);
    font-size: 0.75rem;
  }

  .time {
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    font-variant-numeric: tabular-nums;
  }

  .seek {
    width: 100%;
    min-width: 0;
    /* A 44px-tall hit area for touch; the visible track stays thin. */
    height: 2.75rem;
    margin: 0;
    accent-color: var(--rw-color-accent);
    cursor: pointer;
  }

  .message {
    grid-area: message;
    min-width: 0;
  }

  .status,
  .error {
    min-height: 1.25em;
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  .status {
    color: var(--rw-color-muted);
  }

  .error {
    color: var(--rw-color-text);
    border-left: var(--rw-border-width) solid var(--rw-color-danger);
    padding-left: var(--rw-space-2);
  }

  .error::before {
    content: 'Error: ';
    font-weight: var(--rw-font-weight-bold);
  }

  @media (max-width: 48rem) {
    .playbar {
      grid-template-columns: 1fr;
      grid-template-areas: 'now' 'transport' 'message';
      row-gap: var(--rw-space-1);
      padding: var(--rw-space-2) var(--rw-space-3);
      padding-bottom: calc(var(--rw-space-2) + env(safe-area-inset-bottom));
    }

    .play {
      min-width: 5rem;
      padding-inline: var(--rw-space-3);
    }
  }
</style>
