<script lang="ts">
  import { tick } from 'svelte'
  import { ApiError, applyThemeById, setThemeEnabled, type ThemeInfo, type ThemeState } from '../services/api'
  import { resolveTheme, styleWith, type ThemePreview } from '../services/themes'
  import { sentence } from '../services/validation'
  import ThemeEditor from './ThemeEditor.svelte'

  interface Props {
    /** The theme state from the server (null until loaded). */
    themes: ThemeState | null
    status: 'loading' | 'ready' | 'error'
    loadError: string
    onRetry: () => void
    /** Receives the new state after every change the server confirmed. */
    onChanged: (state: ThemeState) => void
    /** Shows a theme or unsaved edits across the app; null shows the saved theme. */
    onPreview: (preview: ThemePreview | null) => void
    onUnauthorized: () => void
  }
  let { themes, status, loadError, onRetry, onChanged, onPreview, onUnauthorized }: Props = $props()

  const items = $derived(themes?.items ?? [])
  const nameOf = (id: string) => items.find((t) => t.id === id)?.name ?? id
  const usable = (t: ThemeInfo) => t.state === 'available'

  // One request at a time keeps the shown state in step with the server.
  let busy = $state(false)
  let message = $state('')
  let error = $state('')

  // A theme previewed from its card (whole app, nothing saved).
  let previewing = $state<string | null>(null)
  // The theme open in the editor, whether it has unsaved changes, and a
  // theme waiting to be opened once the user decides about them.
  let editing = $state<string | null>(null)
  let editorDirty = $state(false)
  let pendingOpen = $state<string | null>(null)
  const editingTheme = $derived(items.find((t) => t.id === editing && usable(t)))

  function failed(action: string, err: unknown) {
    if (err instanceof ApiError && err.status === 401) {
      onUnauthorized()
      return
    }
    error = `${action}: ${err instanceof Error ? sentence(err.message) : 'Unknown error.'} The saved theme is shown.`
  }

  async function run(action: string, request: () => Promise<ThemeState>, done: (s: ThemeState) => string) {
    if (busy) return
    busy = true
    error = ''
    message = ''
    try {
      const next = await request()
      onChanged(next)
      message = done(next)
    } catch (err) {
      failed(action, err)
    } finally {
      busy = false
    }
  }

  function stopPreview() {
    previewing = null
    onPreview(null)
  }

  function togglePreview(t: ThemeInfo) {
    if (previewing === t.id) {
      stopPreview()
      message = ''
      return
    }
    previewing = t.id
    onPreview({ themeId: t.id })
    message = `Previewing ${t.name}. Nothing is saved until you apply it.`
  }

  async function apply(t: ThemeInfo) {
    const wasPreviewing = previewing === t.id
    await run('Theme not applied', () => applyThemeById(t.id), () => `Applied: ${t.name}.`)
    if (wasPreviewing) stopPreview()
  }

  async function setEnabled(t: ThemeInfo, on: boolean) {
    if (!on && previewing === t.id) stopPreview()
    const before = themes?.active
    await run(on ? 'Theme not enabled' : 'Theme not disabled', () => setThemeEnabled(t.id, on), (next) => {
      if (on) return `Enabled: ${t.name}.`
      const switched = next.active !== before ? ` ${nameOf(next.active)} is now active.` : ''
      return `Disabled: ${t.name}.${switched}`
    })
  }

  async function configure(t: ThemeInfo) {
    if (editing === t.id) {
      document.getElementById('editor-title')?.focus()
      return
    }
    if (editing && editorDirty) {
      pendingOpen = t.id
      await tick()
      document.getElementById('switch-title')?.focus()
      return
    }
    await openEditor(t.id)
  }

  async function openEditor(id: string) {
    pendingOpen = null
    if (previewing) stopPreview()
    message = ''
    error = ''
    editing = id
    await tick()
    document.getElementById('editor-title')?.focus()
  }

  async function closeEditor() {
    const index = items.findIndex((t) => t.id === editing)
    editing = null
    editorDirty = false
    pendingOpen = null
    await tick()
    document.getElementById(`configure-${index}`)?.focus()
  }

  // Generated previews: the theme's resolved tokens on a small drawing.
  const cardTokens = (t: ThemeInfo) => resolveTheme(t, t.customization).tokens

  function stateLabel(t: ThemeInfo): string {
    if (t.state === 'invalid') return 'Invalid'
    if (t.state === 'incompatible') return 'Incompatible'
    return 'Compatible'
  }
</script>

<section class="themes" aria-labelledby="themes-title">
  <h2 id="themes-title" class="rw-panel-title">Theme and style</h2>

  {#if status === 'loading' && !themes}
    <p class="rw-muted" role="status">Loading themes…</p>
  {:else if status === 'error' && !themes}
    <div class="load-error">
      <p class="rw-alert" role="alert">Could not load themes: {loadError} The theme this browser last showed is kept.</p>
      <button class="rw-button rw-button-secondary" type="button" onclick={onRetry}>Try again</button>
    </div>
  {:else if themes}
    {#if themes.notice}<p class="rw-notice">{themes.notice}</p>{/if}

    {#if previewing && !editing}
      <div class="banner" role="region" aria-label="Theme preview">
        <p>Previewing <strong>{nameOf(previewing)}</strong> across the app. Nothing is saved until you apply it.</p>
        <div class="actions">
          {#each items.filter((t) => t.id === previewing) as t (t.id)}
            <button class="rw-button" type="button" onclick={() => apply(t)} disabled={busy}>Apply {t.name}</button>
          {/each}
          <button class="rw-button rw-button-secondary" type="button" onclick={stopPreview}>Stop preview</button>
        </div>
      </div>
    {/if}

    <h3 class="rw-label">Installed themes</h3>
    <ul class="cards">
      {#each items as t, i (t.id)}
        <li class="card" class:active={t.active} class:unusable={!usable(t)} aria-labelledby={`theme-name-${i}`}>
          {#if usable(t)}
            <span class="thumb" use:styleWith={cardTokens(t)} aria-hidden="true">
              <span class="th-rail"><span class="th-nav th-current"></span><span class="th-nav"></span><span class="th-nav"></span></span>
              <span class="th-main">
                <span class="th-line th-title"></span>
                <span class="th-line"></span>
                <span class="th-buttons"><span class="th-button"></span><span class="th-button th-secondary"></span></span>
                <span class="th-mini"><span class="th-art"></span><span class="th-bar"><span class="th-fill"></span></span></span>
              </span>
            </span>
          {:else}
            <span class="thumb thumb-missing" aria-hidden="true">No preview</span>
          {/if}

          <div class="card-body">
            <div class="card-head">
              <h4 id={`theme-name-${i}`} class="card-name">{t.name ?? t.id}</h4>
              <span class="badges">
                {#if t.active}<span class="badge badge-active">Active</span>{/if}
                {#if previewing === t.id}<span class="badge">Previewing</span>{/if}
                {#if usable(t) && !t.enabled}<span class="badge">Disabled</span>{/if}
                {#if usable(t) && t.customization_source !== 'none'}<span class="badge">Customized</span>{/if}
              </span>
            </div>
            {#if t.description}<p class="description">{t.description}</p>{/if}
            <p class="meta">
              {t.source === 'bundled' ? 'Bundled' : 'Installed'}{t.version ? ` · v${t.version}` : ''}{t.author ? ` · ${t.author}` : ''}{t.scheme
                ? ` · ${t.scheme}`
                : ''}
            </p>
            <p class="compat" class:problem={!usable(t)}>
              <span class="compat-state">{stateLabel(t)}</span>{#if t.error}: {t.error}{/if}
            </p>

            {#if usable(t)}
              <div class="card-actions">
                {#if t.enabled && !t.active}
                  <button class="rw-button card-button" type="button" onclick={() => apply(t)} disabled={busy}>Apply</button>
                  <button
                    class="rw-button rw-button-secondary card-button"
                    type="button"
                    aria-pressed={previewing === t.id}
                    onclick={() => togglePreview(t)}
                    disabled={busy || editing !== null}>Preview</button
                  >
                {/if}
                <button
                  id={`configure-${i}`}
                  class="rw-button rw-button-secondary card-button"
                  type="button"
                  aria-expanded={editing === t.id}
                  onclick={() => configure(t)}
                  disabled={busy}>Customize</button
                >
                {#if t.id !== themes.default_theme}
                  <button class="rw-button rw-button-secondary card-button" type="button" onclick={() => setEnabled(t, !t.enabled)} disabled={busy}>
                    {t.enabled ? 'Disable' : 'Enable'}
                  </button>
                {/if}
              </div>
            {/if}
          </div>
        </li>
      {/each}
    </ul>

    {#if error}<p class="rw-alert" role="alert">{error}</p>{/if}
    <p class="saved" role="status">{busy ? 'Saving…' : message}</p>

    {#if pendingOpen}
      <div class="confirm" role="alertdialog" aria-labelledby="switch-title" aria-describedby="switch-text">
        <p id="switch-title" class="rw-label" tabindex="-1">Discard unsaved changes?</p>
        <p id="switch-text" class="rw-hint">
          Your changes to {nameOf(editing ?? '')} are not saved. Open {nameOf(pendingOpen)} and discard them?
        </p>
        <div class="actions">
          <button class="rw-button" type="button" onclick={() => pendingOpen && openEditor(pendingOpen)}>Discard and open</button>
          <button class="rw-button rw-button-secondary" type="button" onclick={() => (pendingOpen = null)}>Keep editing</button>
        </div>
      </div>
    {/if}

    {#if editingTheme}
      {#key editingTheme.id}
        <ThemeEditor theme={editingTheme} {onChanged} {onPreview} onClose={closeEditor} {onUnauthorized} bind:dirty={editorDirty} />
      {/key}
    {/if}

    <div class="install">
      <h3 class="rw-label">Adding themes</h3>
      <p class="rw-hint">
        Themes are installed by the server's administrator, not from this page: a theme is a <code>theme.json</code> file placed in the
        theme directory on the server, then Rewind is restarted. Rewind checks every theme before listing it. Themes contain only
        colors and style settings, never code. See <code>docs/themes.md</code>.
      </p>
      <p class="rw-hint">
        {themes.directory_configured
          ? 'A theme directory is configured on this server.'
          : 'No theme directory is configured on this server, so only the bundled themes are available.'}
      </p>
    </div>
  {/if}
</section>

<style>
  .themes {
    display: grid;
    gap: var(--rw-space-4);
    max-width: 72rem;
    margin-bottom: var(--rw-space-6);
  }

  .load-error {
    display: grid;
    gap: var(--rw-space-3);
    justify-items: start;
    max-width: 32rem;
  }

  .banner,
  .confirm {
    display: grid;
    gap: var(--rw-space-2);
    padding: var(--rw-space-3);
    border-radius: var(--rw-radius-md);
  }

  .banner {
    border: var(--rw-border-width) solid var(--rw-color-accent);
    background: var(--rw-color-surface);
  }

  .confirm {
    border: var(--rw-border-width) solid var(--rw-color-danger);
  }

  .confirm p:focus {
    outline: none;
  }

  .actions,
  .card-actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
  }

  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 19rem), 1fr));
    gap: var(--rw-space-3);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /* The active theme gets a full border plus the "Active" label, so the
     state never depends on color alone. */
  .card {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    align-items: start;
    gap: var(--rw-space-3);
    padding: var(--rw-space-3);
    border: var(--rw-border-width) solid var(--rw-color-secondary);
    border-radius: var(--rw-radius-lg);
  }

  .card.active {
    border-color: var(--rw-color-primary);
  }

  .card-body {
    display: grid;
    gap: var(--rw-space-1);
    min-width: 0;
  }

  .card-head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--rw-space-1) var(--rw-space-2);
  }

  .card-name {
    margin: 0;
    font-size: var(--rw-font-size-md);
    font-weight: var(--rw-font-weight-black);
    overflow-wrap: anywhere;
  }

  .badges {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-1);
  }

  .badge {
    padding: 0 var(--rw-space-1);
    border: var(--rw-border-width) solid var(--rw-color-secondary);
    border-radius: var(--rw-radius-xs);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .badge-active {
    border-color: var(--rw-color-primary);
    background: var(--rw-color-primary);
    color: var(--rw-color-on-primary);
  }

  .description {
    font-size: var(--rw-font-size-sm);
    overflow-wrap: anywhere;
  }

  .meta,
  .compat {
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.75rem;
    overflow-wrap: anywhere;
  }

  .compat.problem {
    color: var(--rw-color-text);
    border-left: var(--rw-border-width) solid var(--rw-color-danger);
    padding-left: var(--rw-space-2);
  }

  .compat-state {
    font-weight: var(--rw-font-weight-bold);
    text-transform: uppercase;
  }

  .card-actions {
    margin-top: var(--rw-space-2);
  }

  .card-button {
    min-height: 2.75rem;
    padding-inline: var(--rw-space-3);
  }

  /* --- Card thumbnails, drawn with the theme's tokens --- */
  .thumb {
    display: grid;
    grid-template-columns: 1.25rem 1fr;
    width: 6.5rem;
    aspect-ratio: 4 / 3;
    overflow: hidden;
    border: var(--rw-border);
    border-radius: var(--rw-radius-md);
    background: var(--rw-color-bg);
  }

  .thumb-missing {
    display: grid;
    place-items: center;
    grid-template-columns: 1fr;
    border: var(--rw-border-width) dashed var(--rw-color-muted);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.65rem;
    text-align: center;
  }

  .th-rail {
    display: grid;
    align-content: start;
    gap: 3px;
    padding: 4px 3px;
    border-right: var(--rw-border-width) var(--rw-border-style) var(--rw-color-border);
  }

  .th-nav {
    height: 4px;
    border-radius: 2px;
    background: var(--rw-color-muted);
  }

  .th-current {
    background: var(--rw-color-primary);
  }

  .th-main {
    display: grid;
    align-content: start;
    gap: 4px;
    padding: 5px;
    min-width: 0;
  }

  .th-line {
    height: 4px;
    width: 70%;
    border-radius: 2px;
    background: var(--rw-color-muted);
  }

  .th-title {
    width: 50%;
    height: 6px;
    background: var(--rw-color-text);
  }

  .th-buttons {
    display: flex;
    gap: 3px;
  }

  .th-button {
    width: 1.1rem;
    height: 0.55rem;
    border: 1px solid var(--rw-color-border);
    border-radius: min(var(--rw-radius-button), 3px);
    background: var(--rw-color-primary);
  }

  .th-secondary {
    background: var(--rw-color-secondary);
  }

  .th-mini {
    display: flex;
    align-items: center;
    gap: 3px;
    margin-top: 2px;
    padding: 3px;
    border: 1px solid var(--rw-color-border);
    border-radius: min(var(--rw-radius-xl), 5px);
    background: var(--rw-color-surface);
  }

  .th-art {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 2px;
    background: var(--rw-color-secondary);
  }

  .th-bar {
    flex: 1;
    height: 3px;
    border-radius: 2px;
    background: var(--rw-color-secondary);
  }

  .th-fill {
    display: block;
    width: 45%;
    height: 100%;
    border-radius: inherit;
    background: var(--rw-color-accent);
  }

  .install {
    display: grid;
    gap: var(--rw-space-2);
    max-width: 44rem;
  }

  .saved {
    min-height: 1.25em;
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  @media (max-width: 30rem) {
    .card {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
