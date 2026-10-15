import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { ThemeInfo } from '../services/api'
import { apiError, deferred, json, mockFetch } from '../test/fetch'
import { themeState } from '../test/themes'
import ThemeEditor from './ThemeEditor.svelte'

function theme(id: string, over: Partial<ThemeInfo> = {}, active = 'rewind-dark'): ThemeInfo {
  const t = themeState(active, { [id]: over }).items.find((i) => i.id === id)!
  return t
}

function props(t: ThemeInfo, over: Record<string, unknown> = {}) {
  return { theme: t, onChanged: vi.fn(), onPreview: vi.fn(), onClose: vi.fn(), onUnauthorized: vi.fn(), ...over }
}

const field = (label: string) => screen.getByLabelText(label) as HTMLInputElement
const picker = (label: string) => screen.getByLabelText(`Pick the ${label.toLowerCase()} color`) as HTMLInputElement
const btn = (name: string | RegExp) => screen.getByRole('button', { name }) as HTMLButtonElement
const specimenVar = (container: HTMLElement, prop: string) => (container.querySelector('.specimen') as HTMLElement).style.getPropertyValue(prop)
const puts = (fetch: ReturnType<typeof mockFetch>) =>
  fetch.mock.calls.filter(([, i]) => i?.method === 'PUT').map(([u, i]) => [String(u), JSON.parse(String(i?.body))])

describe('ThemeEditor: controls', () => {
  it("offers the theme's customizable tokens, grouped, starting at the saved values", () => {
    render(ThemeEditor, { props: props(theme('rewind-dark', {}, 'rewind-dark')) })
    for (const legend of ['Colors', 'Typography', 'Borders and shape', 'Surfaces and spacing']) {
      expect(screen.getByRole('group', { name: legend })).toBeTruthy()
    }
    expect(field('Primary').value).toBe('#c6ff00')
    expect(field('Background').value).toBe('#0b0b0b')
    expect((screen.getByLabelText('Font') as HTMLSelectElement).value).toBe('system')
    expect(field('Button radius').value).toBe('10')
    expect(field('Button radius').getAttribute('aria-valuetext')).toBe('10 px')
    expect(screen.getAllByText('Theme default').length).toBeGreaterThan(20)
    expect(btn('Save').disabled).toBe(true)
    expect(btn('Reset to defaults').disabled).toBe(true)
  })

  it('hides tokens the theme does not allow (OLED keeps its black background)', () => {
    render(ThemeEditor, { props: props(theme('oled-black')) })
    expect(screen.queryByLabelText('Background')).toBeNull()
    expect(field('Surface')).toBeTruthy()
  })

  it('keeps each picker and hex field in sync', async () => {
    render(ThemeEditor, { props: props(theme('rewind-dark')) })
    await fireEvent.input(picker('Accent'), { target: { value: '#ff8800' } })
    expect(field('Accent').value).toBe('#ff8800')
    await fireEvent.input(field('Accent'), { target: { value: '#80B3FF' } })
    expect(picker('Accent').value).toBe('#80b3ff')
    expect(screen.getAllByText('Not saved yet')).toHaveLength(1)
  })

  it('rejects invalid formats and CSS without contacting the server, but not while typing', async () => {
    const fetch = mockFetch({})
    render(ThemeEditor, { props: props(theme('rewind-dark')) })
    await fireEvent.input(field('Accent'), { target: { value: '#ff8' } })
    expect(screen.queryByText(/Use a color like/)).toBeNull()
    for (const bad of ['orange', 'red;}body{', 'url(x)']) {
      await fireEvent.input(field('Accent'), { target: { value: bad } })
      expect(screen.getByText(/Use a color like #c6ff00/)).toBeTruthy()
      expect(field('Accent').getAttribute('aria-invalid')).toBe('true')
      expect(btn('Save').disabled).toBe(true)
    }
    expect(fetch).not.toHaveBeenCalled()
  })

  it('blocks unreadable colors and leaves them out of the preview; borders only get a note', async () => {
    const { container } = render(ThemeEditor, { props: props(theme('rewind-dark')) })
    await fireEvent.input(field('Text'), { target: { value: '#222222' } })
    expect(screen.getByText(/would be hard to read, so they cannot be saved/)).toBeTruthy()
    expect(screen.getByText(/Text on the background: 1\.\d:1, at least 4\.5:1 is needed/)).toBeTruthy()
    expect(btn('Save').disabled).toBe(true)
    expect(specimenVar(container, '--rw-color-text')).toBe('#f2f2f2')

    await fireEvent.click(btn("Reset text to the theme's value"))
    await fireEvent.input(field('Secondary'), { target: { value: '#eeeeee' } })
    expect(screen.queryByText(/cannot be saved/)).toBeNull()
    expect(screen.getByText(/Outlines of secondary buttons: 1\.\d:1/)).toBeTruthy()
    expect(btn('Save').disabled).toBe(false)
  })
})

describe('ThemeEditor: live preview', () => {
  it('previews every change across the app and in the preview, before saving', async () => {
    const fetch = mockFetch({})
    const p = props(theme('light'))
    const { container } = render(ThemeEditor, { props: p })
    expect(p.onPreview).toHaveBeenLastCalledWith({ themeId: 'light', custom: {} })
    expect(specimenVar(container, '--rw-color-bg')).toBe('#f5f5f0')

    await fireEvent.input(field('Accent'), { target: { value: '#0055aa' } })
    await fireEvent.change(screen.getByLabelText('Font'), { target: { value: 'mono' } })
    await fireEvent.input(field('Button radius'), { target: { value: '3' } })
    expect(p.onPreview).toHaveBeenLastCalledWith({ themeId: 'light', custom: { 'color.accent': '#0055aa', 'font.family': 'mono', 'radius.button': 3 } })
    expect(specimenVar(container, '--rw-color-accent')).toBe('#0055aa')
    expect(specimenVar(container, '--rw-radius-button')).toBe('3px')
    expect(specimenVar(container, '--rw-font-sans')).toContain('monospace')
    expect(screen.getByText('Unsaved changes.')).toBeTruthy()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('ends the preview when it closes', () => {
    const p = props(theme('light'))
    const { unmount } = render(ThemeEditor, { props: p })
    unmount()
    expect(p.onPreview).toHaveBeenLastCalledWith(null)
  })

  it('shows the real screens: navigation, track list, dialog, mini player and Now Playing', () => {
    const { container } = render(ThemeEditor, { props: props(theme('light')) })
    const specimen = container.querySelector('.specimen') as HTMLElement
    expect(specimen.hasAttribute('inert')).toBe(true)
    for (const text of ['Library ←', 'Playing', 'Unavailable', 'Delete playlist?', 'Now playing', 'Pause']) {
      expect(within(specimen).getAllByText(text).length, text).toBeGreaterThan(0)
    }
    expect(specimen.querySelectorAll('.rw-button').length).toBeGreaterThan(4)
    expect(specimen.querySelectorAll('input[type="range"]')).toHaveLength(2)
  })
})

describe('ThemeEditor: saving', () => {
  it('saves only changed values for the active theme and applies them', async () => {
    const saved = themeState('light', { light: { customization: { 'color.accent': '#0055aa' }, customization_source: 'saved' } })
    const fetch = mockFetch({ 'PUT /api/themes/light/customization': () => json(200, saved) })
    const p = props(theme('light', {}, 'light'))
    const { rerender } = render(ThemeEditor, { props: p })
    await fireEvent.input(field('Accent'), { target: { value: '#0055AA' } })
    await fireEvent.input(field('Primary'), { target: { value: '#3a1fd6' } }) // equal to the theme's: not sent
    await fireEvent.click(btn('Save'))
    expect(await screen.findByText('Saved and applied: Rewind Light.')).toBeTruthy()
    expect(puts(fetch)).toEqual([['/api/themes/light/customization', { tokens: { 'color.accent': '#0055aa' } }]])
    expect(p.onChanged).toHaveBeenCalledWith(saved)

    // The parent passes the saved theme back: nothing is left unsaved.
    await rerender({ ...p, theme: saved.items[1] })
    expect(screen.getByText('Custom')).toBeTruthy()
    expect(screen.queryByText('Unsaved changes.')).toBeNull()
    expect(btn('Save').disabled).toBe(true)
  })

  it('saves and applies an inactive theme, or only saves it', async () => {
    const fetch = mockFetch({
      'PUT /api/themes/oled-black/customization': () => json(200, themeState()),
      'PUT /api/themes/active': () => json(200, themeState('oled-black')),
    })
    const p = props(theme('oled-black'))
    render(ThemeEditor, { props: p })
    await fireEvent.change(screen.getByLabelText('Density'), { target: { value: 'compact' } })
    await fireEvent.click(btn('Save only'))
    expect(await screen.findByText('Saved for Rewind OLED. It is not the active theme; apply it to use it.')).toBeTruthy()
    expect(puts(fetch)).toEqual([['/api/themes/oled-black/customization', { tokens: { 'spacing.density': 'compact' } }]])

    await fireEvent.click(btn('Save and apply'))
    expect(await screen.findByText('Saved and applied: Rewind OLED.')).toBeTruthy()
    expect(puts(fetch).slice(1)).toEqual([
      ['/api/themes/oled-black/customization', { tokens: { 'spacing.density': 'compact' } }],
      ['/api/themes/active', { id: 'oled-black' }],
    ])
  })

  it('applies an unchanged inactive theme without saving a customization', async () => {
    const fetch = mockFetch({ 'PUT /api/themes/active': () => json(200, themeState('light')) })
    render(ThemeEditor, { props: props(theme('light')) })
    await fireEvent.click(btn('Save and apply'))
    expect(await screen.findByText('Applied: Rewind Light.')).toBeTruthy()
    expect(puts(fetch)).toEqual([['/api/themes/active', { id: 'light' }]])
  })

  it('resets everything to the theme defaults, saved as an empty customization', async () => {
    const custom = { 'color.accent': '#0055aa', 'radius.button': 2 }
    const fetch = mockFetch({ 'PUT /api/themes/light/customization': () => json(200, themeState('light')) })
    render(ThemeEditor, { props: props(theme('light', { customization: custom, customization_source: 'saved' }, 'light')) })
    expect(field('Accent').value).toBe('#0055aa')
    await fireEvent.click(btn('Reset to defaults'))
    expect(field('Accent').value).toBe('#3a1fd6')
    expect(field('Button radius').value).toBe('10')
    expect(screen.getByText('Rewind Light defaults are shown. Save to keep them.')).toBeTruthy()
    await fireEvent.click(btn('Save'))
    await screen.findByText('Saved and applied: Rewind Light.')
    expect(puts(fetch)).toEqual([['/api/themes/light/customization', { tokens: {} }]])
  })

  it('keeps the edits and says so when saving fails', async () => {
    mockFetch({ 'PUT /api/themes/light/customization': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
    const p = props(theme('light', {}, 'light'))
    render(ThemeEditor, { props: p })
    await fireEvent.input(field('Accent'), { target: { value: '#0055aa' } })
    await fireEvent.click(btn('Save'))
    expect((await screen.findByRole('alert')).textContent).toBe('Not saved: The server had a problem. Try again. Your changes are still here.')
    expect(field('Accent').value).toBe('#0055aa')
    expect(p.onChanged).not.toHaveBeenCalled()
    expect(screen.queryByText(/Saved and applied/)).toBeNull()
  })

  it('says when the customization was saved but the theme not applied', async () => {
    mockFetch({
      'PUT /api/themes/oled-black/customization': () => json(200, themeState()),
      'PUT /api/themes/active': () => apiError(409, 'theme_disabled', 'this theme is disabled; enable it before applying it'),
    })
    render(ThemeEditor, { props: props(theme('oled-black')) })
    await fireEvent.change(screen.getByLabelText('Font'), { target: { value: 'serif' } })
    await fireEvent.click(btn('Save and apply'))
    expect((await screen.findByRole('alert')).textContent).toBe('Saved, but not applied: This theme is disabled; enable it before applying it.')
  })

  it('shows Saving… and blocks a second save while one is running', async () => {
    const pending = deferred<Response>()
    const fetch = mockFetch({ 'PUT /api/themes/light/customization': () => pending.promise })
    render(ThemeEditor, { props: props(theme('light', {}, 'light')) })
    await fireEvent.input(field('Accent'), { target: { value: '#0055aa' } })
    await fireEvent.click(btn('Save'))
    expect(btn('Saving…').disabled).toBe(true)
    pending.resolve(json(200, themeState('light')))
    await screen.findByText('Saved and applied: Rewind Light.')
    expect(puts(fetch)).toHaveLength(1)
  })
})

describe('ThemeEditor: leaving', () => {
  it('closes at once without changes', async () => {
    const p = props(theme('light'))
    render(ThemeEditor, { props: p })
    await fireEvent.click(btn('Close'))
    expect(p.onClose).toHaveBeenCalled()
  })

  it('asks before discarding unsaved changes', async () => {
    const p = props(theme('light'))
    render(ThemeEditor, { props: p })
    await fireEvent.change(screen.getByLabelText('Border style'), { target: { value: 'dashed' } })
    await fireEvent.click(btn('Cancel'))
    const dialog = screen.getByRole('alertdialog', { name: 'Discard unsaved changes?' })
    expect(document.activeElement?.id).toBe('discard-title')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(p.onClose).not.toHaveBeenCalled()
    expect((screen.getByLabelText('Border style') as HTMLSelectElement).value).toBe('dashed')

    await fireEvent.click(btn('Cancel'))
    await fireEvent.click(btn('Discard changes'))
    expect(p.onClose).toHaveBeenCalled()
  })

  it('warns before the page is left with unsaved changes', async () => {
    render(ThemeEditor, { props: props(theme('light')) })
    const clean = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(clean)
    expect(clean.defaultPrevented).toBe(false)
    await fireEvent.change(screen.getByLabelText('Font weight'), { target: { value: 'regular' } })
    const dirty = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(dirty)
    expect(dirty.defaultPrevented).toBe(true)
  })
})

describe('ThemeEditor: earlier custom colors', () => {
  it('explains colors carried over from before per-theme customization', () => {
    const legacy = { 'color.accent': '#ff8800', 'color.primary': '#ff8800' }
    render(ThemeEditor, { props: props(theme('rewind-dark', { customization: legacy, customization_source: 'legacy' })) })
    expect(screen.getByText(/come from the custom colors you chose before themes could be customized/)).toBeTruthy()
    expect(screen.getAllByText('Earlier custom color')).toHaveLength(2)
    expect(field('Accent').value).toBe('#ff8800')
  })

  it('says which saved values are unreadable with this theme', () => {
    render(ThemeEditor, {
      props: props(theme('light', { customization: { 'color.accent': '#ff8800' }, customization_source: 'legacy' })),
    })
    expect(screen.getByText(/Not used because they would be hard to read with this theme: Accent/)).toBeTruthy()
  })

  it('notes that a disabled theme must be enabled to apply it', () => {
    render(ThemeEditor, { props: props(theme('light', { enabled: false })) })
    expect(screen.getByText(/This theme is disabled/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Save and apply' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Save only' })).toBeNull()
  })
})
