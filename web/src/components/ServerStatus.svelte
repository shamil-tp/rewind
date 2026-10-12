<script lang="ts">
  import { onMount } from 'svelte'
  import { getHealth, type Health } from '../services/api'

  type Status =
    | { kind: 'checking' }
    | { kind: 'online'; health: Health }
    | { kind: 'offline'; message: string }

  let status = $state<Status>({ kind: 'checking' })
  let controller: AbortController | undefined

  async function check() {
    controller?.abort()
    controller = new AbortController()
    status = { kind: 'checking' }
    try {
      status = { kind: 'online', health: await getHealth(controller.signal) }
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return
      status = { kind: 'offline', message: err instanceof Error ? err.message : 'Unknown error' }
    }
  }

  onMount(() => {
    check()
    return () => controller?.abort()
  })
</script>

<div class="status" role="status" aria-live="polite">
  {#if status.kind === 'checking'}
    <span class="dot" data-state="checking" aria-hidden="true"></span>
    <span>Checking server…</span>
  {:else if status.kind === 'online'}
    <span class="dot" data-state="online" aria-hidden="true"></span>
    <span>Server online</span>
    <span class="meta">v{status.health.version}</span>
  {:else}
    <span class="dot" data-state="offline" aria-hidden="true"></span>
    <span>Server unreachable: {status.message}</span>
    <button type="button" class="retry" onclick={check}>Retry</button>
  {/if}
</div>

<style>
  .status {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2) var(--rw-space-3);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .dot {
    width: 0.75rem;
    height: 0.75rem;
    flex: none;
    border: 2px solid var(--rw-color-border);
    border-radius: var(--rw-radius-round);
    background: transparent;
  }

  .dot[data-state='online'] {
    background: var(--rw-color-accent);
  }

  .dot[data-state='offline'] {
    background: var(--rw-color-danger);
  }

  .meta {
    color: var(--rw-color-muted);
  }

  .retry {
    min-height: 2.75rem;
    padding: var(--rw-space-1) var(--rw-space-4);
    border: var(--rw-border);
    border-radius: var(--rw-radius-md);
    background: var(--rw-color-primary);
    color: var(--rw-color-on-primary);
    font-family: var(--rw-font-mono);
    font-weight: var(--rw-font-weight-bold);
    text-transform: uppercase;
    cursor: pointer;
  }

  .retry:hover {
    background: var(--rw-color-text);
  }
</style>
