<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte'
  import { ApiError, applyThemeById, saveThemeCustomization, type ThemeInfo, type ThemeState } from '../services/api'
  import {
    checkContrast,
    resolveTheme,
    themeTokens,
    TOKEN_GROUPS,
    TOKEN_SPECS,
    tokenSpec,
    validToken,
    withColors,
    type ThemePreview as Preview,
    type Tokens,
    type TokenSpec,
    type TokenValue,
  } from '../services/themes'
  import { sentence } from '../services/validation'
  import ThemePreview from './ThemePreview.svelte'

  interface Props {
    theme: ThemeInfo
    onChanged: (state: ThemeState) => void
    /** Shows the drafts across the app while Settings is open; null ends it. */
    onPreview: (preview: Preview | null) => void
    onClose: () => void
    onUnauthorized: () => void
    /** True while there are unsaved changes (read by the parent). */
    dirty?: boolean
  }
  let { theme, onChanged, onPreview, onClose, onUnauthorized, dirty = $bindable(false) }: Props = $props()

  const name = $derived(theme.name ?? theme.id)
  const themeId = $derived(theme.id)
  const defaults = $derived(themeTokens(theme))
  const editable = $derived(TOKEN_SPECS.filter((s) => s.customizable && (theme.customizable ?? []).includes(s.name)))

  /** Sorted JSON, so equal customizations compare equal. */
  const keyOf = (t: Tokens) => JSON.stringify(Object.entries(t).sort(([a], [b]) => (a < b ? -1 : 1)))

  /** Valid, allowed values that differ from the theme's own. */
  function changed(values: Record<string, unknown>, base: Tokens, specs: TokenSpec[]): Tokens {
    const out: Tokens = {}
    for (const s of specs) {
      const v = validToken(s.name, values[s.name])
      if (v !== undefined && v !== base[s.name]) out[s.name] = v
    }
    return out
  }

  const savedCustom = $derived(changed(theme.customization, defaults, editable))
  const savedKey = $derived(theme.id + '\n' + keyOf(savedCustom))

  function savedDrafts(): Record<string, TokenValue> {
    const out: Record<string, TokenValue> = {}
    for (const s of editable) out[s.name] = savedCustom[s.name] ?? defaults[s.name]
    return out
  }

  // Drafts start as the saved values and are reset whenever those change
  // (after saving). They are compared by key, so saving another setting, or
  // a new copy of the same state, does not discard edits.
  let drafts = $state<Record<string, TokenValue>>(savedDrafts())
  $effect(() => {
    void savedKey
    drafts = untrack(savedDrafts)
  })

  // Colors are typed as text: accept upper case and spaces around.
  const normalize = (spec: TokenSpec, v: TokenValue | undefined) => (spec.kind === 'color' && typeof v === 'string' ? v.trim().toLowerCase() : v)
  const draftValue = (spec: TokenSpec) => validToken(spec.name, normalize(spec, drafts[spec.name]))

  const invalid = $derived(editable.filter((s) => draftValue(s) === undefined).map((s) => s.name))
  const draftCustom = $derived.by(() => {
    const out: Tokens = {}
    for (const s of editable) {
      const v = draftValue(s)
      if (v !== undefined && v !== defaults[s.name]) out[s.name] = v
    }
    return out
  })
  const draftKey = $derived(keyOf(draftCustom))
  const isDirty = $derived(invalid.length > 0 || draftKey !== keyOf(savedCustom))
  $effect(() => {
    dirty = isDirty
  })

  // Readability of the drafted colors. Text problems block saving; others are notes.
  const draftColors = $derived(Object.fromEntries(Object.entries(draftCustom).filter(([n]) => tokenSpec(n)?.kind === 'color')))
  const contrastCheck = $derived(checkContrast(withColors(defaults, draftColors)))
  const label = (n: string) => tokenSpec(n)?.label ?? n

  // What the preview shows: unreadable or invalid drafts are left out, as
  // they would be in the app.
  const previewTokens = $derived(resolveTheme(theme, draftCustom).tokens)
  // Saved values that cannot be used with this theme (e.g. earlier custom colors).
  const savedIgnored = $derived(resolveTheme(theme, theme.customization).ignored.filter((n) => tokenSpec(n)))

  // The whole app shows the drafts while the editor is open (Settings only).
  $effect(() => {
    void draftKey
    onPreview({ themeId, custom: untrack(() => ({ ...draftCustom })) })
  })
  onDestroy(() => onPreview(null))

  // Leaving the page with unsaved changes asks first.
  $effect(() => {
    if (!isDirty) return
    const warn = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  })

  let saving = $state(false)
  let error = $state('')
  let status = $state('')
  let confirming = $state(false)

  function message(err: unknown): string {
    return err instanceof Error ? sentence(err.message) : 'Unknown error.'
  }

  const canApply = $derived(theme.enabled && !theme.active)
  const submitLabel = $derived(canApply ? 'Save and apply' : 'Save')
  const blocked = $derived(invalid.length > 0 || contrastCheck.blocking.length > 0)

  async function save(apply: boolean) {
    error = ''
    status = ''
    if (saving || blocked) {
      if (invalid.length > 0) document.getElementById(fieldId(invalid[0]))?.focus()
      return
    }
    const wasActive = theme.active
    const label = name
    let saved = false
    saving = true
    try {
      if (isDirty) {
        onChanged(await saveThemeCustomization(theme.id, draftCustom))
        saved = true
      }
      if (apply && !wasActive) {
        onChanged(await applyThemeById(theme.id))
        status = `${saved ? 'Saved and applied' : 'Applied'}: ${label}.`
      } else {
        status = wasActive ? `Saved and applied: ${label}.` : `Saved for ${label}. It is not the active theme; apply it to use it.`
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        onUnauthorized()
        return
      }
      error = saved ? `Saved, but not applied: ${message(err)}` : `Not saved: ${message(err)} Your changes are still here.`
    } finally {
      saving = false
    }
  }

  function submit(event: SubmitEvent) {
    event.preventDefault()
    void save(true)
  }

  const atDefaults = $derived(editable.every((s) => draftValue(s) === defaults[s.name]))

  function resetAll() {
    error = ''
    for (const s of editable) drafts[s.name] = defaults[s.name]
    status = isDirty ? `${name} defaults are shown. Save to keep them.` : ''
  }

  function resetOne(spec: TokenSpec) {
    drafts[spec.name] = defaults[spec.name]
  }

  async function cancel() {
    if (!isDirty) {
      onClose()
      return
    }
    confirming = true
    await tick()
    document.getElementById('discard-title')?.focus()
  }

  // --- Per-control details ---

  const fieldId = (n: string) => `token-${n.replace(/\./g, '-')}`

  function origin(spec: TokenSpec): string {
    if (draftValue(spec) === undefined) return 'Invalid'
    if (draftValue(spec) !== (savedCustom[spec.name] ?? defaults[spec.name])) return 'Not saved yet'
    if (savedIgnored.includes(spec.name)) return 'Not readable here'
    if (spec.name in savedCustom) return theme.customization_source === 'legacy' ? 'Earlier custom color' : 'Custom'
    return 'Theme default'
  }

  function shown(spec: TokenSpec): string {
    const v = draftValue(spec) ?? defaults[spec.name]
    if (spec.kind === 'length') return `${v} px`
    if (spec.name === 'font.heading-scale') return `${Number(v).toFixed(2)}×`
    return String(v)
  }

  function fieldError(spec: TokenSpec): string | undefined {
    if (draftValue(spec) !== undefined) return undefined
    const v = String(normalize(spec, drafts[spec.name]) ?? '')
    // While typing, a format error is shown only once more typing cannot fix it.
    if (spec.kind === 'color' && /^#?[0-9a-f]{0,5}$/.test(v)) return undefined
    return spec.kind === 'color' ? 'Use a color like #c6ff00 (# and six hex digits).' : `Use a value from ${spec.min} to ${spec.max}.`
  }

  const groups = $derived(TOKEN_GROUPS.map((g) => ({ ...g, specs: editable.filter((s) => s.group === g.id) })).filter((g) => g.specs.length > 0))
</script>

<section class="editor rw-panel" aria-labelledby="editor-title">
  <header class="editor-head">
    <h3 id="editor-title" class="rw-panel-title" tabindex="-1">Customize {name}</h3>
    <p class="rw-hint">
      {theme.source === 'bundled' ? 'Bundled with Rewind' : 'Installed by the administrator'}{theme.version ? ` · version ${theme.version}` : ''}{theme.author
        ? ` · by ${theme.author}`
        : ''}
    </p>
  </header>
  {#if theme.description}<p>{theme.description}</p>{/if}
  <p class="rw-hint">
    Changes show at once in the preview and across the app while Settings is open. Nothing is saved until you press {submitLabel}.
    Your settings for this theme are kept separately from other themes.
  </p>

  {#if !theme.enabled}
    <p class="rw-notice">This theme is disabled. You can customize it, but enable it to apply it.</p>
  {/if}
  {#if theme.customization_source === 'legacy'}
    <p class="rw-notice">
      Some colors come from the custom colors you chose before themes could be customized one by one. They apply to every theme you
      have not customized. Saving here keeps them for {name} only.
    </p>
  {/if}
  {#if savedIgnored.length > 0}
    <p class="rw-notice">
      Not used because they would be hard to read with this theme: {savedIgnored.map(label).join(', ')}. Choose other values or reset
      them.
    </p>
  {/if}

  <form class="editor-form" onsubmit={submit} novalidate>
    <div class="layout">
      <div class="controls">
        {#if groups.length === 0}
          <p class="rw-muted">{name} cannot be customized.</p>
        {/if}
        {#each groups as group (group.id)}
          <fieldset class="token-group" disabled={saving}>
            <legend class="rw-label">{group.label}</legend>
            {#each group.specs as spec (spec.name)}
              {@const id = fieldId(spec.name)}
              {@const problem = fieldError(spec)}
              <div class="control">
                <div class="control-head">
                  <label class="control-name" for={id}>{spec.label}</label>
                  <span class="origin">{origin(spec)}</span>
                </div>
                <p id={`${id}-hint`} class="rw-hint">{spec.help}</p>
                <div class="inputs">
                  {#if spec.kind === 'color'}
                    <input
                      class="picker"
                      type="color"
                      value={String(draftValue(spec) ?? defaults[spec.name])}
                      oninput={(e) => (drafts[spec.name] = e.currentTarget.value)}
                      aria-label={`Pick the ${spec.label.toLowerCase()} color`}
                    />
                    <input
                      {id}
                      class="rw-input hex"
                      type="text"
                      maxlength="7"
                      spellcheck="false"
                      autocomplete="off"
                      bind:value={drafts[spec.name]}
                      aria-invalid={problem ? 'true' : undefined}
                      aria-describedby={problem ? `${id}-error ${id}-hint` : `${id}-hint`}
                    />
                  {:else if spec.kind === 'choice'}
                    <select {id} class="rw-input choice" bind:value={drafts[spec.name]} aria-describedby={`${id}-hint`}>
                      {#each spec.choices ?? [] as c (c.value)}
                        <option value={c.value}>{c.label}</option>
                      {/each}
                    </select>
                  {:else}
                    <input
                      {id}
                      class="range"
                      type="range"
                      min={spec.min}
                      max={spec.max}
                      step={spec.step}
                      bind:value={drafts[spec.name]}
                      aria-describedby={`${id}-hint`}
                      aria-valuetext={shown(spec)}
                    />
                    <output class="value" for={id}>{shown(spec)}</output>
                  {/if}
                  <button
                    class="reset-one"
                    type="button"
                    onclick={() => resetOne(spec)}
                    disabled={draftValue(spec) === defaults[spec.name]}
                    aria-label={`Reset ${spec.label.toLowerCase()} to the theme's value`}>Reset</button
                  >
                </div>
                {#if problem}<p id={`${id}-error`} class="rw-field-error">{problem}</p>{/if}
              </div>
            {/each}
            {#if group.id === 'colors' && (contrastCheck.blocking.length > 0 || contrastCheck.notes.length > 0)}
              <div class="contrast" aria-live="polite">
                {#if contrastCheck.blocking.length > 0}
                  <p class="rw-field-error">These colors would be hard to read, so they cannot be saved and the preview leaves them out:</p>
                  <ul class="problems">
                    {#each contrastCheck.blocking as p (p.fg + p.bg)}<li class="rw-field-error">{p.message}</li>{/each}
                  </ul>
                {/if}
                {#each contrastCheck.notes as p (p.fg + p.bg)}<p class="note">{p.message}</p>{/each}
              </div>
            {/if}
          </fieldset>
        {/each}
      </div>

      <div class="preview-column">
        <p class="rw-label">Preview</p>
        <ThemePreview tokens={previewTokens} />
      </div>
    </div>

    {#if confirming}
      <div class="confirm" role="alertdialog" aria-labelledby="discard-title" aria-describedby="discard-text">
        <p id="discard-title" class="rw-label" tabindex="-1">Discard unsaved changes?</p>
        <p id="discard-text" class="rw-hint">Your changes to {name} are not saved. The saved settings will be shown again.</p>
        <div class="actions">
          <button class="rw-button" type="button" onclick={onClose}>Discard changes</button>
          <button class="rw-button rw-button-secondary" type="button" onclick={() => (confirming = false)}>Keep editing</button>
        </div>
      </div>
    {/if}

    <div class="actions">
      <button class="rw-button" type="submit" disabled={saving || blocked || (!isDirty && !canApply)}>
        {saving ? 'Saving…' : submitLabel}
      </button>
      {#if canApply}
        <button class="rw-button rw-button-secondary" type="button" onclick={() => save(false)} disabled={saving || blocked || !isDirty}
          >Save only</button
        >
      {/if}
      <button class="rw-button rw-button-secondary" type="button" onclick={resetAll} disabled={saving || atDefaults}>Reset to defaults</button>
      <button class="rw-button rw-button-secondary" type="button" onclick={cancel} disabled={saving}>{isDirty ? 'Cancel' : 'Close'}</button>
    </div>
    <p class="unsaved" aria-live="polite">{isDirty ? 'Unsaved changes.' : ''}</p>
    {#if error}<p class="rw-alert" role="alert">{error}</p>{/if}
    <p class="saved" role="status">{status}</p>
  </form>
</section>

<style>
  .editor {
    min-width: 0;
    box-shadow: none;
  }

  .editor-head {
    display: grid;
    gap: var(--rw-space-1);
  }

  .editor-head h3:focus {
    outline: none;
  }

  .editor-head h3:focus-visible {
    outline: var(--rw-focus-ring);
  }

  .editor-form {
    display: grid;
    gap: var(--rw-space-3);
    min-width: 0;
  }

  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--rw-space-4);
    min-width: 0;
  }

  .controls {
    display: grid;
    gap: var(--rw-space-4);
    min-width: 0;
  }

  .token-group {
    display: grid;
    gap: var(--rw-space-2);
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }

  .token-group legend {
    margin-bottom: var(--rw-space-2);
    padding: 0;
  }

  .control {
    display: grid;
    gap: var(--rw-space-1);
    padding: var(--rw-space-3);
    border: var(--rw-border-width) solid var(--rw-color-secondary);
    border-radius: var(--rw-radius-md);
    background: var(--rw-color-bg);
  }

  .control-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--rw-space-1) var(--rw-space-3);
  }

  .control-name {
    font-weight: var(--rw-font-weight-bold);
  }

  .origin {
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.75rem;
    text-transform: uppercase;
  }

  .inputs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2);
    margin-top: var(--rw-space-1);
  }

  .picker {
    width: 2.75rem;
    height: 2.75rem;
    padding: 0;
    border: var(--rw-border);
    border-radius: var(--rw-radius-input);
    background: var(--rw-color-bg);
    cursor: pointer;
  }

  .hex {
    width: 7.5rem;
    font-family: var(--rw-font-mono);
  }

  .choice {
    width: auto;
    min-width: 10rem;
    appearance: auto;
  }

  .range {
    flex: 1 1 8rem;
    min-width: 6rem;
    height: 2.75rem;
    margin: 0;
    accent-color: var(--rw-color-accent);
  }

  .value {
    min-width: 4rem;
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    font-variant-numeric: tabular-nums;
  }

  .reset-one {
    min-height: 2.75rem;
    padding: 0 var(--rw-space-3);
    border: var(--rw-border-width) solid var(--rw-color-secondary);
    border-radius: var(--rw-radius-button);
    background: transparent;
    color: var(--rw-color-text);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    text-transform: uppercase;
    cursor: pointer;
  }

  .reset-one:hover:not(:disabled) {
    border-color: var(--rw-color-border);
  }

  .reset-one:disabled {
    color: var(--rw-color-muted);
    cursor: default;
  }

  .contrast {
    display: grid;
    gap: var(--rw-space-1);
  }

  .problems {
    margin: 0;
    padding-left: var(--rw-space-5);
  }

  .note {
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
  }

  .note::before {
    content: 'Note: ';
    color: var(--rw-color-text);
    font-weight: var(--rw-font-weight-bold);
  }

  .preview-column {
    display: grid;
    align-content: start;
    gap: var(--rw-space-2);
    min-width: 0;
  }

  .confirm {
    display: grid;
    gap: var(--rw-space-2);
    padding: var(--rw-space-3);
    border: var(--rw-border-width) solid var(--rw-color-danger);
    border-radius: var(--rw-radius-md);
    background: var(--rw-color-bg);
  }

  .confirm p:focus {
    outline: none;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
  }

  .unsaved,
  .saved {
    min-height: 1.25em;
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  /* Wide screens: controls and a sticky preview side by side. */
  @media (min-width: 72rem) {
    .layout {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
      align-items: start;
    }

    .preview-column {
      position: sticky;
      top: var(--rw-space-4);
    }
  }
</style>
