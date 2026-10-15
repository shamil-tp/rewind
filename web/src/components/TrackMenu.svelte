<script lang="ts" module>
  /** An extra menu action from the surrounding view (e.g. "Remove from playlist"). */
  export interface MenuAction {
    label: string
    onSelect: () => void
    danger?: boolean
  }
</script>

<script lang="ts">
  import { tick } from 'svelte'
  import type { PlaybackContext, Track } from '../services/api'
  import { listening } from '../services/listening.svelte'
  import { player } from '../services/player.svelte'
  import { albumHref, artistHref, navigate, ui } from '../services/ui.svelte'
  import { sentence } from '../services/validation'
  import AddToPlaylist from './AddToPlaylist.svelte'
  import Icon from './Icon.svelte'

  interface Props {
    track: Track
    context?: PlaybackContext | null
    extra?: MenuAction[]
    onUnauthorized: () => void
    /** Hide navigation to the album or artist (on that page already). */
    hideAlbum?: boolean
    hideArtist?: string
    /** Bound by the parent to open the menu from a right-click. */
    open?: boolean
  }
  let { track, context = null, extra = [], onUnauthorized, hideAlbum = false, hideArtist, open = $bindable(false) }: Props = $props()

  let trigger: HTMLButtonElement | undefined = $state()
  let menu: HTMLDivElement | undefined = $state()
  let adding = $state(false)
  let error = $state('')

  const favorite = $derived(listening.isFavorite(track))
  const artists = $derived((track.artists ?? []).filter((a) => a.id !== hideArtist).slice(0, 3))

  $effect(() => {
    if (open) void focusFirst()
  })

  async function focusFirst() {
    await tick()
    menu?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
  }

  function close(returnFocus = true) {
    open = false
    adding = false
    error = ''
    if (returnFocus) trigger?.focus()
  }

  function run(fn: () => void | Promise<void>) {
    return async () => {
      try {
        await fn()
        close()
      } catch (err) {
        if ((err as { status?: number }).status === 401) {
          onUnauthorized()
          return
        }
        error = err instanceof Error ? sentence(err.message) : 'Something went wrong.'
      }
    }
  }

  function go(hash: string) {
    return () => {
      close(false)
      ui.nowPlayingOpen = false
      navigate(hash)
    }
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      close()
      return
    }
    if (adding || !['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
    const items = [...(menu?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]
    const i = items.indexOf(document.activeElement as HTMLElement)
    const next =
      event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (i + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
    items[next]?.focus()
    event.preventDefault()
  }

  function outside(event: PointerEvent) {
    if (open && menu && !menu.contains(event.target as Node) && !trigger?.contains(event.target as Node)) close(false)
  }
</script>

<svelte:window onpointerdown={outside} />

<span class="wrap">
  <button
    class="trigger"
    type="button"
    bind:this={trigger}
    onclick={() => (open ? close() : (open = true))}
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label={`More actions for ${track.title}`}
    title="More actions"
  >
    <Icon name="more" />
  </button>

  {#if open}
    <button class="backdrop" type="button" tabindex="-1" aria-hidden="true" onclick={() => close()}></button>
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions (keyboard navigation of the menu) -->
    <div class="menu" bind:this={menu} role="menu" tabindex="-1" aria-label={`Actions for ${track.title}`} onkeydown={onKeydown}>
      <p class="menu-title">{track.title}</p>
      {#if adding}
        <AddToPlaylist
          {track}
          onDone={(message) => {
            listening.announce(message)
            close()
          }}
          onCancel={() => (adding = false)}
          {onUnauthorized}
        />
      {:else}
        {#if track.available}
          <button role="menuitem" type="button" onclick={run(() => player.playNow(track, context))}>Play now</button>
          <button role="menuitem" type="button" onclick={run(() => player.playNext([track]))}>Play next</button>
          <button role="menuitem" type="button" onclick={run(() => player.addToQueue([track]))}>Add to queue</button>
        {/if}
        <button role="menuitem" type="button" onclick={() => (adding = true)}>Add to playlist…</button>
        <button role="menuitem" type="button" onclick={run(() => listening.toggleFavorite(track).then(() => {}))}>
          {favorite ? 'Remove from favorites' : 'Add to favorites'}
        </button>
        {#if track.album_id && !hideAlbum}
          <button role="menuitem" type="button" onclick={go(albumHref(track.album_id))}>Go to album</button>
        {/if}
        {#each artists as a (a.id)}
          <button role="menuitem" type="button" onclick={go(artistHref(a.id))}>Go to {a.name}</button>
        {/each}
        <button
          role="menuitem"
          type="button"
          onclick={() => {
            close(false)
            ui.editTrack(track.id)
          }}>Edit details…</button
        >
        {#each extra as action (action.label)}
          <button role="menuitem" type="button" class:danger={action.danger} onclick={run(action.onSelect)}>{action.label}</button>
        {/each}
      {/if}
      {#if error}<p class="rw-field-error" role="alert">{error}</p>{/if}
    </div>
  {/if}
</span>

<style>
  .wrap {
    position: relative;
    display: inline-flex;
  }

  .trigger {
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

  .trigger:hover,
  .trigger[aria-expanded='true'] {
    border-color: var(--rw-color-border);
    color: var(--rw-color-text);
  }

  .backdrop {
    display: none;
  }

  .menu {
    position: absolute;
    top: 100%;
    right: 0;
    z-index: 40;
    display: grid;
    min-width: 14rem;
    max-width: min(22rem, calc(100vw - 2 * var(--rw-space-3)));
    max-height: min(70vh, 32rem);
    overflow-y: auto;
    padding: var(--rw-space-1);
    border: var(--rw-border);
    border-radius: var(--rw-radius-md);
    background: var(--rw-color-surface);
    box-shadow: var(--rw-shadow);
    text-align: left;
  }

  .menu-title {
    overflow: hidden;
    padding: var(--rw-space-1) var(--rw-space-2);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.75rem;
    text-overflow: ellipsis;
    text-transform: uppercase;
    white-space: nowrap;
  }

  [role='menuitem'] {
    min-height: 2.75rem;
    padding: var(--rw-space-2) var(--rw-space-3);
    border: 0;
    border-radius: var(--rw-radius-sm);
    background: transparent;
    color: var(--rw-color-text);
    text-align: left;
    cursor: pointer;
  }

  [role='menuitem']:hover,
  [role='menuitem']:focus-visible {
    background: var(--rw-color-bg);
  }

  [role='menuitem'].danger {
    color: var(--rw-color-danger);
  }

  /* Phones: a bottom sheet over a dimmed page. */
  @media (max-width: 48rem) {
    .backdrop {
      position: fixed;
      inset: 0;
      z-index: 39;
      display: block;
      border: 0;
      background: var(--rw-color-bg);
      opacity: 0.7;
    }

    .menu {
      position: fixed;
      inset: auto 0 0 0;
      max-width: none;
      max-height: 75dvh;
      padding: var(--rw-space-2) var(--rw-space-2) calc(var(--rw-space-3) + env(safe-area-inset-bottom));
      border-radius: var(--rw-radius-lg) var(--rw-radius-lg) 0 0;
    }
  }
</style>
