<script lang="ts">
  import { ApiError, savePreferences, type LibrarySortChoice, type Preferences } from '../services/api'
  import { LIBRARY_SORTS } from '../services/librarySort'
  import {
    applyTokens,
    COLOR_PATTERN,
    COLOR_ROLES,
    colorProblem,
    colorWarning,
    resolveTheme,
    THEMES,
    themeById,
    type ColorRole,
    type CustomColors,
    type ThemeColors,
    type ThemeId,
  } from '../services/themes'
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

  // --- Custom colors: edited together, previewed live, saved with one button ---

  const ROLE_INFO: Record<ColorRole, { label: string; pref: 'primary_color' | 'secondary_color' | 'accent_color'; help: string }> = {
    primary: {
      label: 'Primary color',
      pref: 'primary_color',
      help: 'Main buttons, links and the current page in the menu.',
    },
    secondary: {
      label: 'Secondary color',
      pref: 'secondary_color',
      help: 'Secondary buttons and supporting surfaces, such as the artwork placeholder.',
    },
    accent: {
      label: 'Accent color',
      pref: 'accent_color',
      help: 'Highlights: playback progress, the playing track, focus outlines and small markers.',
    },
  }

  const savedColors = $derived<CustomColors>({
    primary: preferences.primary_color,
    secondary: preferences.secondary_color,
    accent: preferences.accent_color,
  })
  const currentTheme = $derived(themeById(savedTheme))
  // What the app shows now.
  const resolved = $derived(resolveTheme(savedTheme, savedColors))
  // One derived string per role: drafts are reset only when a shown color
  // really changes, not whenever another preference is saved.
  const shownPrimary = $derived(resolved.colors.primary)
  const shownSecondary = $derived(resolved.colors.secondary)
  const shownAccent = $derived(resolved.colors.accent)
  const shown = $derived<CustomColors>({ primary: shownPrimary, secondary: shownSecondary, accent: shownAccent })

  let drafts = $state<CustomColors>({ primary: '', secondary: '', accent: '' })
  let submitted = $state(false)
  let savingColors = $state(false)
  $effect(() => {
    drafts.primary = shownPrimary
  })
  $effect(() => {
    drafts.secondary = shownSecondary
  })
  $effect(() => {
    drafts.accent = shownAccent
  })

  const normalize = (v: string) => v.trim().toLowerCase()
  const isDirty = (role: ColorRole) => normalize(drafts[role]) !== shown[role]
  const anyDirty = $derived(COLOR_ROLES.some(isDirty))
  const anySaved = $derived(COLOR_ROLES.some((r) => savedColors[r] !== ''))

  /** Problems that block saving, for changed colors only. */
  const problems = $derived(
    Object.fromEntries(COLOR_ROLES.map((r) => [r, isDirty(r) ? colorProblem(r, normalize(drafts[r]), currentTheme) : undefined])) as Record<
      ColorRole,
      string | undefined
    >,
  )
  // While typing, a format error is shown only once the value cannot become
  // valid by typing more.
  const stillTyping = (v: string) => /^#?[0-9a-f]{0,5}$/.test(v)
  const visibleProblem = (role: ColorRole) =>
    problems[role] && (submitted || !stillTyping(normalize(drafts[role])) || COLOR_PATTERN.test(normalize(drafts[role])))
      ? problems[role]
      : undefined
  const warnings = $derived(
    Object.fromEntries(COLOR_ROLES.map((r) => [r, colorWarning(r, normalize(drafts[r]), currentTheme)])) as Record<ColorRole, string | undefined>,
  )

  // The preview uses the drafts where they are valid colors.
  const preview = $derived(
    resolveTheme(
      savedTheme,
      Object.fromEntries(
        COLOR_ROLES.map((r) => {
          const v = normalize(drafts[r])
          return [r, isDirty(r) && COLOR_PATTERN.test(v) ? v : savedColors[r]]
        }),
      ),
    ),
  )
  const pickerValue = (role: ColorRole) => {
    const v = normalize(drafts[role])
    return COLOR_PATTERN.test(v) ? v : preview.colors[role]
  }

  /** How a saved color is currently used, in words. */
  function origin(role: ColorRole): string {
    if (isDirty(role)) return 'Not applied yet'
    if (resolved.ignored.includes(role)) return 'Not readable on this theme'
    if (savedColors[role] !== '') return 'Custom'
    if (role === 'primary' && savedColors.accent !== '' && !resolved.ignored.includes('accent')) return 'Follows the accent'
    return 'Theme default'
  }

  async function saveColors(event: SubmitEvent) {
    event.preventDefault()
    submitted = true
    appearanceStatus = ''
    appearanceError = ''
    if (!anyDirty || savingColors || status !== 'ready') return
    if (COLOR_ROLES.some((r) => problems[r])) return
    const changes: Partial<Preferences> = {}
    for (const r of COLOR_ROLES) if (isDirty(r)) changes[ROLE_INFO[r].pref] = normalize(drafts[r])
    await saveColorChanges(changes, 'Colors saved and applied.')
  }

  async function resetColors() {
    appearanceStatus = ''
    appearanceError = ''
    submitted = false
    if (!anySaved) {
      // Nothing saved: only discard the unsaved edits.
      drafts = { ...shown }
      return
    }
    await saveColorChanges(
      { primary_color: '', secondary_color: '', accent_color: '' },
      `Colors reset to the ${currentTheme.name} defaults.`,
    )
  }

  async function saveColorChanges(changes: Partial<Preferences>, done: string) {
    savingColors = true
    try {
      await save(changes)
      submitted = false
      appearanceStatus = done
    } catch (err) {
      if (!isUnauthorized(err)) appearanceError = `Colors not saved: ${message(err)}`
    } finally {
      savingColors = false
    }
  }

  // The preview gets the real color tokens, so it uses the same classes and
  // styles as the app (set through the CSS object model, as above).
  function previewTokens(node: HTMLElement, colors: ThemeColors) {
    applyTokens(node, colors)
    return { update: (next: ThemeColors) => applyTokens(node, next) }
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

      <form class="colors" onsubmit={saveColors} novalidate aria-labelledby="colors-title">
        <h3 id="colors-title" class="rw-label">Colors</h3>
        <p class="rw-hint">
          Optional, and saved for all themes. Six-digit hex colors such as #c6ff00. Primary and accent colors must stay readable
          on the theme's background; text on every color is chosen automatically.
        </p>

        {#each COLOR_ROLES as role (role)}
          {@const info = ROLE_INFO[role]}
          {@const problem = visibleProblem(role)}
          <div class="color-row">
            <div class="color-head">
              <label class="color-name" for={`pref-${role}`}>{info.label}</label>
              <span class="origin">{origin(role)}</span>
            </div>
            <p id={`pref-${role}-hint`} class="rw-hint">{info.help}</p>
            <div class="color-inputs">
              <input
                class="picker"
                type="color"
                value={pickerValue(role)}
                oninput={(e) => (drafts[role] = e.currentTarget.value)}
                disabled={status !== 'ready' || savingColors}
                aria-label={`Pick the ${info.label.toLowerCase()}`}
              />
              <input
                id={`pref-${role}`}
                class="rw-input hex"
                type="text"
                maxlength="7"
                spellcheck="false"
                autocomplete="off"
                bind:value={drafts[role]}
                disabled={status !== 'ready' || savingColors}
                aria-invalid={problem ? 'true' : undefined}
                aria-describedby={problem ? `pref-${role}-error pref-${role}-hint` : `pref-${role}-hint`}
              />
            </div>
            {#if problem}<p id={`pref-${role}-error`} class="rw-field-error">{problem}</p>{/if}
            {#if !problem && warnings[role]}<p class="color-warning">{warnings[role]}</p>{/if}
            {#if resolved.ignored.includes(role)}
              <p class="rw-notice">
                Your {info.label.toLowerCase()} {savedColors[role]} is too hard to read on {resolved.theme.name}, so the theme's own
                color is shown. Choose another color or reset the colors.
              </p>
            {/if}
          </div>
        {/each}

        <div class="palette" aria-labelledby="preview-title">
          <p id="preview-title" class="rw-label">Preview</p>
          <!-- A picture of the app in these colors, built from the same classes and tokens. Not interactive. -->
          <div class="palette-preview" use:previewTokens={preview.colors} aria-hidden="true">
            <div class="pv-row">
              <span class="rw-button pv-button">Primary</span>
              <span class="rw-button rw-button-secondary pv-button">Secondary</span>
              <span class="pv-link">A link</span>
              <span class="pv-tag">Playing</span>
            </div>
            <div class="pv-mini">
              <span class="pv-art"></span>
              <span class="pv-lines"><span class="pv-title">Track title</span><span class="pv-meta">Artist · Album</span></span>
              <span class="pv-bar"><span class="pv-fill"></span></span>
              <span class="rw-button pv-play">Pause</span>
            </div>
            <div class="pv-screen">
              <span class="pv-art pv-art-large"></span>
              <span class="pv-details">
                <span class="pv-label">Now playing</span>
                <span class="pv-title pv-title-large">Track title</span>
                <span class="pv-meta">Artist</span>
                <span class="pv-bar"><span class="pv-fill"></span></span>
                <span class="pv-controls">
                  <span class="rw-button rw-button-secondary pv-control"></span>
                  <span class="rw-button pv-control pv-main"></span>
                  <span class="rw-button rw-button-secondary pv-control"></span>
                </span>
              </span>
            </div>
          </div>
        </div>

        <div class="color-actions">
          <button class="rw-button" type="submit" disabled={status !== 'ready' || savingColors || !anyDirty}>
            {savingColors ? 'Saving…' : 'Apply colors'}
          </button>
          <button
            class="rw-button rw-button-secondary"
            type="button"
            onclick={resetColors}
            disabled={status !== 'ready' || savingColors || (!anySaved && !anyDirty)}>Reset to defaults</button
          >
        </div>
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
    border-radius: var(--rw-radius-lg);
    cursor: pointer;
  }

  /* The chosen theme gets a full border plus a filled radio and "Current"
     text, so the state never depends on color alone. */
  .theme.selected {
    border-color: var(--rw-color-primary);
  }

  .theme input {
    position: absolute;
    top: var(--rw-space-3);
    right: var(--rw-space-3);
    width: 1.25rem;
    height: 1.25rem;
    margin: 0;
    accent-color: var(--rw-color-primary);
  }

  .preview {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 4px;
    width: 5.5rem;
    padding: 6px;
    border: 2px solid var(--swatch-border);
    border-radius: var(--rw-radius-sm);
    background: var(--swatch-bg);
  }

  .preview > span {
    height: 1.25rem;
    border-radius: var(--rw-radius-xs);
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
    border-radius: var(--rw-radius-xs);
    background: var(--rw-color-primary);
    color: var(--rw-color-on-primary);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .colors {
    display: grid;
    gap: var(--rw-space-3);
    min-width: 0;
  }

  .colors h3 {
    margin: 0;
  }

  .color-row {
    display: grid;
    gap: var(--rw-space-1);
    padding: var(--rw-space-3);
    border: var(--rw-border-width) solid var(--rw-color-surface);
    border-radius: var(--rw-radius-lg);
  }

  .color-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--rw-space-1) var(--rw-space-3);
  }

  .color-name {
    font-weight: var(--rw-font-weight-bold);
  }

  .origin {
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.75rem;
    text-transform: uppercase;
  }

  .color-inputs {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
    margin-top: var(--rw-space-1);
  }

  .picker {
    width: 2.75rem;
    height: 2.75rem;
    padding: 0;
    border: var(--rw-border);
    border-radius: var(--rw-radius-sm);
    background: var(--rw-color-bg);
    cursor: pointer;
  }

  .hex {
    width: 7.5rem;
    font-family: var(--rw-font-mono);
  }

  .color-warning {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .color-warning::before {
    content: 'Note: ';
    color: var(--rw-color-text);
    font-weight: var(--rw-font-weight-bold);
  }

  .color-actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
  }

  /* --- Preview: drawn with the tokens set on .palette-preview --- */
  .palette {
    display: grid;
    gap: var(--rw-space-2);
    min-width: 0;
  }

  .palette-preview {
    display: grid;
    gap: var(--rw-space-3);
    padding: var(--rw-space-4);
    border: var(--rw-border-width) solid var(--rw-color-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-bg);
    color: var(--rw-color-text);
    overflow: hidden;
  }

  .pv-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2) var(--rw-space-3);
  }

  .pv-button {
    display: inline-grid;
    place-items: center;
    padding-inline: var(--rw-space-3);
    border-color: var(--rw-color-border);
    cursor: default;
  }

  .pv-link {
    color: var(--rw-color-primary);
    text-decoration: underline;
  }

  .pv-tag {
    padding: 0 var(--rw-space-1);
    border-radius: var(--rw-radius-xs);
    background: var(--rw-color-accent);
    color: var(--rw-color-on-accent);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .pv-mini {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) minmax(3rem, 1fr) auto;
    align-items: center;
    gap: var(--rw-space-3);
    padding: var(--rw-space-2) var(--rw-space-3);
    border: var(--rw-border-width) solid var(--rw-color-border);
    border-radius: var(--rw-radius-xl);
    background: var(--rw-color-surface);
    box-shadow: var(--rw-shadow-offset) var(--rw-shadow-offset) 0 0 var(--rw-color-accent);
  }

  .pv-art {
    width: 2.25rem;
    aspect-ratio: 1;
    border: var(--rw-border-width) solid var(--rw-color-border);
    border-radius: var(--rw-radius-sm);
    background: var(--rw-color-secondary);
  }

  .pv-lines,
  .pv-details {
    display: grid;
    min-width: 0;
  }

  .pv-title {
    overflow: hidden;
    font-weight: var(--rw-font-weight-black);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .pv-meta {
    overflow: hidden;
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .pv-bar {
    display: block;
    height: 0.4rem;
    border-radius: var(--rw-radius-round);
    background: var(--rw-color-secondary);
  }

  .pv-fill {
    display: block;
    width: 40%;
    height: 100%;
    border-radius: inherit;
    background: var(--rw-color-accent);
  }

  .pv-play {
    display: inline-grid;
    place-items: center;
    min-height: 2.25rem;
    padding-inline: var(--rw-space-3);
    border-color: var(--rw-color-border);
    cursor: default;
  }

  .pv-screen {
    display: grid;
    grid-template-columns: 6rem minmax(0, 1fr);
    align-items: center;
    gap: var(--rw-space-4);
  }

  .pv-art-large {
    width: 100%;
    border-radius: var(--rw-radius-art);
  }

  .pv-details {
    gap: var(--rw-space-1);
  }

  .pv-label {
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    letter-spacing: 0.12em;
    text-transform: uppercase;
  }

  .pv-title-large {
    font-size: var(--rw-font-size-lg);
  }

  .pv-controls {
    display: flex;
    align-items: center;
    gap: var(--rw-space-2);
    margin-top: var(--rw-space-1);
  }

  .pv-control {
    min-height: 0;
    width: 2rem;
    height: 2rem;
    padding: 0;
    border-color: var(--rw-color-border);
    cursor: default;
  }

  .pv-main {
    width: 2.5rem;
    height: 2.5rem;
    border-radius: var(--rw-radius-round);
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
