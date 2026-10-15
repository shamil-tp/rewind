<script lang="ts">
  // The play queue: what is playing, what comes next and what was played.
  // It edits the shared player's queue only; reordering never changes the
  // current track. Drag and drop works with a mouse; the ↑/↓ buttons do the
  // same for keyboard and touch.
  import { tick } from 'svelte'
  import { addTracksToPlaylist, ApiError, createPlaylist } from '../services/api'
  import { formatTime } from '../services/format'
  import { listening } from '../services/listening.svelte'
  import { player, type Entry } from '../services/player.svelte'
  import { ui } from '../services/ui.svelte'
  import { sentence } from '../services/validation'
  import Artwork from './Artwork.svelte'
  import Icon from './Icon.svelte'

  interface Props {
    onUnauthorized: () => void
  }
  let { onUnauthorized }: Props = $props()

  let panel: HTMLElement | undefined = $state()
  let confirmClear = $state(false)
  let saving = $state(false)
  let saveName = $state('')
  let saveError = $state('')
  let showPlayed = $state(false)
  let dragFrom = $state<number | null>(null)
  let dropAt = $state<number | null>(null)

  const played = $derived(player.entries.slice(0, Math.max(player.index, 0)))
  const upcoming = $derived(player.entries.slice(player.index + 1))
  const totalMs = $derived(upcoming.reduce((s, e) => s + (e.track.duration_ms ?? 0), 0))

  $effect(() => {
    if (ui.queueOpen) void tick().then(() => panel?.querySelector<HTMLElement>('.close')?.focus())
  })

  function onKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && !event.defaultPrevented) {
      event.preventDefault()
      ui.closeQueue()
    }
  }

  // Absolute queue index of an upcoming row.
  const abs = (i: number) => player.index + 1 + i

  async function moveBy(i: number, by: -1 | 1) {
    const from = abs(i)
    player.moveEntry(from, from + by)
    await tick()
    panel?.querySelector<HTMLElement>(`[data-row="${from + by}"] .${by < 0 ? 'up' : 'down'}`)?.focus()
  }

  function drop(to: number) {
    if (dragFrom !== null && dragFrom !== to) player.moveEntry(dragFrom, to)
    dragFrom = dropAt = null
  }

  function label(e: Entry) {
    return `${e.track.title}${e.track.artist ? ` by ${e.track.artist}` : ''}`
  }

  async function saveAsPlaylist(event: SubmitEvent) {
    event.preventDefault()
    if (saving) return
    saveError = ''
    const ids = [...new Set(player.entries.map((e) => e.track.id))]
    if (ids.length === 0) return
    saving = true
    try {
      const pl = await createPlaylist(saveName.trim() || 'Saved queue')
      await addTracksToPlaylist(pl.id, ids)
      listening.announce(`Saved the queue as “${pl.name}” (${ids.length} tracks).`)
      saveName = ''
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      else saveError = err instanceof Error ? sentence(err.message) : 'Could not save the playlist.'
    } finally {
      saving = false
    }
  }
</script>

{#if ui.queueOpen}
  <button class="backdrop" type="button" tabindex="-1" aria-hidden="true" onclick={() => ui.closeQueue()}></button>
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions (Escape closes the panel) -->
  <div class="queue" role="dialog" tabindex="-1" aria-labelledby="queue-title" bind:this={panel} onkeydown={onKeydown}>
    <header class="head">
      <h2 id="queue-title" class="rw-panel-title">Queue</h2>
      <span class="count">{player.entries.length} {player.entries.length === 1 ? 'track' : 'tracks'}</span>
      <button class="close icon-btn" type="button" onclick={() => ui.closeQueue()} aria-label="Close the queue" title="Close (Esc)">
        <Icon name="close" />
      </button>
    </header>

    {#if player.entries.length === 0}
      <p class="empty">The queue is empty. Play an album, a playlist or any track, or use “Add to queue” in a track's menu.</p>
    {:else}
      {#if player.context?.name}<p class="rw-hint">Playing from {player.context.name}</p>{/if}

      <h3 class="rw-label">Now playing</h3>
      {#if player.current}
        {@const e = player.entries[player.index]}
        <div class="row current">
          <span class="thumb"><Artwork trackId={e.track.id} revision={e.track.revision} /></span>
          <span class="text"><span class="t">{e.track.title}</span><span class="s">{e.track.artist ?? 'Unknown artist'}</span></span>
          <span class="len">{player.playing ? 'Playing' : 'Paused'}</span>
          <button class="icon-btn" type="button" onclick={() => player.removeAt(player.index)} aria-label={`Remove ${label(e)} from the queue`}>
            <Icon name="close" />
          </button>
        </div>
      {/if}

      <div class="section-head">
        <h3 class="rw-label">Up next</h3>
        <span class="count">{upcoming.length} · {formatTime(totalMs / 1000)}</span>
      </div>
      {#if upcoming.length === 0}
        <p class="rw-muted">Nothing after this track{player.repeat === 'all' ? '; repeat all starts the queue again' : ''}.</p>
      {:else}
        <ol class="list" aria-label="Up next">
          {#each upcoming as e, i (e.key)}
            {@const at = abs(i)}
            <li
              class="row"
              class:drop={dropAt === at}
              class:unavailable={!e.track.available}
              data-row={at}
              draggable="true"
              ondragstart={(ev) => {
                dragFrom = at
                ev.dataTransfer?.setData('text/plain', String(at))
              }}
              ondragover={(ev) => {
                ev.preventDefault()
                dropAt = at
              }}
              ondragleave={() => (dropAt = dropAt === at ? null : dropAt)}
              ondrop={(ev) => {
                ev.preventDefault()
                drop(at)
              }}
              ondragend={() => (dragFrom = dropAt = null)}
            >
              <span class="grip" aria-hidden="true"><Icon name="drag" /></span>
              <button class="play" type="button" onclick={() => player.jumpTo(at)} disabled={!e.track.available} aria-label={`Play ${label(e)} now`}>
                <span class="thumb"><Artwork trackId={e.track.id} revision={e.track.revision} /></span>
                <span class="text">
                  <span class="t">{e.track.title}</span>
                  <span class="s">{e.track.available ? (e.track.artist ?? 'Unknown artist') : 'Unavailable: it will be skipped'}</span>
                </span>
              </button>
              <span class="len">{formatTime(e.track.duration_ms ? e.track.duration_ms / 1000 : undefined)}</span>
              <span class="moves">
                <button class="icon-btn up" type="button" onclick={() => moveBy(i, -1)} disabled={i === 0} aria-label={`Move ${label(e)} up`}>
                  <Icon name="up" />
                </button>
                <button class="icon-btn down" type="button" onclick={() => moveBy(i, 1)} disabled={i === upcoming.length - 1} aria-label={`Move ${label(e)} down`}>
                  <Icon name="down" />
                </button>
                <button class="icon-btn" type="button" onclick={() => player.removeAt(at)} aria-label={`Remove ${label(e)} from the queue`}>
                  <Icon name="close" />
                </button>
              </span>
            </li>
          {/each}
        </ol>
      {/if}

      {#if played.length > 0}
        <button class="rw-button rw-button-secondary small" type="button" aria-expanded={showPlayed} onclick={() => (showPlayed = !showPlayed)}>
          {showPlayed ? 'Hide' : 'Show'} played ({played.length})
        </button>
        {#if showPlayed}
          <ol class="list played" aria-label="Played">
            {#each played as e, i (e.key)}
              <li class="row">
                <button class="play" type="button" onclick={() => player.jumpTo(i)} disabled={!e.track.available} aria-label={`Play ${label(e)} again`}>
                  <span class="text"><span class="t">{e.track.title}</span><span class="s">{e.track.artist ?? 'Unknown artist'}</span></span>
                </button>
              </li>
            {/each}
          </ol>
        {/if}
      {/if}

      <div class="actions">
        <button class="rw-button rw-button-secondary small" type="button" onclick={() => player.clearUpcoming()} disabled={upcoming.length === 0}>
          Clear upcoming
        </button>
        {#if confirmClear}
          <span class="confirm" role="alertdialog" aria-label="Clear the queue?">
            <span>Clear the queue and stop playback?</span>
            <button
              class="rw-button small"
              type="button"
              onclick={() => {
                player.clear()
                confirmClear = false
              }}>Clear queue</button
            >
            <button class="rw-button rw-button-secondary small" type="button" onclick={() => (confirmClear = false)}>Keep</button>
          </span>
        {:else}
          <button class="rw-button rw-button-secondary small" type="button" onclick={() => (confirmClear = true)}>Clear queue…</button>
        {/if}
      </div>

      <form class="save" onsubmit={saveAsPlaylist}>
        <label class="rw-label" for="queue-playlist-name">Save as playlist</label>
        <div class="save-row">
          <input id="queue-playlist-name" class="rw-input" maxlength="100" placeholder="Saved queue" bind:value={saveName} />
          <button class="rw-button small" type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
        </div>
        <p class="rw-hint">Tracks queued more than once are saved once.</p>
        {#if saveError}<p class="rw-alert" role="alert">{saveError}</p>{/if}
      </form>
    {/if}
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 34;
    border: 0;
    background: var(--rw-color-bg);
    opacity: 0.6;
  }

  .queue {
    position: fixed;
    inset: 0 0 0 auto;
    z-index: 35;
    display: flex;
    flex-direction: column;
    gap: var(--rw-space-3);
    width: min(28rem, 100vw);
    padding: calc(var(--rw-space-4) + env(safe-area-inset-top)) var(--rw-space-4) calc(var(--rw-space-4) + env(safe-area-inset-bottom));
    overflow-y: auto;
    border-left: var(--rw-border);
    background: var(--rw-color-bg);
    color: var(--rw-color-text);
  }

  .head,
  .section-head {
    display: flex;
    align-items: center;
    gap: var(--rw-space-3);
  }

  .head .close {
    margin-left: auto;
  }

  .section-head .count {
    margin-left: auto;
  }

  .count {
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  .empty {
    color: var(--rw-color-muted);
  }

  .list {
    display: grid;
    gap: var(--rw-space-1);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .row {
    display: flex;
    align-items: center;
    gap: var(--rw-space-2);
    min-width: 0;
    padding: var(--rw-space-1);
    border: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-sm);
  }

  .row.current {
    border-color: var(--rw-color-accent);
    background: var(--rw-color-surface);
  }

  .row.drop {
    border-color: var(--rw-color-primary);
  }

  .row.unavailable {
    color: var(--rw-color-muted);
  }

  .grip {
    flex: none;
    color: var(--rw-color-muted);
    cursor: grab;
  }

  .play {
    display: flex;
    flex: 1;
    align-items: center;
    gap: var(--rw-space-2);
    min-width: 0;
    min-height: 2.75rem;
    padding: 0;
    border: 0;
    background: transparent;
    color: inherit;
    text-align: left;
    cursor: pointer;
  }

  .thumb {
    flex: none;
    width: 2.5rem;
  }

  .text {
    display: grid;
    flex: 1;
    min-width: 0;
  }

  .t,
  .s {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .t {
    font-weight: var(--rw-font-weight-bold);
  }

  .s,
  .len {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .len {
    flex: none;
    font-family: var(--rw-font-mono);
  }

  .moves {
    display: flex;
    flex: none;
  }

  .icon-btn {
    display: grid;
    place-items: center;
    width: 2.5rem;
    height: 2.5rem;
    padding: 0;
    border: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-sm);
    background: transparent;
    color: var(--rw-color-muted);
    cursor: pointer;
  }

  .icon-btn:hover:not(:disabled) {
    border-color: var(--rw-color-border);
    color: var(--rw-color-text);
  }

  .icon-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
  }

  .confirm {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2);
    padding: var(--rw-space-2);
    border: var(--rw-border-width) solid var(--rw-color-danger);
    border-radius: var(--rw-radius-md);
  }

  .small {
    min-height: 2.5rem;
    padding-inline: var(--rw-space-3);
  }

  .save {
    display: grid;
    gap: var(--rw-space-1);
    padding-top: var(--rw-space-3);
    border-top: 1px solid var(--rw-color-surface);
  }

  .save-row {
    display: flex;
    gap: var(--rw-space-2);
  }

  @media (max-width: 48rem) {
    .queue {
      inset: 0;
      width: 100vw;
      border-left: 0;
    }
  }
</style>
