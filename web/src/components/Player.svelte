<script lang="ts">
  // The mini player (a floating bar) and the Now Playing screen: two views
  // of the shared player state (services/player.svelte.ts). Neither owns
  // audio; opening and closing the screen never touches playback.
  import { tick } from 'svelte'
  import { getTrack, type Track } from '../services/api'
  import { formatTime } from '../services/format'
  import { listening } from '../services/listening.svelte'
  import { player, SEEK_STEP_S, SPEEDS } from '../services/player.svelte'
  import { albumHref, artistHref, navigate, ui } from '../services/ui.svelte'
  import { sentence } from '../services/validation'
  import Artwork from './Artwork.svelte'
  import Icon from './Icon.svelte'
  import TrackMenu from './TrackMenu.svelte'

  interface Props {
    onUnauthorized: () => void
  }
  let { onUnauthorized }: Props = $props()

  const track = $derived(player.current)
  let openButton: HTMLButtonElement | undefined = $state()
  let closeButton: HTMLButtonElement | undefined = $state()
  let screenEl: HTMLDivElement | undefined = $state()
  let favError = $state('')

  // Lyrics are only in single-track reads; fetched when first shown.
  let showLyrics = $state(false)
  let lyrics = $state<{ id: number; text: string | null } | null>(null)
  let lyricsError = $state('')

  // Nothing to show without a track (for example after the queue was cleared).
  $effect(() => {
    if (!track && ui.nowPlayingOpen) ui.nowPlayingOpen = false
  })

  $effect(() => {
    document.documentElement.classList.toggle('rw-no-scroll', ui.nowPlayingOpen)
    return () => document.documentElement.classList.remove('rw-no-scroll')
  })

  $effect(() => {
    const t = track
    if (showLyrics && t && t.has_lyrics && lyrics?.id !== t.id) void loadLyrics(t)
  })

  async function loadLyrics(t: Track) {
    lyricsError = ''
    try {
      const full = await getTrack(t.id)
      if (player.current?.id === t.id) lyrics = { id: t.id, text: full.lyrics ?? null }
    } catch (err) {
      if ((err as { status?: number }).status === 401) onUnauthorized()
      else lyricsError = err instanceof Error ? sentence(err.message) : 'Could not load the lyrics.'
    }
  }

  async function expand() {
    if (!track || ui.nowPlayingOpen) return
    ui.nowPlayingOpen = true
    await tick()
    closeButton?.focus()
  }

  async function collapse() {
    if (!ui.nowPlayingOpen) return
    ui.nowPlayingOpen = false
    await tick()
    openButton?.focus()
  }

  /** Escape closes; Tab stays inside the screen while it is open. */
  function onScreenKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && !event.defaultPrevented) {
      event.preventDefault()
      void collapse()
      return
    }
    if (event.key !== 'Tab' || !screenEl) return
    // Controls inside a closed <details> are not reachable, except its summary.
    const focusable = [...screenEl.querySelectorAll<HTMLElement>('button, input, select, a[href], summary')].filter(
      (el) => !(el as HTMLButtonElement).disabled && !(el.tagName !== 'SUMMARY' && el.closest('details:not([open])')),
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
  function swipeEnd(event: PointerEvent, direction: 'up' | 'down') {
    if (swipeStart === null) return
    const moved = event.clientY - swipeStart
    swipeStart = null
    if (direction === 'up' && moved < -SWIPE_PX) void expand()
    if (direction === 'down' && moved > SWIPE_PX) void collapse()
  }

  function seekInput(event: Event) {
    player.seek(Number((event.currentTarget as HTMLInputElement).value))
  }

  async function toggleFavorite() {
    if (!track) return
    favError = ''
    try {
      await listening.toggleFavorite(track)
    } catch (err) {
      if ((err as { status?: number }).status === 401) onUnauthorized()
      else favError = err instanceof Error ? sentence(err.message) : 'Could not change the favorite.'
    }
  }

  function go(hash: string) {
    ui.nowPlayingOpen = false
    navigate(hash)
  }

  const duration = $derived(player.duration)
  const valueText = $derived(`${formatTime(player.position)} of ${formatTime(duration || undefined)}`)
  const remaining = $derived(duration ? Math.max(duration - player.position, 0) : undefined)
  const favorite = $derived(track ? listening.isFavorite(track) : false)
  const repeatLabel = $derived(player.repeat === 'off' ? 'Repeat: off' : player.repeat === 'all' ? 'Repeat: all' : 'Repeat: one')
  const artistLine = $derived(track ? (track.artist ?? track.album_artist ?? 'Unknown artist') : '')
  const metaLine = $derived(track ? [artistLine, track.album].filter(Boolean).join(' · ') : '')
  const position = $derived(player.index >= 0 ? `${player.index + 1} of ${player.entries.length}` : '')
  const contextLabel = $derived(player.context?.name ? `${player.context.name} · ${position}` : position)
  const status = $derived(player.loading ? 'Loading…' : player.notice)
  const progressPct = $derived(duration ? Math.min((player.position / duration) * 100, 100) : 0)

  function progressBar(node: HTMLElement, pct: number) {
    node.style.setProperty('--progress', `${pct}%`)
    return { update: (p: number) => node.style.setProperty('--progress', `${p}%`) }
  }

  function infoRows(t: Track): [string, string][] {
    const rows: [string, string | undefined][] = [
      ['Album artist', t.album_artist],
      ['Track', t.track_number ? `${t.track_number}${t.disc_number ? ` (disc ${t.disc_number})` : ''}` : undefined],
      ['Year', t.year ? String(t.year) : undefined],
      ['Genre', t.genre],
      ['Composer', t.composer],
      ['Format', `${t.format.toUpperCase()}${t.codec ? ` · ${t.codec}` : ''}`],
      ['Plays', t.play_count !== undefined ? String(t.play_count) : undefined],
      ['File', t.relative_path],
    ]
    return rows.filter((r): r is [string, string] => !!r[1])
  }
</script>

{#snippet transport(big: boolean)}
  <button
    class="rw-button rw-button-secondary ctl"
    class:big
    type="button"
    onclick={() => player.previous()}
    disabled={!player.hasPrevious}
    aria-label="Previous track"
    title="Previous track (Shift+←)"><Icon name="previous" /></button
  >
  <button
    class="rw-button ctl play"
    class:big
    type="button"
    onclick={() => player.toggle()}
    aria-label={player.playing ? 'Pause' : 'Play'}
    title={`${player.playing ? 'Pause' : 'Play'} (Space)`}
  >
    <Icon name={player.playing ? 'pause' : 'play'} size={big ? '2rem' : '1.25rem'} />
  </button>
  <button
    class="rw-button rw-button-secondary ctl"
    class:big
    type="button"
    onclick={() => player.next()}
    disabled={!player.hasNext}
    aria-label="Next track"
    title="Next track (Shift+→)"><Icon name="next" /></button
  >
{/snippet}

{#snippet modes()}
  <button
    class="toggle"
    type="button"
    onclick={() => player.toggleShuffle()}
    aria-pressed={player.shuffle}
    aria-label="Shuffle"
    title="Shuffle (S)"><Icon name="shuffle" /></button
  >
  <button class="toggle" type="button" onclick={() => player.cycleRepeat()} aria-pressed={player.repeat !== 'off'} aria-label={repeatLabel} title={`${repeatLabel} (R)`}>
    <Icon name={player.repeat === 'one' ? 'repeatOne' : 'repeat'} />
  </button>
{/snippet}

{#snippet volume(id: string)}
  <span class="volume">
    <button class="toggle" type="button" onclick={() => player.toggleMute()} aria-pressed={player.muted} aria-label="Mute" title="Mute (M)">
      <Icon name={player.muted || player.volume === 0 ? 'mute' : 'volume'} />
    </button>
    <input
      {id}
      class="vol"
      type="range"
      min="0"
      max="1"
      step="0.05"
      value={player.muted ? 0 : player.volume}
      oninput={(e) => player.setVolume(Number(e.currentTarget.value))}
      aria-label="Volume"
      aria-valuetext={`${Math.round((player.muted ? 0 : player.volume) * 100)}%`}
    />
  </span>
{/snippet}

<section class="playbar" aria-label="Player" aria-busy={player.loading} hidden={!track} inert={ui.nowPlayingOpen}>
  {#if track}
    <span class="mini-progress" use:progressBar={progressPct} aria-hidden="true"></span>
    <button
      class="now"
      type="button"
      bind:this={openButton}
      onclick={expand}
      onpointerdown={swipeDown}
      onpointerup={(e) => swipeEnd(e, 'up')}
      aria-haspopup="dialog"
      aria-expanded={ui.nowPlayingOpen}
      aria-label={`Open Now Playing: ${track.title}`}
    >
      <span class="mini-art"><Artwork trackId={track.id} revision={track.revision} eager /></span>
      <span class="now-text">
        <span class="title" title={track.title}>{track.title}</span>
        <span class="meta" title={metaLine}>{metaLine || ' '}</span>
      </span>
    </button>

    <button
      class="toggle fav"
      type="button"
      onclick={toggleFavorite}
      aria-pressed={favorite}
      aria-label={`Favorite: ${track.title}`}
      title="Favorite (F)"><Icon name={favorite ? 'heartFilled' : 'heart'} /></button
    >

    <div class="transport">
      <div class="buttons">{@render transport(false)}</div>
      <div class="seekrow">
        <span class="time" aria-label="Elapsed time">{formatTime(player.position)}</span>
        <input
          class="seek"
          type="range"
          min="0"
          max={duration || 0}
          step="1"
          value={player.position}
          oninput={seekInput}
          disabled={!duration || !!player.error}
          aria-label="Seek"
          aria-valuetext={valueText}
        />
        <span class="time" aria-label="Duration">{formatTime(duration || undefined)}</span>
      </div>
    </div>

    <div class="extras">
      <span class="modes">{@render modes()}</span>
      {@render volume('mini-volume')}
      <button
        class="toggle"
        type="button"
        onclick={() => ui.toggleQueue()}
        aria-pressed={ui.queueOpen}
        aria-label="Queue"
        title="Queue (Q)"><Icon name="queue" /></button
      >
    </div>

    <div class="message">
      {#if player.error}
        <p class="error" role="alert">{player.error}</p>
      {:else}
        <p class="status" role="status" aria-live="polite">{status}</p>
      {/if}
      {#if favError}<p class="error" role="alert">{favError}</p>{/if}
    </div>
  {/if}
</section>

{#if ui.nowPlayingOpen && track}
  <div
    class="screen"
    role="dialog"
    aria-modal="true"
    aria-labelledby="np-title"
    tabindex="-1"
    bind:this={screenEl}
    onkeydown={onScreenKeydown}
    onpointerdown={headerSwipeDown}
    onpointerup={(e) => swipeEnd(e, 'down')}
  >
    <header class="screen-head">
      <button
        class="rw-button rw-button-secondary collapse"
        type="button"
        bind:this={closeButton}
        onclick={collapse}
        aria-label="Close Now Playing"
        title="Close (Esc)"
      >
        <Icon name="collapse" />
      </button>
      <p class="label">Now playing</p>
      {#if contextLabel}<p class="label context-label">{contextLabel}</p>{/if}
      <span class="head-actions">
        <button class="toggle" type="button" onclick={() => ui.openQueue()} aria-label="Queue" title="Queue (Q)"><Icon name="queue" /></button>
        <TrackMenu track={track} context={player.context} {onUnauthorized} />
      </span>
    </header>

    <div class="screen-body">
      <div class="art-col">
        {#if showLyrics && track.has_lyrics}
          <div class="lyrics" aria-label="Lyrics">
            {#if lyricsError}<p class="rw-alert" role="alert">{lyricsError}</p>
            {:else if lyrics?.id === track.id}<pre>{lyrics.text ?? ''}</pre>
            {:else}<p class="rw-muted" role="status">Loading lyrics…</p>{/if}
          </div>
        {:else}
          <span class="np-art"><Artwork trackId={track.id} size="large" shape="large" revision={track.revision} eager /></span>
        {/if}
      </div>

      <div class="details">
        <div class="info">
          <div class="title-row">
            <h2 id="np-title" class="np-title">{track.title}</h2>
            <button
              class="toggle fav"
              type="button"
              onclick={toggleFavorite}
              aria-pressed={favorite}
              aria-label={`Favorite: ${track.title}`}
              title="Favorite (F)"><Icon name={favorite ? 'heartFilled' : 'heart'} size="1.5rem" /></button
            >
          </div>
          <p class="np-artist">
            {#if track.artists && track.artists.length > 0}
              {#each track.artists as a, i (a.id)}{#if i > 0}<span aria-hidden="true">, </span>{/if}<a
                  href={artistHref(a.id)}
                  onclick={(e) => {
                    e.preventDefault()
                    go(artistHref(a.id))
                  }}>{a.name}</a
                >{/each}
            {:else}Unknown artist{/if}
          </p>
          {#if track.album}
            <p class="np-album">
              {#if track.album_id}<a
                  href={albumHref(track.album_id)}
                  onclick={(e) => {
                    e.preventDefault()
                    go(albumHref(track.album_id!))
                  }}>{track.album}</a
                >{:else}{track.album}{/if}{#if track.year}<span class="year"> · {track.year}</span>{/if}
            </p>
          {/if}
        </div>

        <div class="progress">
          <input
            class="seek"
            type="range"
            min="0"
            max={duration || 0}
            step="1"
            value={player.position}
            oninput={seekInput}
            disabled={!duration || !!player.error}
            aria-label="Seek"
            aria-valuetext={valueText}
          />
          <div class="times">
            <span class="time" aria-label="Elapsed time">{formatTime(player.position)}</span>
            <span class="time" aria-label="Remaining time">-{formatTime(remaining)}</span>
          </div>
        </div>

        <div class="controls">
          {@render modes()}
          {@render transport(true)}
        </div>

        <div class="secondary">
          {@render volume('np-volume')}
          <label class="speed">
            <span class="rw-label">Speed</span>
            <select class="rw-input" value={player.speed} onchange={(e) => player.setSpeed(Number(e.currentTarget.value))}>
              {#each SPEEDS as s (s)}<option value={s}>{s}×</option>{/each}
            </select>
          </label>
          {#if track.has_lyrics}
            <button class="rw-button rw-button-secondary small" type="button" aria-pressed={showLyrics} onclick={() => (showLyrics = !showLyrics)}>
              <Icon name="lyrics" />{showLyrics ? 'Artwork' : 'Lyrics'}
            </button>
          {/if}
          <span class="seek-steps">
            <button class="rw-button rw-button-secondary small" type="button" onclick={() => player.seekBy(-SEEK_STEP_S)} title="Back 10 seconds (←)">−10s</button>
            <button class="rw-button rw-button-secondary small" type="button" onclick={() => player.seekBy(SEEK_STEP_S)} title="Forward 10 seconds (→)">+10s</button>
          </span>
        </div>

        <div class="screen-message">
          {#if player.error}
            <p class="error" role="alert">{player.error}</p>
          {:else}
            <p class="status">{status}</p>
          {/if}
        </div>

        <details class="track-info">
          <summary>Track information</summary>
          <dl>
            {#each infoRows(track) as [k, v] (k)}<dt>{k}</dt><dd>{v}</dd>{/each}
          </dl>
        </details>
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
    grid-template-columns: minmax(10rem, 1.2fr) auto minmax(16rem, 2fr) auto;
    grid-template-areas: 'now fav transport extras' 'message message message message';
    column-gap: var(--rw-space-3);
    align-items: center;
    padding: var(--rw-space-2) var(--rw-space-4);
    overflow: hidden;
    border: var(--rw-border);
    border-radius: var(--rw-radius-xl);
    background: var(--rw-color-surface);
    box-shadow: var(--rw-shadow);
  }

  .playbar[hidden] {
    display: none;
  }

  .mini-progress {
    position: absolute;
    inset: 0 auto auto 0;
    width: var(--progress, 0%);
    height: 3px;
    background: var(--rw-color-accent);
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
    touch-action: pan-x;
  }

  .now:hover {
    background: var(--rw-color-bg);
  }

  .mini-art {
    flex: none;
    width: 2.75rem;
  }

  .now-text {
    display: grid;
    min-width: 0;
  }

  .title,
  .meta {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .title {
    font-weight: var(--rw-font-weight-black);
  }

  .meta {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .playbar > .fav {
    grid-area: fav;
  }

  .transport {
    grid-area: transport;
    display: grid;
    gap: var(--rw-space-1);
    min-width: 0;
  }

  .buttons {
    display: flex;
    justify-content: center;
    gap: var(--rw-space-2);
  }

  .seekrow {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--rw-space-2);
  }

  .extras {
    grid-area: extras;
    display: flex;
    align-items: center;
    gap: var(--rw-space-1);
  }

  .modes {
    display: flex;
  }

  .ctl {
    display: grid;
    place-items: center;
    width: 2.75rem;
    min-height: 2.75rem;
    padding: 0;
  }

  .ctl.play:not(.big) {
    width: 3.25rem;
  }

  .ctl.big {
    width: 3.5rem;
    height: 3.5rem;
  }

  .ctl.play.big {
    width: 4.75rem;
    height: 4.75rem;
    border-radius: var(--rw-radius-round);
    box-shadow: var(--rw-shadow);
  }

  .toggle {
    display: grid;
    place-items: center;
    width: 2.75rem;
    height: 2.75rem;
    padding: 0;
    border: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-sm);
    background: transparent;
    color: var(--rw-color-muted);
    cursor: pointer;
  }

  .toggle:hover {
    color: var(--rw-color-text);
    border-color: var(--rw-color-border);
  }

  /* On state: accent color plus a filled box, never color alone. */
  .toggle[aria-pressed='true'] {
    border-color: var(--rw-color-accent);
    color: var(--rw-color-accent);
  }

  .volume {
    display: flex;
    align-items: center;
  }

  .vol {
    width: 5.5rem;
    accent-color: var(--rw-color-accent);
  }

  .time {
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    font-variant-numeric: tabular-nums;
  }

  .seek {
    width: 100%;
    min-width: 0;
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
    max-width: 64rem;
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

  .context-label {
    overflow: hidden;
    color: var(--rw-color-muted);
    font-weight: normal;
    letter-spacing: 0.04em;
    text-overflow: ellipsis;
    text-transform: none;
    white-space: nowrap;
  }

  .head-actions {
    display: flex;
    gap: var(--rw-space-1);
    margin-left: auto;
  }

  .screen-body {
    display: grid;
    grid-template-columns: minmax(0, min(28rem, 44vw)) minmax(0, 1fr);
    align-items: center;
    align-content: center;
    gap: var(--rw-space-6);
    width: 100%;
    max-width: 64rem;
    margin: 0 auto;
    padding-block: var(--rw-space-5);
  }

  .np-art {
    display: block;
  }

  .np-art :global(.art) {
    box-shadow: var(--rw-shadow);
  }

  .lyrics {
    max-height: min(28rem, 60dvh);
    overflow-y: auto;
    padding: var(--rw-space-4);
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-surface);
  }

  .lyrics pre {
    margin: 0;
    font: inherit;
    line-height: 1.7;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .details {
    display: grid;
    gap: var(--rw-space-4);
    min-width: 0;
  }

  .info {
    display: grid;
    gap: var(--rw-space-1);
    min-width: 0;
  }

  .title-row {
    display: flex;
    align-items: flex-start;
    gap: var(--rw-space-2);
  }

  .np-title {
    display: -webkit-box;
    flex: 1;
    min-width: 0;
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

  .np-artist a,
  .np-album a {
    color: inherit;
    text-decoration-thickness: 1px;
    text-underline-offset: 0.2em;
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
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-3);
  }

  .secondary {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2) var(--rw-space-4);
  }

  .speed {
    display: flex;
    align-items: center;
    gap: var(--rw-space-2);
  }

  .speed select {
    width: auto;
    min-height: 2.5rem;
    padding-block: 0;
    appearance: auto;
  }

  .small {
    display: inline-flex;
    align-items: center;
    gap: var(--rw-space-1);
    min-height: 2.5rem;
    padding-inline: var(--rw-space-3);
  }

  .seek-steps {
    display: flex;
    gap: var(--rw-space-1);
  }

  .screen-message {
    min-height: 1.5em;
  }

  .track-info summary {
    min-height: 2.75rem;
    padding: var(--rw-space-2) 0;
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    text-transform: uppercase;
    cursor: pointer;
  }

  .track-info dl {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: var(--rw-space-1) var(--rw-space-4);
    margin: 0;
  }

  .track-info dt {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .track-info dd {
    margin: 0;
    overflow-wrap: anywhere;
  }

  /* Tablet: fewer controls in the bar. */
  @media (max-width: 64rem) {
    .playbar {
      grid-template-columns: minmax(0, 1.2fr) auto minmax(12rem, 1.5fr) auto;
    }

    .volume .vol,
    .playbar .modes {
      display: none;
    }
  }

  /* Phones: a compact bar above the bottom navigation. */
  @media (max-width: 48rem) {
    .playbar {
      inset: auto var(--rw-space-2) calc(var(--rw-bottom-nav, 0px) + var(--rw-space-2) + env(safe-area-inset-bottom)) var(--rw-space-2);
      grid-template-columns: minmax(0, 1fr) auto auto;
      grid-template-areas: 'now fav transport' 'message message message';
      padding: var(--rw-space-1) var(--rw-space-2);
      border-radius: var(--rw-radius-lg);
    }

    .seekrow,
    .extras,
    .buttons > :global(button:not(.play)) {
      display: none;
    }

    .screen {
      padding-inline: calc(var(--rw-space-4) + env(safe-area-inset-left)) calc(var(--rw-space-4) + env(safe-area-inset-right));
    }

    .screen-body {
      grid-template-columns: minmax(0, 1fr);
      justify-items: center;
      align-content: start;
      gap: var(--rw-space-5);
      padding-block: var(--rw-space-4);
    }

    .art-col {
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
