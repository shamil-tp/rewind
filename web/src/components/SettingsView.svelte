<script lang="ts">
  import { ApiError, savePreferences, type LibrarySortChoice, type Preferences } from '../services/api'
  import { LIBRARY_SORTS } from '../services/librarySort'
  import { accentProblem, resolveTheme, THEMES, themeById, type ThemeColors, type ThemeId } from '../services/themes'
  import { sentence } from '../services/validation'
  import PluginStatus from './PluginStatus.svelte'

  interface Props {
    /** The saved preferences (defaults until they are loaded). */
    preferences: Preferences
    /** 'error' when the saved preferences could not be loaded. */
    status: 'loading' | 'ready' | 'error'
    loadError: string
    onRetry: () => void
    onSaved: (preferences: Preferences) => void
    /** Shows a theme before it is saved; null returns to the saved one. */
    onPreviewTheme: (id: ThemeId | null) => void
    onUnauthorized: () => void
  }
  let { preferences, status, loadError, onRetry, onSaved, onPreviewTheme, onUnauthorized }: Props = $props()

  function message(err: unknown): string {
    return err instanceof Error ? sentence(err.message) : 'Unknown error.'
  }

  /** Saves changes and passes the stored result to the parent; throws on failure. */
  async function save(changes: Partial<Preferences>): Promise<Preferences> {
    const result = await savePreferences(changes)
    onSaved(result)
    return result
  }

  function isUnauthorized(err: unknown): boolean {
    if (err instanceof ApiError && err.status === 401) {
      onUnauthorized()
      return true
    }
    return false
  }

  // --- Theme: applied at once, saved at once ---

  const savedTheme = $derived(themeById(preferences.theme).id)
  // A theme chosen but not yet confirmed by the server.
  let pendingTheme = $state<ThemeId | null>(null)
  const selectedTheme = $derived(pendingTheme ?? savedTheme)
  let savingTheme = false
  let appearanceStatus = $state('')
  let appearanceError = $state('')

  async function chooseTheme(id: ThemeId) {
    appearanceError = ''
    appearanceStatus = ''
    pendingTheme = id
    onPreviewTheme(id)
    // One request at a time; the loop saves the latest choice last, so quick
    // changes (arrow keys) cannot leave an older theme saved.
    if (savingTheme) return
    savingTheme = true
    try {
      while (pendingTheme !== null) {
        const target: ThemeId = pendingTheme
        appearanceStatus = 'Saving theme…'
        await save({ theme: target })
        if (pendingTheme === target) {
          pendingTheme = null
          onPreviewTheme(null)
          appearanceStatus = `Theme saved: ${themeById(target).name}.`
        }
      }
    } catch (err) {
      pendingTheme = null
      onPreviewTheme(null)
      appearanceStatus = ''
      if (!isUnauthorized(err)) {
        appearanceError = `Theme not saved: ${message(err)} The saved theme is shown again.`
      }
    } finally {
      savingTheme = false
    }
  }

  // Preview swatches are set through the CSS object model, which the
  // Content-Security-Policy allows (inline style attributes are blocked).
  function swatch(node: HTMLElement, colors: ThemeColors) {
    node.style.setProperty('--swatch-bg', colors.bg)
    node.style.setProperty('--swatch-surface', colors.surface)
    node.style.setProperty('--swatch-text', colors.text)
    node.style.setProperty('--swatch-accent', colors.accent)
    node.style.setProperty('--swatch-border', colors.border)
  }

  // --- Custom accent ---

  const resolved = $derived(resolveTheme(savedTheme, preferences.accent_color))
  const shownAccent = $derived(preferences.accent_color || resolved.theme.colors.accent)
  let accentDraft = $state('')
  let accentError = $state('')
  let savingAccent = $state(false)
  $effect(() => {
    accentDraft = shownAccent
    accentError = ''
  })
  const accentDirty = $derived(accentDraft.trim().toLowerCase() !== shownAccent)

  async function saveAccent(event: SubmitEvent) {
    event.preventDefault()
    const value = accentDraft.trim().toLowerCase()
    appearanceStatus = ''
    appearanceError = ''
    // Checked here for quick feedback; the server checks the format again.
    accentError = accentProblem(value, themeById(savedTheme)) ?? ''
    if (accentError || savingAccent || status !== 'ready') return
    await saveAccentValue(value, 'Accent color saved.')
  }

  async function resetAccent() {
    appearanceStatus = ''
    appearanceError = ''
    accentError = ''
    await saveAccentValue('', `Using the ${themeById(savedTheme).name} accent.`)
  }

  async function saveAccentValue(value: string, done: string) {
    savingAccent = true
    try {
      await save({ accent_color: value })
      appearanceStatus = done
    } catch (err) {
      if (!isUnauthorized(err)) appearanceError = `Accent color not saved: ${message(err)}`
    } finally {
      savingAccent = false
    }
  }

  // --- Library and playback (saved with the Save button) ---

  // Deriveds change only when the saved value changes, so saving the theme
  // does not discard unsaved edits here.
  const savedSort = $derived(preferences.library_sort)
  const savedAutoplay = $derived(preferences.autoplay_next)
  let sort = $state<LibrarySortChoice>('artist')
  let autoplay = $state(true)
  let saving = $state(false)
  let error = $state('')
  let saved = $state('')
  $effect(() => {
    sort = savedSort
  })
  $effect(() => {
    autoplay = savedAutoplay
  })

  const dirty = $derived(sort !== savedSort || autoplay !== savedAutoplay)

  async function saveForm(event: SubmitEvent) {
    event.preventDefault()
    if (saving || !dirty || status !== 'ready') return
    saving = true
    error = ''
    saved = ''
    const changes: Partial<Preferences> = {}
    if (sort !== savedSort) changes.library_sort = sort
    if (autoplay !== savedAutoplay) changes.autoplay_next = autoplay
    try {
      await save(changes)
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

    <section class="group" aria-labelledby="appearance-title">
      <h2 id="appearance-title" class="rw-panel-title">Appearance</h2>

      <fieldset class="themes" disabled={status !== 'ready'}>
        <legend class="rw-label">Theme</legend>
        <div class="theme-list">
          {#each THEMES as t (t.id)}
            <label class="theme" class:selected={selectedTheme === t.id}>
              <input
                type="radio"
                name="theme"
                value={t.id}
                checked={selectedTheme === t.id}
                onchange={() => chooseTheme(t.id)}
              />
              <span class="preview" aria-hidden="true" use:swatch={t.colors}>
                <span class="p-surface"></span><span class="p-text"></span><span class="p-accent"></span>
              </span>
              <span class="theme-name">{t.name}</span>
              {#if savedTheme === t.id}<span class="current">Current</span>{/if}
            </label>
          {/each}
        </div>
      </fieldset>

      <form class="accent" onsubmit={saveAccent} novalidate>
        <label class="rw-label" for="pref-accent">Accent color</label>
        <div class="accent-row">
          <input
            class="picker"
            type="color"
            bind:value={accentDraft}
            disabled={status !== 'ready' || savingAccent}
            aria-label="Pick an accent color"
          />
          <input
            id="pref-accent"
            class="rw-input hex"
            type="text"
            maxlength="7"
            spellcheck="false"
            autocomplete="off"
            bind:value={accentDraft}
            disabled={status !== 'ready' || savingAccent}
            aria-invalid={accentError ? 'true' : undefined}
            aria-describedby={accentError ? 'pref-accent-error pref-accent-hint' : 'pref-accent-hint'}
          />
          <button class="rw-button" type="submit" disabled={status !== 'ready' || savingAccent || !accentDirty}>
            {savingAccent ? 'Saving…' : 'Use color'}
          </button>
          <button
            class="rw-button rw-button-secondary"
            type="button"
            onclick={resetAccent}
            disabled={status !== 'ready' || savingAccent || preferences.accent_color === ''}>Theme default</button
          >
        </div>
        <p id="pref-accent-hint" class="rw-hint">
          Optional. A six-digit hex color such as #c6ff00. It must stay readable on the theme's background.
        </p>
        {#if accentError}<p id="pref-accent-error" class="rw-field-error">{accentError}</p>{/if}
        {#if resolved.accentIgnored}
          <p class="rw-notice">
            Your accent color {preferences.accent_color} is too hard to read on {resolved.theme.name}, so the theme's own accent
            is shown. Choose another color or use the theme default.
          </p>
        {/if}
      </form>

      {#if appearanceError}<p class="rw-alert" role="alert">{appearanceError}</p>{/if}
      <p class="saved" role="status">{appearanceStatus}</p>
    </section>

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
            <label class="rw-label" for="pref-autoplay">Continue playlists</label>
            <p id="pref-autoplay-hint" class="rw-hint">When a playlist track ends, play the next available one.</p>
          </div>
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

  .themes {
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }

  .themes legend {
    margin-bottom: var(--rw-space-2);
    padding: 0;
  }

  .theme-list {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(9.5rem, 1fr));
    gap: var(--rw-space-2);
  }

  .theme {
    position: relative;
    display: grid;
    gap: var(--rw-space-2);
    padding: var(--rw-space-3);
    border: var(--rw-border-width) solid var(--rw-color-surface);
    cursor: pointer;
  }

  /* The chosen theme gets a full border plus a filled radio and "Current"
     text, so the state never depends on color alone. */
  .theme.selected {
    border-color: var(--rw-color-accent);
  }

  .theme input {
    position: absolute;
    top: var(--rw-space-3);
    right: var(--rw-space-3);
    width: 1.25rem;
    height: 1.25rem;
    margin: 0;
    accent-color: var(--rw-color-accent);
  }

  .preview {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 4px;
    width: 5.5rem;
    padding: 6px;
    border: 2px solid var(--swatch-border);
    background: var(--swatch-bg);
  }

  .preview > span {
    height: 1.25rem;
  }

  .p-surface {
    background: var(--swatch-surface);
  }

  .p-text {
    background: var(--swatch-text);
  }

  .p-accent {
    background: var(--swatch-accent);
  }

  .theme-name {
    font-weight: var(--rw-font-weight-bold);
  }

  .current {
    justify-self: start;
    padding: 0 var(--rw-space-1);
    background: var(--rw-color-accent);
    color: var(--rw-color-on-accent);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .accent {
    display: grid;
    gap: var(--rw-space-1);
  }

  .accent-row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
  }

  .picker {
    width: 2.75rem;
    height: 2.75rem;
    padding: 0;
    border: var(--rw-border);
    background: var(--rw-color-bg);
    cursor: pointer;
  }

  .hex {
    width: 7.5rem;
    font-family: var(--rw-font-mono);
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
    accent-color: var(--rw-color-accent);
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
