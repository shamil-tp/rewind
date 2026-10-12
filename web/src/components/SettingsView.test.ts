import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { DEFAULT_PREFERENCES, type Preferences } from '../services/api'
import { apiError, deferred, json, mockFetch as baseMockFetch } from '../test/fetch'
import SettingsView from './SettingsView.svelte'

// Settings also shows plugin status; these tests are about preferences.
const mockFetch: typeof baseMockFetch = (routes) =>
  baseMockFetch({ 'GET /api/plugins': () => json(200, { configured: false, api_version: 1, items: [] }), ...routes })

function prefs(over: Partial<Preferences> = {}): Preferences {
  return { ...DEFAULT_PREFERENCES, ...over }
}

function props(over: Record<string, unknown> = {}) {
  return {
    preferences: prefs(),
    status: 'ready' as const,
    loadError: '',
    onRetry: vi.fn(),
    onSaved: vi.fn(),
    onPreviewTheme: vi.fn(),
    onUnauthorized: vi.fn(),
    ...over,
  }
}

const save = () => screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement
const radio = (name: string) => screen.getByRole('radio', { name: new RegExp(name) }) as HTMLInputElement
const patches = (fetch: ReturnType<typeof mockFetch>) =>
  fetch.mock.calls.filter(([, i]) => i?.method === 'PATCH').map(([, i]) => JSON.parse(String(i?.body)))

describe('SettingsView: library and playback', () => {
  it('shows the saved values; Save is enabled only after a change', async () => {
    render(SettingsView, { props: props({ preferences: prefs({ library_sort: 'added', autoplay_next: false }) }) })
    expect((screen.getByLabelText('Library sort order') as HTMLSelectElement).value).toBe('added')
    expect((screen.getByLabelText('Continue playlists') as HTMLInputElement).checked).toBe(false)
    expect(save().disabled).toBe(true)
    await fireEvent.click(screen.getByLabelText('Continue playlists'))
    expect(save().disabled).toBe(false)
  })

  it('saves only the changed preferences and reports success', async () => {
    const stored = prefs({ library_sort: 'year' })
    const fetch = mockFetch({ 'PATCH /api/preferences': () => json(200, { preferences: stored }) })
    const p = props()
    const { rerender } = render(SettingsView, { props: p })
    await fireEvent.change(screen.getByLabelText('Library sort order'), { target: { value: 'year' } })
    await fireEvent.click(save())
    await vi.waitFor(() => expect(p.onSaved).toHaveBeenCalledWith(stored))
    expect(patches(fetch)).toEqual([{ library_sort: 'year' }])
    // The parent passes the saved values back down.
    await rerender({ ...p, preferences: stored })
    expect(await screen.findByText('Settings saved.')).toBeTruthy()
    expect(save().disabled).toBe(true)
  })

  it('does not pretend a failed save worked', async () => {
    mockFetch({ 'PATCH /api/preferences': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
    const p = props()
    render(SettingsView, { props: p })
    await fireEvent.click(screen.getByLabelText('Continue playlists'))
    await fireEvent.click(save())
    expect((await screen.findByRole('alert')).textContent).toBe('Not saved: The server had a problem. Try again.')
    expect(p.onSaved).not.toHaveBeenCalled()
    expect(screen.queryByText('Settings saved.')).toBeNull()
    // The unsaved change is kept for another try.
    expect((screen.getByLabelText('Continue playlists') as HTMLInputElement).checked).toBe(false)
    expect(save().disabled).toBe(false)
  })

  it('reports an expired session', async () => {
    mockFetch({ 'PATCH /api/preferences': () => apiError(401, 'unauthenticated', 'sign in required') })
    const p = props()
    render(SettingsView, { props: p })
    await fireEvent.click(screen.getByLabelText('Continue playlists'))
    await fireEvent.click(save())
    await vi.waitFor(() => expect(p.onUnauthorized).toHaveBeenCalled())
  })

  it('explains a load failure and blocks saving until settings load', async () => {
    const p = props({ status: 'error', loadError: 'Cannot reach the Rewind server.' })
    render(SettingsView, { props: p })
    expect(screen.getByRole('alert').textContent).toContain('Could not load your settings: Cannot reach the Rewind server.')
    expect((screen.getByLabelText('Library sort order') as HTMLSelectElement).disabled).toBe(true)
    expect(radio('Light').matches(':disabled')).toBe(true) // via the disabled fieldset
    expect(save().disabled).toBe(true)
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(p.onRetry).toHaveBeenCalled()
  })

  it('shows a loading state', () => {
    render(SettingsView, { props: props({ status: 'loading' }) })
    expect(screen.getByText('Loading settings…')).toBeTruthy()
  })
})

describe('SettingsView: theme', () => {
  it('lists the built-in themes as one labelled radio group, marking the saved one in text', () => {
    render(SettingsView, { props: props({ preferences: prefs({ theme: 'light' }) }) })
    const group = screen.getByRole('group', { name: 'Theme' })
    const radios = within(group).getAllByRole('radio') as HTMLInputElement[]
    expect(radios.map((r) => r.labels?.[0].textContent?.replace(/\s+/g, ' ').trim())).toEqual(['Rewind Dark', 'Light Current', 'OLED Black'])
    expect(radios.map((r) => r.checked)).toEqual([false, true, false])
    // One name: browsers move between them with the arrow keys, and Tab enters at the checked one.
    expect(new Set(radios.map((r) => r.name)).size).toBe(1)
  })

  it('falls back to Rewind Dark for an unknown saved theme', () => {
    render(SettingsView, { props: props({ preferences: prefs({ theme: 'solarized' }) }) })
    expect(radio('Rewind Dark').checked).toBe(true)
  })

  it('applies a theme at once and reports it saved only after the server confirms', async () => {
    const pending = deferred<Response>()
    const fetch = mockFetch({ 'PATCH /api/preferences': () => pending.promise })
    const p = props()
    render(SettingsView, { props: p })
    await fireEvent.click(radio('OLED Black'))
    expect(p.onPreviewTheme).toHaveBeenLastCalledWith('oled-black')
    expect(radio('OLED Black').checked).toBe(true)
    expect(screen.getByText('Saving theme…')).toBeTruthy()
    expect(screen.queryByText(/Theme saved/)).toBeNull()

    pending.resolve(json(200, { preferences: prefs({ theme: 'oled-black' }) }))
    expect(await screen.findByText('Theme saved: OLED Black.')).toBeTruthy()
    expect(p.onSaved).toHaveBeenCalledWith(prefs({ theme: 'oled-black' }))
    expect(p.onPreviewTheme).toHaveBeenLastCalledWith(null)
    expect(patches(fetch)).toEqual([{ theme: 'oled-black' }])
  })

  it('returns to the saved theme when saving fails', async () => {
    mockFetch({ 'PATCH /api/preferences': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
    const p = props({ preferences: prefs({ theme: 'light' }) })
    render(SettingsView, { props: p })
    await fireEvent.click(radio('OLED Black'))
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Theme not saved: The server had a problem. Try again. The saved theme is shown again.',
    )
    expect(p.onPreviewTheme).toHaveBeenLastCalledWith(null)
    expect(radio('Light').checked).toBe(true)
    expect(p.onSaved).not.toHaveBeenCalled()
  })

  it('saves quick successive choices one at a time, the last one last', async () => {
    const first = deferred<Response>()
    let n = 0
    const fetch = mockFetch({
      'PATCH /api/preferences': (init) => {
        n++
        const body = JSON.parse(String(init?.body))
        return n === 1 ? first.promise : json(200, { preferences: prefs(body) })
      },
    })
    render(SettingsView, { props: props() })
    await fireEvent.click(radio('Light'))
    await fireEvent.click(radio('OLED Black'))
    expect(patches(fetch)).toEqual([{ theme: 'light' }]) // the second waits
    first.resolve(json(200, { preferences: prefs({ theme: 'light' }) }))
    expect(await screen.findByText('Theme saved: OLED Black.')).toBeTruthy()
    expect(patches(fetch)).toEqual([{ theme: 'light' }, { theme: 'oled-black' }])
  })

  it('keeps unsaved library edits when a theme is saved', async () => {
    mockFetch({ 'PATCH /api/preferences': () => json(200, { preferences: prefs({ theme: 'light' }) }) })
    const p = props()
    const { rerender } = render(SettingsView, { props: p })
    await fireEvent.change(screen.getByLabelText('Library sort order'), { target: { value: 'title' } })
    await fireEvent.click(radio('Light'))
    await screen.findByText('Theme saved: Light.')
    await rerender({ ...p, preferences: prefs({ theme: 'light' }) })
    expect((screen.getByLabelText('Library sort order') as HTMLSelectElement).value).toBe('title')
    expect(save().disabled).toBe(false)
  })
})

describe('SettingsView: colors', () => {
  const hex = (label: string) => screen.getByLabelText(label) as HTMLInputElement
  const picker = (label: string) => screen.getByLabelText(`Pick the ${label.toLowerCase()}`) as HTMLInputElement
  const apply = () => screen.getByRole('button', { name: 'Apply colors' }) as HTMLButtonElement
  const reset = () => screen.getByRole('button', { name: 'Reset to defaults' }) as HTMLButtonElement
  const previewToken = (container: HTMLElement, token: string) =>
    (container.querySelector('.palette-preview') as HTMLElement).style.getPropertyValue(token)

  function storingFetch(initial: Preferences = prefs()) {
    const state = { stored: initial }
    const fetch = mockFetch({
      'PATCH /api/preferences': (init) => {
        state.stored = prefs({ ...state.stored, ...JSON.parse(String(init?.body)) })
        return json(200, { preferences: state.stored })
      },
    })
    return { fetch, state }
  }

  it("starts at the theme's colors, with nothing to apply or reset", () => {
    render(SettingsView, { props: props() })
    expect(hex('Primary color').value).toBe('#c6ff00')
    expect(hex('Secondary color').value).toBe('#262626')
    expect(hex('Accent color').value).toBe('#c6ff00')
    expect(screen.getAllByText('Theme default')).toHaveLength(3)
    expect(apply().disabled).toBe(true)
    expect(reset().disabled).toBe(true)
  })

  it('keeps an accent saved before the new colors existed; primary follows it', () => {
    render(SettingsView, { props: props({ preferences: prefs({ accent_color: '#ff8800' }) }) })
    expect(hex('Accent color').value).toBe('#ff8800')
    expect(hex('Primary color').value).toBe('#ff8800')
    expect(screen.getByText('Follows the accent')).toBeTruthy()
    expect(screen.getByText('Custom')).toBeTruthy()
    expect(reset().disabled).toBe(false)
  })

  it('rejects invalid formats and CSS for every color without contacting the server', async () => {
    const fetch = mockFetch({})
    render(SettingsView, { props: props() })
    for (const label of ['Primary color', 'Secondary color', 'Accent color']) {
      for (const bad of ['orange', '#f80', 'rgb(1,2,3)', '#ff8800;background:url(x)', 'url(https://x)', '#gggggg']) {
        await fireEvent.input(hex(label), { target: { value: bad } })
        await fireEvent.click(apply())
        expect(screen.getByText('Use a color like #c6ff00 (# and six hex digits).'), `${label} ${bad}`).toBeTruthy()
        expect(hex(label).getAttribute('aria-invalid')).toBe('true')
      }
      await fireEvent.click(reset()) // nothing saved: only discards the edit
      expect(hex(label).getAttribute('aria-invalid')).toBeNull()
    }
    expect(patches(fetch)).toEqual([])
  })

  it('does not flag a value that is still being typed', async () => {
    render(SettingsView, { props: props() })
    await fireEvent.input(hex('Primary color'), { target: { value: '#ff8' } })
    expect(screen.queryByText(/six hex digits/)).toBeNull()
    await fireEvent.input(hex('Primary color'), { target: { value: '#ff8z' } })
    expect(screen.getByText(/six hex digits/)).toBeTruthy()
  })

  it('blocks unreadable primary and accent colors; only warns about secondary', async () => {
    const fetch = mockFetch({})
    render(SettingsView, { props: props({ preferences: prefs({ theme: 'light' }) }) })
    await fireEvent.input(hex('Primary color'), { target: { value: '#ff8800' } })
    expect(screen.getByText(/Too little contrast with the Light background \(1\.9:1; at least 4\.5:1 is needed\)/)).toBeTruthy()
    await fireEvent.click(apply())
    expect(patches(fetch)).toEqual([])

    await fireEvent.click(reset())
    await fireEvent.input(hex('Secondary color'), { target: { value: '#111111' } })
    expect(screen.getByText(/Outlines of secondary buttons will be hard to see/)).toBeTruthy()
    expect(apply().disabled).toBe(false)
  })

  it('keeps each picker and hex field in sync', async () => {
    render(SettingsView, { props: props() })
    await fireEvent.input(picker('Secondary color'), { target: { value: '#123456' } })
    expect(hex('Secondary color').value).toBe('#123456')
    await fireEvent.input(hex('Secondary color'), { target: { value: '#ABCDEF' } })
    expect(picker('Secondary color').value).toBe('#abcdef')
    // An invalid hex value leaves the picker on the last color it can show.
    await fireEvent.input(hex('Secondary color'), { target: { value: 'nope' } })
    expect(picker('Secondary color').value).toBe('#262626')
  })

  it('previews edits live with the real tokens, before anything is saved', async () => {
    const fetch = mockFetch({})
    const { container } = render(SettingsView, { props: props() })
    expect(previewToken(container, '--rw-color-primary')).toBe('#c6ff00')
    await fireEvent.input(hex('Primary color'), { target: { value: '#80b3ff' } })
    expect(previewToken(container, '--rw-color-primary')).toBe('#80b3ff')
    expect(previewToken(container, '--rw-color-on-primary')).toBe('#000000')
    expect(screen.getByText('Not applied yet')).toBeTruthy()
    // Primary was chosen, so a new accent does not move it.
    await fireEvent.input(hex('Accent color'), { target: { value: '#ff8800' } })
    expect(previewToken(container, '--rw-color-accent')).toBe('#ff8800')
    expect(previewToken(container, '--rw-color-primary')).toBe('#80b3ff')
    await fireEvent.input(hex('Secondary color'), { target: { value: '#004466' } })
    expect(previewToken(container, '--rw-color-secondary')).toBe('#004466')
    expect(patches(fetch)).toEqual([])
    // The page itself is unchanged until the colors are applied.
    expect(document.documentElement.style.getPropertyValue('--rw-color-primary')).toBe('')
  })

  it('previews an unchosen primary following a new accent', async () => {
    const { container } = render(SettingsView, { props: props() })
    await fireEvent.input(hex('Accent color'), { target: { value: '#ff8800' } })
    expect(previewToken(container, '--rw-color-primary')).toBe('#ff8800')
  })

  it('applies all changed colors in one request (lower case) and resets them all', async () => {
    const { fetch, state } = storingFetch()
    const p = props()
    const { rerender } = render(SettingsView, { props: p })
    await fireEvent.input(hex('Primary color'), { target: { value: ' #FF8800 ' } })
    await fireEvent.input(hex('Secondary color'), { target: { value: '#004466' } })
    await fireEvent.click(apply())
    expect(await screen.findByText('Colors saved and applied.')).toBeTruthy()
    await rerender({ ...p, preferences: state.stored })
    expect(hex('Primary color').value).toBe('#ff8800')
    expect(screen.getAllByText('Custom')).toHaveLength(2)
    expect(apply().disabled).toBe(true)

    await fireEvent.click(reset())
    expect(await screen.findByText('Colors reset to the Rewind Dark defaults.')).toBeTruthy()
    await rerender({ ...p, preferences: state.stored })
    expect(hex('Primary color').value).toBe('#c6ff00')
    expect(hex('Secondary color').value).toBe('#262626')
    expect(patches(fetch)).toEqual([
      { primary_color: '#ff8800', secondary_color: '#004466' },
      { primary_color: '', secondary_color: '', accent_color: '' },
    ])
    expect(p.onSaved).toHaveBeenCalledTimes(2)
  })

  it('keeps unsaved color edits when another setting is saved', async () => {
    const p = props()
    const { rerender } = render(SettingsView, { props: p })
    await fireEvent.input(hex('Accent color'), { target: { value: '#ff8800' } })
    await rerender({ ...p, preferences: prefs({ autoplay_next: false }) })
    expect(hex('Accent color').value).toBe('#ff8800')
  })

  it('says when a saved color is unreadable on the chosen theme', () => {
    render(SettingsView, { props: props({ preferences: prefs({ theme: 'light', accent_color: '#ff8800', primary_color: '#ffff00' }) }) })
    expect(screen.getByText(/Your accent color #ff8800 is too hard to read on Light/)).toBeTruthy()
    expect(screen.getByText(/Your primary color #ffff00 is too hard to read on Light/)).toBeTruthy()
    expect(screen.getAllByText('Not readable on this theme')).toHaveLength(2)
  })

  it('reports a failed save', async () => {
    mockFetch({ 'PATCH /api/preferences': () => apiError(422, 'validation_failed', 'accent_color must be ""') })
    const p = props()
    render(SettingsView, { props: p })
    await fireEvent.input(hex('Accent color'), { target: { value: '#ff8800' } })
    await fireEvent.click(apply())
    expect((await screen.findByRole('alert')).textContent).toBe('Colors not saved: Accent_color must be "".')
    expect(p.onSaved).not.toHaveBeenCalled()
  })
})
