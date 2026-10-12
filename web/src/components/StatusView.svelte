<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import {
    ApiError,
    getLibraryRoots,
    getLibrarySummary,
    startScan,
    type LibraryRoot,
    type LibrarySummary,
  } from '../services/api'
  import { formatTime } from '../services/format'
  import { sentence } from '../services/validation'

  interface Props {
    onUnauthorized: () => void
    /** Called after a scan that this view watched has finished. */
    onScanFinished: () => void
    /** How often (ms) the overview is polled while a scan is running or queued. */
    pollInterval?: number
  }
  let { onUnauthorized, onScanFinished, pollInterval = 2000 }: Props = $props()

  let summary = $state<LibrarySummary | null>(null)
  let roots = $state<LibraryRoot[]>([])
  let error = $state('')
  let scanMessage = $state('')
  let requesting = $state(false)
  let poll: ReturnType<typeof setTimeout> | undefined
  let watching = false
  let destroyed = false

  const scanning = $derived(!!summary?.scan.running)

  function handle(err: unknown): boolean {
    if (err instanceof ApiError && err.status === 401) {
      onUnauthorized()
      return true
    }
    return false
  }

  async function refresh() {
    try {
      const [s, r] = await Promise.all([getLibrarySummary(), getLibraryRoots()])
      if (destroyed) return
      summary = s
      roots = r
      error = ''
      schedule()
    } catch (err) {
      if (destroyed || handle(err)) return
      error = err instanceof Error ? sentence(err.message) : 'Could not load the library status.'
    }
  }

  /** Polls only while the server reports a scan running or queued. */
  function schedule() {
    clearTimeout(poll)
    if (summary?.scan.running) {
      watching = true
      poll = setTimeout(refresh, pollInterval)
    } else if (watching) {
      watching = false
      scanMessage = 'Scan finished. See the results below.'
      onScanFinished()
    }
  }

  async function rescan() {
    if (requesting || scanning) return
    requesting = true
    scanMessage = ''
    let accepted = false
    try {
      const res = await startScan()
      accepted = true
      scanMessage =
        res.already_running.length > 0
          ? 'Scan requested; some folders were already being scanned.'
          : 'Scan requested. It runs in the background.'
    } catch (err) {
      if (handle(err)) return
      if (err instanceof ApiError && err.code === 'scan_in_progress') {
        accepted = true // a scan is running: watch it like our own
        scanMessage = 'A scan is already running.'
      } else if (err instanceof ApiError && err.code === 'no_library_roots') {
        scanMessage = 'No music folders are configured. Set REWIND_LIBRARY_ROOTS and restart Rewind.'
      } else {
        scanMessage = err instanceof Error ? sentence(err.message) : 'The scan could not be started.'
      }
    } finally {
      requesting = false
    }
    // Completion is reported only when the server says the scan has ended,
    // never merely because the request was accepted.
    watching = accepted
    await refresh()
  }

  onMount(() => {
    void refresh()
  })

  onDestroy(() => {
    destroyed = true
    clearTimeout(poll)
  })

  function when(iso: string | undefined): string {
    return iso ? new Date(iso).toLocaleString() : 'never'
  }

  function hours(ms: number): string {
    return formatTime(ms / 1000)
  }

  const statusLabels: Record<string, string> = {
    available: 'Available',
    unavailable: 'Unavailable',
    pending: 'Not scanned yet',
    running: 'Running',
    completed: 'Completed',
    partial: 'Partial',
    failed: 'Failed',
    interrupted: 'Interrupted',
  }
</script>

<section class="view" aria-labelledby="status-title">
  <header class="view-header">
    <h1 id="status-title" class="view-title" tabindex="-1">Status</h1>
    {#if summary}
      <p class="count">Last successful scan: {when(summary.last_successful_scan_at)}</p>
    {/if}
  </header>

  {#if error}
    <div class="state">
      <p class="rw-alert" role="alert">{error}</p>
      <button class="rw-button rw-button-secondary" type="button" onclick={refresh}>Try again</button>
    </div>
  {:else if !summary}
    <p class="rw-muted" role="status">Loading status…</p>
  {:else}
    <dl class="figures">
      <div><dt>Tracks</dt><dd>{summary.tracks}</dd></div>
      <div><dt>Artists</dt><dd>{summary.artists}</dd></div>
      <div><dt>Albums</dt><dd>{summary.albums}</dd></div>
      <div><dt>Total time</dt><dd>{hours(summary.total_duration_ms)}</dd></div>
      {#if summary.missing_tracks > 0}
        <div><dt>Missing files</dt><dd>{summary.missing_tracks}</dd></div>
      {/if}
      {#if summary.tracks !== summary.available_tracks}
        <div><dt>On unavailable drives</dt><dd>{summary.tracks - summary.available_tracks}</dd></div>
      {/if}
    </dl>

    <div class="scan">
      <button class="rw-button" type="button" onclick={rescan} disabled={requesting || scanning} aria-describedby="scan-state">
        {requesting ? 'Requesting…' : scanning ? 'Scanning…' : 'Rescan library'}
      </button>
      <p id="scan-state" class="rw-muted" role="status" aria-live="polite">
        {#if scanning}
          Scanning
          {#each summary.scan.active as a (a.scan_id)}
            {roots.find((r) => r.id === a.root_id)?.name ?? `folder ${a.root_id}`}: {a.counts.files_found} files checked.
          {/each}
          {#if summary.scan.queued.length > 0}{summary.scan.queued.length} more waiting.{/if}
        {:else if scanMessage}
          {scanMessage}
        {/if}
      </p>
    </div>

    <h2 class="section-title">Music folders</h2>
    {#if roots.length === 0}
      <p class="rw-muted">No music folders are configured. Set <code>REWIND_LIBRARY_ROOTS</code> and restart Rewind.</p>
    {:else}
      <ul class="roots">
        {#each roots as root (root.id)}
          <li class="root" class:problem={root.status === 'unavailable' || !root.enabled}>
            <div class="root-head">
              <span class="root-name">{root.name}</span>
              <span class="pill" data-status={root.enabled ? root.status : 'disabled'}>
                {root.enabled ? statusLabels[root.status] : 'No longer configured'}
              </span>
              {#if root.scanning}<span class="pill" data-status="running">Scanning</span>{/if}
              {#if root.queued}<span class="pill">Waiting to scan</span>{/if}
            </div>
            <p class="root-detail">
              {root.track_count}
              {root.track_count === 1 ? 'track' : 'tracks'}{root.missing_count > 0 ? `, ${root.missing_count} missing` : ''}
              {#if root.status === 'unavailable'} · kept while the drive is disconnected{/if}
            </p>
            {#if root.status_message && root.status !== 'available'}
              <p class="root-detail">{root.status_message}</p>
            {/if}
            {#if root.last_scan}
              <p class="root-detail">
                Last scan: {statusLabels[root.last_scan.status] ?? root.last_scan.status},
                {when(root.last_scan.finished_at ?? root.last_scan.started_at)}
                {#if root.last_scan.status !== 'running'}
                  · {root.last_scan.counts.tracks_added} added, {root.last_scan.counts.tracks_updated} updated,
                  {root.last_scan.counts.tracks_missing} newly missing{#if root.last_scan.counts.files_failed > 0}, {root.last_scan.counts.files_failed} unreadable{/if}
                {/if}
              </p>
              {#if root.last_scan.error}
                <p class="root-detail scan-error">{root.last_scan.error}</p>
              {/if}
            {:else}
              <p class="root-detail">Not scanned yet.</p>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</section>

<style>
  .state {
    display: grid;
    gap: var(--rw-space-3);
    justify-items: start;
  }

  .figures {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-5) var(--rw-space-6);
    margin: 0 0 var(--rw-space-5);
  }

  .figures dt {
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }

  .figures dd {
    margin: 0;
    font-size: var(--rw-font-size-lg);
    font-weight: var(--rw-font-weight-black);
    font-variant-numeric: tabular-nums;
  }

  .scan {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-3) var(--rw-space-4);
    padding: var(--rw-space-4) 0;
    border-top: var(--rw-border);
    border-bottom: var(--rw-border);
    margin-bottom: var(--rw-space-5);
  }

  .section-title {
    margin-bottom: var(--rw-space-3);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }

  .roots {
    display: grid;
    gap: var(--rw-space-3);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .root {
    padding: var(--rw-space-3) var(--rw-space-4);
    border-left: var(--rw-border-width) solid var(--rw-color-accent);
    background: var(--rw-color-surface);
  }

  .root.problem {
    border-left-color: var(--rw-color-danger);
  }

  .root-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2);
  }

  .root-name {
    font-weight: var(--rw-font-weight-black);
    overflow-wrap: anywhere;
  }

  .pill {
    padding: 0 var(--rw-space-2);
    border: 1px solid var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .pill[data-status='available'] {
    border-color: var(--rw-color-accent);
  }

  .pill[data-status='unavailable'],
  .pill[data-status='disabled'] {
    border-color: var(--rw-color-danger);
  }

  .root-detail {
    margin-top: var(--rw-space-1);
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
    overflow-wrap: anywhere;
  }

  .scan-error {
    color: var(--rw-color-text);
  }
</style>
