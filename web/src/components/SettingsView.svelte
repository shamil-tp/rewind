<script lang="ts">
  import { ApiError, savePreferences, type LibrarySortChoice, type Preferences, type ThemeState } from '../services/api'
  import { LIBRARY_SORTS } from '../services/librarySort'
  import type { ThemePreview } from '../services/themes'
  import { sentence } from '../services/validation'
  import PluginStatus from './PluginStatus.svelte'
  import ThemeSettings from './ThemeSettings.svelte'

  interface Props {
    /** The saved preferences (defaults until they are loaded). */
    preferences: Preferences
    /** 'error' when the saved preferences could not be loaded. */
    status: 'loading' | 'ready' | 'error'
    loadError: string
    onRetry: () => void
    onSaved: (preferences: Preferences) => void
    /** The theme state from the server (null until loaded). */
    themes: ThemeState | null
    themesStatus: 'loading' | 'ready' | 'error'
    themesError: string
    onRetryThemes: () => void
    onThemesChanged: (state: ThemeState) => void
    /** Shows a theme or unsaved edits before they are saved; null returns to the saved theme. */
    onPreviewTheme: (preview: ThemePreview | null) => void
    onUnauthorized: () => void
  }
  let {
    preferences,
    status,
    loadError,
    onRetry,
    onSaved,
    themes,
    themesStatus,
    themesError,
    onRetryThemes,
    onThemesChanged,
    onPreviewTheme,
    onUnauthorized,
  }: Props = $props()

  function message(err: unknown): string {
    return err instanceof Error ? sentence(err.message) : 'Unknown error.'
  }

  function isUnauthorized(err: unknown): boolean {
    if (err instanceof ApiError && err.status === 401) {
      onUnauthorized()
      return true
    }
    return false
  }

  // --- Library and playback (saved with the Save button) ---

  // Deriveds change only when the saved value changes, so saving another
  // setting does not discard unsaved edits here.
  const savedSort = $derived(preferences.library_sort)
  const savedAutoplay = $derived(preferences.autoplay_next)
  const savedThreshold = $derived(preferences.play_threshold_percent)
  const savedRecord = $derived(preferences.record_history)
  let sort = $state<LibrarySortChoice>('artist')
  let autoplay = $state(true)
  let threshold = $state(50)
  let record = $state(true)
  let saving = $state(false)
  let error = $state('')
  let saved = $state('')
  $effect(() => {
    sort = savedSort
  })
  $effect(() => {
    autoplay = savedAutoplay
  })
  $effect(() => {
    threshold = savedThreshold
  })
  $effect(() => {
    record = savedRecord
  })

  const dirty = $derived(sort !== savedSort || autoplay !== savedAutoplay || threshold !== savedThreshold || record !== savedRecord)

  async function saveForm(event: SubmitEvent) {
    event.preventDefault()
    if (saving || !dirty || status !== 'ready') return
    saving = true
    error = ''
    saved = ''
    const changes: Partial<Preferences> = {}
    if (sort !== savedSort) changes.library_sort = sort
    if (autoplay !== savedAutoplay) changes.autoplay_next = autoplay
    if (threshold !== savedThreshold) changes.play_threshold_percent = threshold
    if (record !== savedRecord) changes.record_history = record
    try {
      onSaved(await savePreferences(changes))
      saved = 'Settings saved.'
    } catch (err) {
      // The form keeps the unsaved values so the user can retry.
      if (!isUnauthorized(err)) error = `Not saved: ${message(err)}`
    } finally {
      saving = false
    }
  }
</script>

<section class="view" aria-labelledby="settings-title">
  <header class="view-header">
    <h1 id="settings-title" class="view-title" tabindex="-1">Settings</h1>
  </header>

  {#if status === 'loading'}
    <p class="rw-muted" role="status">Loading settings…</p>
  {:else}
    {#if status === 'error'}
      <div class="load-error">
        <p class="rw-alert" role="alert">Could not load your settings: {loadError} Defaults are in use until they load.</p>
        <button class="rw-button rw-button-secondary" type="button" onclick={onRetry}>Try again</button>
      </div>
    {/if}

    <ThemeSettings
      {themes}
      status={themesStatus}
      loadError={themesError}
      onRetry={onRetryThemes}
      onChanged={onThemesChanged}
      onPreview={onPreviewTheme}
      {onUnauthorized}
    />

    <section class="group" aria-labelledby="library-settings-title">
      <h2 id="library-settings-title" class="rw-panel-title">Library and playback</h2>
      <form class="rw-form settings" onsubmit={saveForm}>
        <div class="rw-field">
          <label class="rw-label" for="pref-sort">Library sort order</label>
          <select id="pref-sort" class="rw-input" bind:value={sort} disabled={status !== 'ready'} aria-describedby="pref-sort-hint">
            {#each LIBRARY_SORTS as s (s.value)}
              <option value={s.value}>{s.label}</option>
            {/each}
          </select>
          <p id="pref-sort-hint" class="rw-hint">How the library is sorted when Rewind opens. The menu in the library still changes it for the moment.</p>
        </div>

        <div class="check">
          <input id="pref-autoplay" type="checkbox" bind:checked={autoplay} disabled={status !== 'ready'} aria-describedby="pref-autoplay-hint" />
          <div>
            <label class="rw-label" for="pref-autoplay">Continue playing</label>
            <p id="pref-autoplay-hint" class="rw-hint">When a track ends, play the next available one in the queue.</p>
          </div>
        </div>

        <div class="check">
          <input id="pref-history" type="checkbox" bind:checked={record} disabled={status !== 'ready'} aria-describedby="pref-history-hint" />
          <div>
            <label class="rw-label" for="pref-history">Record listening history</label>
            <p id="pref-history-hint" class="rw-hint">
              Keeps the tracks you play, for History, play counts and top tracks. Turn off for a private mode: nothing new is recorded.
              History is stored only on your server.
            </p>
          </div>
        </div>

        <div class="rw-field">
          <label class="rw-label" for="pref-threshold">Count a play after</label>
          <div class="threshold">
            <input id="pref-threshold" type="range" min="10" max="100" step="5" bind:value={threshold} disabled={status !== 'ready' || !record}
              aria-describedby="pref-threshold-hint" aria-valuetext={`${threshold}% of the track`} />
            <output for="pref-threshold">{threshold}%</output>
          </div>
          <p id="pref-threshold-hint" class="rw-hint">How much of a track must be played before it counts (at most four minutes). Skipped tracks are not counted.</p>
        </div>

        <div class="actions">
          <button class="rw-button" type="submit" disabled={saving || !dirty || status !== 'ready'}>{saving ? 'Saving…' : 'Save'}</button>
        </div>
        {#if error}<p class="rw-alert" role="alert">{error}</p>{/if}
        <p class="saved" role="status">{dirty ? '' : saved}</p>
      </form>
    </section>

    <PluginStatus {onUnauthorized} />
  {/if}
</section>

<style>
  .group {
    display: grid;
    gap: var(--rw-space-4);
    max-width: 36rem;
    margin-bottom: var(--rw-space-6);
  }

  .settings {
    max-width: 32rem;
  }

  .load-error {
    display: grid;
    gap: var(--rw-space-3);
    justify-items: start;
    max-width: 32rem;
    margin-bottom: var(--rw-space-5);
  }

  select.rw-input {
    appearance: auto;
  }

  .check {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: var(--rw-space-3);
    align-items: start;
  }

  .check input {
    width: 1.5rem;
    height: 1.5rem;
    margin: 0;
    accent-color: var(--rw-color-primary);
  }

  .threshold {
    display: flex;
    align-items: center;
    gap: var(--rw-space-3);
  }

  .threshold input {
    flex: 1;
    accent-color: var(--rw-color-accent);
  }

  .threshold output {
    min-width: 3rem;
    font-family: var(--rw-font-mono);
  }

  .actions {
    display: flex;
    gap: var(--rw-space-2);
  }

  .saved {
    min-height: 1.25em;
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }
</style>
