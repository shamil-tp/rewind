<script lang="ts">
  // Artwork of a track (or of an album, artist or playlist through a
  // representative track). The box keeps its square shape while loading, so
  // nothing shifts; the placeholder shows until the image arrives and stays
  // when there is none (404) or it fails to load.
  import { artworkUrl } from '../services/api'
  import Icon, { type IconName } from './Icon.svelte'

  interface Props {
    trackId: number | null | undefined
    size?: 'small' | 'large'
    /** Changes with the track's metadata, so a cached image is refreshed. */
    revision?: number
    /** Placeholder icon. */
    kind?: IconName
    /** Corner style: list thumbnails or large artwork. */
    shape?: 'thumb' | 'large' | 'round'
    eager?: boolean
  }
  let { trackId, size = 'small', revision, kind = 'note', shape = 'thumb', eager = false }: Props = $props()

  const src = $derived(trackId ? artworkUrl(trackId, size, revision) : null)
  let loaded = $state(false)
  let failed = $state(false)
  // A new image starts unloaded again.
  $effect(() => {
    void src
    loaded = false
    failed = false
  })
</script>

<span class="art art-{shape}" class:loaded aria-hidden="true">
  {#if !loaded}
    <span class="placeholder"><Icon name={kind} size="45%" /></span>
  {/if}
  {#if src && !failed}
    <img {src} alt="" loading={eager ? 'eager' : 'lazy'} decoding="async" onload={() => (loaded = true)} onerror={() => (failed = true)} />
  {/if}
</span>

<style>
  .art {
    position: relative;
    display: block;
    flex: none;
    width: 100%;
    aspect-ratio: 1;
    overflow: hidden;
    border: var(--rw-border);
    background: var(--rw-color-secondary);
    color: var(--rw-color-on-secondary);
  }

  .art-thumb {
    border-radius: var(--rw-radius-sm);
  }

  .art-large {
    border-radius: var(--rw-radius-art);
  }

  .art-round {
    border-radius: var(--rw-radius-round);
  }

  .placeholder {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
  }

  img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    opacity: 0;
  }

  .loaded img {
    opacity: 1;
  }
</style>
