<script lang="ts">
  // Edits a track's details in Rewind's database. Music files are never
  // changed: edits replace the tag values Rewind shows, survive rescans, and
  // can be reverted to the file's values field by field.
  import { tick } from 'svelte'
  import {
    ApiError,
    getTrackMetadata,
    revertTrackMetadata,
    saveTrackMetadata,
    type EditField,
    type MetadataResponse,
    type MetadataValue,
  } from '../services/api'
  import { listening } from '../services/listening.svelte'
  import { player } from '../services/player.svelte'
  import { ui } from '../services/ui.svelte'
  import { sentence } from '../services/validation'

  interface Props {
    trackId: number
    onUnauthorized: () => void
  }
  let { trackId, onUnauthorized }: Props = $props()

  const LABELS: Record<string, string> = {
    title: 'Title',
    artist: 'Artist',
    album_artist: 'Album artist',
    album: 'Album',
    track_number: 'Track number',
    disc_number: 'Disc number',
    year: 'Year',
    genre: 'Genre',
    composer: 'Composer',
    lyrics: 'Lyrics',
  }

  let data = $state<MetadataResponse | null>(null)
  let drafts = $state<Record<string, string>>({})
  let loadError = $state('')
  let error = $state('')
  let conflict = $state(false)
  let saving = $state(false)
  let confirmDiscard = $state(false)
  let dialog: HTMLElement | undefined = $state()

  const show = (v: MetadataValue | undefined) => (v === null || v === undefined ? '' : String(v))

  async function load() {
    loadError = ''
    conflict = false
    try {
      data = await getTrackMetadata(trackId)
      drafts = Object.fromEntries(data.fields.map((f) => [f.Name, show(data!.metadata.values[f.Name])]))
      await tick()
      dialog?.querySelector<HTMLElement>('input, textarea')?.focus()
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) onUnauthorized()
      else loadError = err instanceof Error ? sentence(err.message) : 'Could not load the details.'
    }
  }

  $effect(() => {
    void trackId
    void load()
  })

  /** The value to send for a field, or an error message. */
  function parse(f: EditField, raw: string): { value: MetadataValue } | { error: string } {
    const v = f.Kind === 'multiline' ? raw.replace(/\r\n?/g, '\n').trim() : raw.trim()
    if (v === '') return f.Required ? { error: `${LABELS[f.Name]} cannot be empty.` } : { value: null }
    if (f.Kind === 'number') {
      const n = Number(v)
      if (!/^\d+$/.test(v) || n < f.Min || n > f.Max) return { error: `${LABELS[f.Name]} must be a whole number from ${f.Min} to ${f.Max}.` }
      return { value: n }
    }
    if ([...v].length > f.Max) return { error: `${LABELS[f.Name]} must be at most ${f.Max} characters.` }
    return { value: v }
  }

  const parsed = $derived(data ? Object.fromEntries(data.fields.map((f) => [f.Name, parse(f, drafts[f.Name] ?? '')])) : {})
  const changes = $derived.by(() => {
    const out: Record<string, MetadataValue> = {}
    if (!data) return out
    for (const f of data.fields) {
      const p = parsed[f.Name]
      if (p && 'value' in p && p.value !== (data.metadata.values[f.Name] ?? null)) out[f.Name] = p.value
    }
    return out
  })
  const invalid = $derived(Object.entries(parsed).filter(([, p]) => 'error' in p))
  const dirty = $derived(Object.keys(changes).length > 0)

  async function apply(result: MetadataResponse, message: string) {
    data = result
    drafts = Object.fromEntries(result.fields.map((f) => [f.Name, show(result.metadata.values[f.Name])]))
    player.updateTrack(result.track)
    listening.libraryChanged()
    listening.announce(message)
  }

  function failed(err: unknown) {
    if (err instanceof ApiError && err.status === 401) {
      onUnauthorized()
      return
    }
    if (err instanceof ApiError && err.status === 409) {
      conflict = true
      error = 'These details were changed meanwhile (another edit or a rescan). Reload to see the current values; your changes are not saved.'
      return
    }
    error = err instanceof Error ? sentence(err.message) : 'Not saved.'
  }

  async function save(event: SubmitEvent) {
    event.preventDefault()
    if (!data || saving || !dirty || invalid.length > 0) return
    saving = true
    error = ''
    try {
      await apply(await saveTrackMetadata(trackId, data.metadata.revision, changes), 'Details saved.')
    } catch (err) {
      failed(err)
    } finally {
      saving = false
    }
  }

  async function revert(fields?: string[]) {
    if (!data || saving) return
    saving = true
    error = ''
    try {
      await apply(await revertTrackMetadata(trackId, data.metadata.revision, fields), fields ? 'Restored the file’s value.' : 'Restored the file’s values.')
    } catch (err) {
      failed(err)
    } finally {
      saving = false
    }
  }

  function close() {
    if (dirty && !confirmDiscard) {
      confirmDiscard = true
      return
    }
    ui.closeEditor()
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      close()
    }
  }

  const edited = (name: string) => data?.metadata.edited.includes(name) ?? false
</script>

<div class="backdrop" aria-hidden="true"></div>
<!-- svelte-ignore a11y_no_noninteractive_element_interactions (Escape closes the dialog) -->
<div class="dialog" role="dialog" tabindex="-1" aria-modal="true" aria-labelledby="metadata-dialog-title" bind:this={dialog} onkeydown={onKeydown}>
  <header class="head">
    <h2 id="metadata-dialog-title" class="rw-panel-title">Edit details</h2>
    <p class="rw-hint">
      Saved in Rewind's library only. Your music files are not modified, and rescans keep these edits. “Use file value” undoes an
      edit.
    </p>
  </header>

  {#if loadError}
    <p class="rw-alert" role="alert">{loadError}</p>
    <div class="actions">
      <button class="rw-button rw-button-secondary" type="button" onclick={load}>Try again</button>
      <button class="rw-button rw-button-secondary" type="button" onclick={() => ui.closeEditor()}>Close</button>
    </div>
  {:else if !data}
    <p class="rw-muted" role="status">Loading…</p>
  {:else}
    <form class="form" onsubmit={save} novalidate>
      <div class="fields">
        {#each data.fields as f (f.Name)}
          {@const p = parsed[f.Name]}
          {@const id = `meta-${f.Name}`}
          <div class="rw-field" class:wide={f.Kind === 'multiline' || f.Name === 'title'}>
            <div class="label-row">
              <label class="rw-label" for={id}>{LABELS[f.Name] ?? f.Name}</label>
              {#if f.Name in changes}<span class="tag">Unsaved</span>{:else if edited(f.Name)}<span class="tag">Edited</span>{/if}
            </div>
            {#if f.Kind === 'multiline'}
              <textarea {id} class="rw-input" rows="6" bind:value={drafts[f.Name]} aria-invalid={'error' in p ? 'true' : undefined}></textarea>
            {:else}
              <input
                {id}
                class="rw-input"
                inputmode={f.Kind === 'number' ? 'numeric' : undefined}
                bind:value={drafts[f.Name]}
                aria-invalid={'error' in p ? 'true' : undefined}
                aria-describedby={'error' in p ? `${id}-error` : undefined}
              />
            {/if}
            {#if 'error' in p}<p id={`${id}-error`} class="rw-field-error">{p.error}</p>{/if}
            {#if edited(f.Name)}
              <p class="rw-hint from-file">
                In the file: {show(data.metadata.from_file[f.Name]) || '(empty)'}
                <button class="link" type="button" onclick={() => revert([f.Name])} disabled={saving}>Use file value</button>
              </p>
            {/if}
          </div>
        {/each}
      </div>

      {#if error}
        <p class="rw-alert" role="alert">{error}</p>
        {#if conflict}<button class="rw-button rw-button-secondary" type="button" onclick={load}>Reload</button>{/if}
      {/if}
      {#if confirmDiscard}
        <div class="confirm" role="alertdialog" aria-label="Discard changes?">
          <p>Discard your unsaved changes?</p>
          <button class="rw-button" type="button" onclick={() => ui.closeEditor()}>Discard</button>
          <button class="rw-button rw-button-secondary" type="button" onclick={() => (confirmDiscard = false)}>Keep editing</button>
        </div>
      {/if}
      <p class="status" role="status">{dirty ? 'Unsaved changes.' : ''}</p>
      <div class="actions">
        <button class="rw-button" type="submit" disabled={!dirty || invalid.length > 0 || saving}>{saving ? 'Saving…' : 'Save'}</button>
        <button class="rw-button rw-button-secondary" type="button" onclick={close}>{dirty ? 'Cancel' : 'Close'}</button>
        {#if data.metadata.edited.length > 0}
          <button class="rw-button rw-button-secondary" type="button" onclick={() => revert()} disabled={saving}>Restore all file values</button>
        {/if}
      </div>
    </form>
  {/if}
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 44;
    background: var(--rw-color-bg);
    opacity: 0.7;
  }

  .dialog {
    position: fixed;
    inset: 50% auto auto 50%;
    z-index: 45;
    display: grid;
    gap: var(--rw-space-3);
    width: min(44rem, calc(100vw - 2 * var(--rw-space-3)));
    max-height: calc(100dvh - 2 * var(--rw-space-3));
    padding: var(--rw-space-panel);
    overflow-y: auto;
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-surface);
    box-shadow: var(--rw-shadow);
    transform: translate(-50%, -50%);
  }

  .head {
    display: grid;
    gap: var(--rw-space-1);
  }

  .form {
    display: grid;
    gap: var(--rw-space-3);
  }

  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 14rem), 1fr));
    gap: var(--rw-space-3);
  }

  .wide {
    grid-column: 1 / -1;
  }

  .label-row {
    display: flex;
    align-items: baseline;
    gap: var(--rw-space-2);
  }

  .tag {
    padding: 0 var(--rw-space-1);
    border: var(--rw-border-width) solid var(--rw-color-accent);
    border-radius: var(--rw-radius-xs);
    font-family: var(--rw-font-mono);
    font-size: 0.65rem;
    text-transform: uppercase;
  }

  textarea {
    resize: vertical;
    font: inherit;
  }

  .from-file {
    overflow-wrap: anywhere;
  }

  .link {
    padding: 0;
    border: 0;
    background: none;
    color: var(--rw-color-primary);
    font: inherit;
    text-decoration: underline;
    cursor: pointer;
  }

  .confirm {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2);
    padding: var(--rw-space-3);
    border: var(--rw-border-width) solid var(--rw-color-danger);
    border-radius: var(--rw-radius-md);
  }

  .status {
    min-height: 1.25em;
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--rw-space-2);
  }
</style>
