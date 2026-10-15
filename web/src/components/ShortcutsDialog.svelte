<script lang="ts">
  import { tick } from 'svelte'
  import { SHORTCUTS } from '../services/shortcuts'
  import { ui } from '../services/ui.svelte'

  let closeButton: HTMLButtonElement | undefined = $state()
  $effect(() => {
    void tick().then(() => closeButton?.focus())
  })

  function onKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      ui.closeShortcuts()
    }
  }
</script>

<button class="backdrop" type="button" tabindex="-1" aria-hidden="true" onclick={() => ui.closeShortcuts()}></button>
<!-- svelte-ignore a11y_no_noninteractive_element_interactions (Escape closes the dialog) -->
<div class="dialog" role="dialog" tabindex="-1" aria-modal="true" aria-labelledby="shortcuts-title" onkeydown={onKeydown}>
  <h2 id="shortcuts-title" class="rw-panel-title">Keyboard shortcuts</h2>
  <p class="rw-hint">Shortcuts work anywhere except while typing in a text field. Every action also has a button.</p>
  <dl>
    {#each SHORTCUTS as s (s.keys)}
      <dt><kbd>{s.keys}</kbd></dt>
      <dd>{s.action}</dd>
    {/each}
  </dl>
  <button class="rw-button" type="button" bind:this={closeButton} onclick={() => ui.closeShortcuts()}>Close</button>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 44;
    border: 0;
    background: var(--rw-color-bg);
    opacity: 0.7;
  }

  .dialog {
    position: fixed;
    inset: 50% auto auto 50%;
    z-index: 45;
    display: grid;
    gap: var(--rw-space-3);
    width: min(32rem, calc(100vw - 2 * var(--rw-space-3)));
    max-height: calc(100dvh - 2 * var(--rw-space-3));
    padding: var(--rw-space-panel);
    overflow-y: auto;
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-surface);
    box-shadow: var(--rw-shadow);
    transform: translate(-50%, -50%);
  }

  dl {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: var(--rw-space-2) var(--rw-space-4);
    margin: 0;
  }

  dd {
    margin: 0;
  }

  kbd {
    padding: 0 var(--rw-space-1);
    border: var(--rw-border-width) solid var(--rw-color-border);
    border-radius: var(--rw-radius-xs);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    white-space: nowrap;
  }

  .rw-button {
    justify-self: start;
  }
</style>
