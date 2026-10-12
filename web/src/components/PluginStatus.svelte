<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { ApiError, getPlugins, type PluginState, type PluginStatusList } from '../services/api'
  import { sentence } from '../services/validation'

  interface Props {
    onUnauthorized: () => void
  }
  let { onUnauthorized }: Props = $props()

  // Read-only: plugins are installed and enabled by the server's
  // administrator (docs/plugins.md). Every value is shown as plain text.
  let data = $state<PluginStatusList | null>(null)
  let error = $state('')
  let controller: AbortController | undefined

  const labels: Record<PluginState, string> = {
    running: 'Running',
    disabled: 'Installed, not enabled',
    failed: 'Failed to start',
    invalid: 'Invalid',
    incompatible: 'Incompatible',
    unavailable: 'Not in this build',
    missing: 'Enabled, not installed',
  }

  async function load() {
    controller?.abort()
    controller = new AbortController()
    error = ''
    try {
      data = await getPlugins(controller.signal)
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return
      if (err instanceof ApiError && err.status === 401) {
        onUnauthorized()
        return
      }
      error = err instanceof Error ? sentence(err.message) : 'Could not load plugin status.'
    }
  }

  onMount(() => {
    void load()
  })
  onDestroy(() => controller?.abort())
</script>

<section class="group" aria-labelledby="plugins-title">
  <h2 id="plugins-title" class="rw-panel-title">Plugins</h2>
  {#if error}
    <div class="state">
      <p class="rw-alert" role="alert">{error}</p>
      <button class="rw-button rw-button-secondary" type="button" onclick={load}>Try again</button>
    </div>
  {:else if !data}
    <p class="rw-muted" role="status">Loading plugins…</p>
  {:else if !data.configured}
    <p class="rw-muted">Plugins are off. The server administrator can enable trusted plugins; see the plugin documentation.</p>
  {:else if data.items.length === 0}
    <p class="rw-muted">No plugins are installed.</p>
  {:else}
    <ul class="plugins" aria-label="Installed plugins">
      {#each data.items as p (p.id)}
        <li class="plugin">
          <p class="head">
            <span class="name">{p.name ?? p.id}</span>
            {#if p.version}<span class="meta">{p.version}</span>{/if}
            <span class="badge" class:ok={p.state === 'running'}>{labels[p.state] ?? p.state}</span>
          </p>
          {#if p.description}<p class="rw-hint">{p.description}</p>{/if}
          {#if p.error}<p class="problem">{p.error}</p>{/if}
          <p class="meta">ID {p.id}{#if p.author} · {p.author}{/if}</p>
        </li>
      {/each}
    </ul>
    <p class="rw-hint">Plugins are enabled on the server and take effect after a restart. Details are in the server log.</p>
  {/if}
</section>

<style>
  .group {
    display: grid;
    gap: var(--rw-space-3);
    max-width: 36rem;
    margin-bottom: var(--rw-space-6);
  }

  .state {
    display: grid;
    gap: var(--rw-space-3);
    justify-items: start;
  }

  .plugins {
    margin: 0;
    padding: 0;
    border-top: var(--rw-border);
    list-style: none;
  }

  .plugin {
    display: grid;
    gap: var(--rw-space-1);
    padding: var(--rw-space-3) 0;
    border-bottom: 1px solid var(--rw-color-surface);
  }

  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--rw-space-1) var(--rw-space-3);
  }

  .name {
    font-weight: var(--rw-font-weight-bold);
    overflow-wrap: anywhere;
  }

  .meta {
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    overflow-wrap: anywhere;
  }

  /* The state is always written out; color only reinforces it. */
  .badge {
    padding: 0 var(--rw-space-1);
    border: 1px solid var(--rw-color-muted);
    border-radius: var(--rw-radius-xs);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .badge.ok {
    border-color: var(--rw-color-accent);
    background: var(--rw-color-accent);
    color: var(--rw-color-on-accent);
  }

  .problem {
    padding-left: var(--rw-space-2);
    border-left: var(--rw-border-width) solid var(--rw-color-danger);
    font-size: var(--rw-font-size-sm);
  }
</style>
