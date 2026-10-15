<script lang="ts">
  import { onDestroy, tick } from 'svelte'
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
    /** Called when the Now Playing screen opens or closes (the page behind becomes inert). */
    onExpandedChange?: (expanded: boolean) => void
    /** Previous/next controls while playing a playlist; absent otherwise. */
    sequence?: Sequence | null
  }
  let { track, onUnauthorized, onPlayingChange, onEnded, onExpandedChange, sequence = null }: Props = $props()

  // One <audio> element for the lifetime of the component; selecting another
  // track only changes its source, so no elements or listeners accumulate.
  // The mini bar and the Now Playing screen are two views of the same state
  // below; switching between them never touches the element.
  let audio: HTMLAudioElement | undefined = $state()
  let loadedId: number | null = null

  let playing = $state(false)
  let loading = $state(false)
  let error = $state('')
  let notice = $state('')
  let current = $state(0)
  let duration = $state(0)

  // --- Now Playing screen ---
  let expanded = $state(false)
  let openButton: HTMLButtonElement | undefined = $state()
  let closeButton: HTMLButtonElement | undefined = $state()
  let screenEl: HTMLDivElement | undefined = $state()

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

  // Nothing to show without a track (for example after the session expired).
  $effect(() => {
    if (!track && expanded) expanded = false
  })

  $effect(() => {
    onExpandedChange?.(expanded)
    document.documentElement.classList.toggle('rw-no-scroll', expanded)
  })

  onDestroy(() => {
    release()
    document.documentElement.classList.remove('rw-no-scroll')
  })

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

  async function expand() {
    if (!track || expanded) return
    expanded = true
    await tick()
    closeButton?.focus()
  }

  async function collapse() {
    if (!expanded) return
    expanded = false
    await tick()
    openButton?.focus()
  }

  /** Escape closes; Tab stays inside the screen while it is open. */
  function onScreenKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      void collapse()
      return
    }
    if (event.key !== 'Tab' || !screenEl) return
    const focusable = [...screenEl.querySelectorAll<HTMLElement>('button, input')].filter(
      (el) => !(el as HTMLButtonElement | HTMLInputElement).disabled,
    )
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
  }

  // Pull up the mini bar's track info to open, pull the screen's header down
  // to close. Only gestures that start in these two areas count, so seeking
  // and scrolling are not affected; a tap still works as a click.
  const SWIPE_PX = 48
  let swipeStart: number | null = null
  function swipeDown(event: PointerEvent) {
    swipeStart = event.clientY
  }
  function headerSwipeDown(event: PointerEvent) {
    swipeStart = event.target instanceof Element && event.target.closest('.screen-head') ? event.clientY : null
  }
  function swipeUp(event: PointerEvent, direction: 'up' | 'down') {
    if (swipeStart === null) return
    const moved = event.clientY - swipeStart
    swipeStart = null
    if (direction === 'up' && moved < -SWIPE_PX) void expand()
    if (direction === 'down' && moved > SWIPE_PX) void collapse()
  }

  const metaLine = $derived(track ? [track.artist, track.album].filter(Boolean).join(' · ') : '')
  const valueText = $derived(`${formatTime(current)} of ${formatTime(duration || undefined)}`)
</script>

{#snippet artwork(size: 'small' | 'large')}
  <!-- No artwork is indexed yet, so every track shows the same placeholder. -->
  <span class="art art-{size}" aria-hidden="true">
    <svg viewBox="0 0 24 24" focusable="false"><path d="M9 18.5a2.5 2.5 0 1 1-2-2.45V5.5l11-2v11a2.5 2.5 0 1 1-2-2.45V7.1l-7 1.27z" /></svg>
  </span>
{/snippet}

{#snippet icon(name: 'previous' | 'next' | 'play' | 'pause' | 'collapse')}
  <svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
    {#if name === 'previous'}<path d="M6 5h2v14H6zM20 5v14L9 12z" />
    {:else if name === 'next'}<path d="M16 5h2v14h-2zM4 5v14l11-7z" />
    {:else if name === 'play'}<path d="M7 4.5v15L20 12z" />
    {:else if name === 'pause'}<path d="M6 4.5h4.5v15H6zM13.5 4.5H18v15h-4.5z" />
    {:else}<path d="M5.4 8.6 12 15.2l6.6-6.6L20 10l-8 8-8-8z" />{/if}
  </svg>
{/snippet}

<!--
  The bar stays mounted while nothing is selected (only hidden), so the single
  <audio> element survives view changes and is never recreated.
-->
<section class="playbar" aria-label="Player" aria-busy={loading} hidden={!track} inert={expanded}>
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
    <button
      class="now"
      type="button"
      bind:this={openButton}
      onclick={expand}
      onpointerdown={swipeDown}
      onpointerup={(e) => swipeUp(e, 'up')}
      aria-haspopup="dialog"
      aria-expanded={expanded}
      aria-label={`Open Now Playing: ${track.title}`}
    >
      {@render artwork('small')}
      <span class="now-text">
        <span class="title" title={track.title}>{track.title}</span>
        <span class="meta">{#if metaLine}{metaLine}{:else}&nbsp;{/if}</span>
        {#if sequence}<span class="meta sequence">{sequence.label}</span>{/if}
      </span>
    </button>

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
        aria-valuetext={valueText}
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

{#if expanded && track}
  <div
    class="screen"
    role="dialog"
    aria-modal="true"
    aria-labelledby="np-title"
    tabindex="-1"
    bind:this={screenEl}
    onkeydown={onScreenKeydown}
    onpointerdown={headerSwipeDown}
    onpointerup={(e) => swipeUp(e, 'down')}
  >
    <header class="screen-head">
      <button
        class="rw-button rw-button-secondary collapse"
        type="button"
        bind:this={closeButton}
        onclick={collapse}
        aria-label="Close Now Playing"
      >
        {@render icon('collapse')}
      </button>
      <p class="label">Now playing</p>
      {#if sequence}<p class="label sequence-label">{sequence.label}</p>{/if}
    </header>

    <div class="screen-body">
      {@render artwork('large')}

      <div class="details">
        <div class="info">
          <h2 id="np-title" class="np-title">{track.title}</h2>
          <p class="np-artist">{track.artist ?? 'Unknown artist'}</p>
          {#if track.album}<p class="np-album">{track.album}</p>{/if}
        </div>

        <div class="progress">
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
            aria-valuetext={valueText}
          />
          <div class="times">
            <span class="time" aria-label="Elapsed time">{formatTime(current)}</span>
            <span class="time" aria-label="Duration">{formatTime(duration || undefined)}</span>
          </div>
        </div>

        <div class="controls">
          {#if sequence}
            <button
              class="rw-button rw-button-secondary control"
              type="button"
              onclick={sequence.onPrevious}
              disabled={!sequence.hasPrevious}
              aria-label="Previous track">{@render icon('previous')}</button
            >
          {/if}
          <button class="rw-button control main" type="button" onclick={toggle} disabled={!!error} aria-label={playing ? 'Pause' : 'Play'}>
            {@render icon(playing ? 'pause' : 'play')}
          </button>
          {#if sequence}
            <button
              class="rw-button rw-button-secondary control"
              type="button"
              onclick={sequence.onNext}
              disabled={!sequence.hasNext}
              aria-label="Next track">{@render icon('next')}</button
            >
          {/if}
        </div>

        <div class="screen-message">
          {#if error}
            <p class="error">{error}</p>
          {:else}
            <p class="status">{#if loading}Loading…{:else if notice}{notice}{/if}</p>
          {/if}
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  /* --- Mini player: a floating rounded bar --- */
  .playbar {
    position: fixed;
    inset: auto var(--rw-space-3) calc(var(--rw-space-3) + env(safe-area-inset-bottom)) var(--rw-space-3);
    z-index: 10;
    display: grid;
    grid-template-columns: minmax(10rem, 1fr) minmax(16rem, 2fr);
    grid-template-areas: 'now transport' 'now message';
    column-gap: var(--rw-space-5);
    align-items: center;
    padding: var(--rw-space-2) var(--rw-space-4);
    border: var(--rw-border);
    border-radius: var(--rw-radius-xl);
    background: var(--rw-color-surface);
    box-shadow: var(--rw-shadow);
  }

  .playbar[hidden] {
    display: none;
  }

  .now {
    grid-area: now;
    display: flex;
    align-items: center;
    gap: var(--rw-space-3);
    min-width: 0;
    min-height: 3.25rem;
    padding: var(--rw-space-1);
    border: 0;
    border-radius: var(--rw-radius-lg);
    background: transparent;
    color: var(--rw-color-text);
    text-align: left;
    cursor: pointer;
    /* Vertical pulls open the screen instead of scrolling the page. */
    touch-action: pan-x;
  }

  .now:hover {
    background: var(--rw-color-bg);
  }

  .now-text {
    display: grid;
    min-width: 0;
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

  /* --- Artwork placeholder, shared by both views --- */
  .art {
    display: grid;
    flex: none;
    place-items: center;
    aspect-ratio: 1;
    border: var(--rw-border);
    background: var(--rw-color-secondary);
    color: var(--rw-color-on-secondary);
  }

  .art svg {
    width: 45%;
    height: 45%;
    fill: currentColor;
  }

  .art-small {
    width: 2.75rem;
    border-radius: var(--rw-radius-sm);
  }

  .art-large {
    width: 100%;
    border-radius: var(--rw-radius-art);
    box-shadow: var(--rw-shadow);
  }

  /* --- Now Playing screen --- */
  .screen {
    position: fixed;
    inset: 0;
    z-index: 30;
    display: grid;
    grid-template-rows: auto minmax(0, 1fr);
    padding: calc(var(--rw-space-4) + env(safe-area-inset-top)) calc(var(--rw-space-5) + env(safe-area-inset-right))
      calc(var(--rw-space-5) + env(safe-area-inset-bottom)) calc(var(--rw-space-5) + env(safe-area-inset-left));
    overflow-y: auto;
    background: var(--rw-color-bg);
    color: var(--rw-color-text);
    animation: screen-in 180ms ease-out;
  }

  .screen:focus {
    outline: none;
  }

  @keyframes screen-in {
    from {
      opacity: 0;
      transform: translateY(1.5rem);
    }
  }

  .screen-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2) var(--rw-space-4);
    width: 100%;
    max-width: 60rem;
    margin: 0 auto;
    touch-action: pan-x;
  }

  .collapse {
    display: grid;
    place-items: center;
    width: 2.75rem;
    padding: 0;
  }

  .label {
    font-family: var(--rw-font-mono);
    font-size: 0.75rem;
    font-weight: var(--rw-font-weight-bold);
    letter-spacing: 0.12em;
    text-transform: uppercase;
  }

  .sequence-label {
    margin-left: auto;
    color: var(--rw-color-muted);
    font-weight: normal;
    letter-spacing: 0.04em;
    text-transform: none;
  }

  .screen-body {
    display: grid;
    grid-template-columns: minmax(0, min(26rem, 44vw)) minmax(0, 1fr);
    align-items: center;
    align-content: center;
    gap: var(--rw-space-6);
    width: 100%;
    max-width: 60rem;
    margin: 0 auto;
    padding-block: var(--rw-space-5);
  }

  .details {
    display: grid;
    gap: var(--rw-space-5);
    min-width: 0;
  }

  .info {
    display: grid;
    gap: var(--rw-space-1);
    min-width: 0;
  }

  .np-title {
    display: -webkit-box;
    overflow: hidden;
    font-size: calc(clamp(1.75rem, 4vw, 3rem) * var(--rw-heading-scale));
    font-weight: var(--rw-font-weight-black);
    line-height: 1.05;
    letter-spacing: -0.02em;
    overflow-wrap: anywhere;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
    line-clamp: 3;
  }

  .np-artist,
  .np-album {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .np-artist {
    font-size: var(--rw-font-size-lg);
    font-weight: var(--rw-font-weight-bold);
  }

  .np-album {
    color: var(--rw-color-muted);
  }

  .times {
    display: flex;
    justify-content: space-between;
    color: var(--rw-color-muted);
  }

  .controls {
    display: flex;
    align-items: center;
    gap: var(--rw-space-4);
  }

  .control {
    display: grid;
    place-items: center;
    width: 3.5rem;
    height: 3.5rem;
    padding: 0;
  }

  .control.main {
    width: 4.75rem;
    height: 4.75rem;
    border-radius: var(--rw-radius-round);
    box-shadow: var(--rw-shadow);
  }

  .icon {
    width: 1.5rem;
    height: 1.5rem;
    fill: currentColor;
  }

  .main .icon {
    width: 2rem;
    height: 2rem;
  }

  .screen-message {
    min-height: 1.5em;
  }

  @media (max-width: 48rem) {
    .playbar {
      inset: auto var(--rw-space-2) calc(var(--rw-space-2) + env(safe-area-inset-bottom)) var(--rw-space-2);
      grid-template-columns: 1fr;
      grid-template-areas: 'now' 'transport' 'message';
      row-gap: var(--rw-space-1);
      padding: var(--rw-space-2) var(--rw-space-3);
      border-radius: var(--rw-radius-lg);
    }

    .play {
      min-width: 5rem;
      padding-inline: var(--rw-space-3);
    }

    .screen {
      padding-inline: calc(var(--rw-space-4) + env(safe-area-inset-left)) calc(var(--rw-space-4) + env(safe-area-inset-right));
    }

    /* Vertical: artwork, then details; the artwork shrinks on short screens. */
    .screen-body {
      grid-template-columns: minmax(0, 1fr);
      justify-items: center;
      align-content: start;
      gap: var(--rw-space-5);
      padding-block: var(--rw-space-4);
    }

    .art-large {
      width: min(100%, 24rem, 42dvh);
    }

    .details {
      width: 100%;
      max-width: 28rem;
    }

    .controls {
      justify-content: center;
    }
  }
</style>
