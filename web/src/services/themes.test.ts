import { afterEach, describe, expect, it } from 'vitest'
import appCss from '../app.css?raw'
import {
  accentProblem,
  applyRememberedTheme,
  applyTheme,
  contrast,
  DEFAULT_THEME,
  isThemeId,
  MIN_TEXT_CONTRAST,
  MIN_UI_CONTRAST,
  rememberTheme,
  resolveTheme,
  textOn,
  themeById,
  THEMES,
  TOKENS,
  type ThemeColors,
} from './themes'

// Component sources, to check that their styles use tokens only.
const components = import.meta.glob(['../App.svelte', '../components/*.svelte'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

function resetDocument() {
  document.documentElement.removeAttribute('style')
  delete document.documentElement.dataset.theme
  document.head.innerHTML = ''
  localStorage.clear()
}

function rootDefaults(): Record<string, string> {
  const root = /:root\s*\{([^}]*)\}/.exec(appCss)?.[1] ?? ''
  return Object.fromEntries([...root.matchAll(/(--rw-color-[a-z-]+):\s*(#[0-9a-f]{6});/g)].map((m) => [m[1], m[2]]))
}

describe('built-in themes', () => {
  it('include Rewind Dark (the default), Light and OLED Black', () => {
    expect(THEMES.map((t) => t.name)).toEqual(['Rewind Dark', 'Light', 'OLED Black'])
    expect(DEFAULT_THEME).toBe('rewind-dark')
    expect(new Set(THEMES.map((t) => t.id)).size).toBe(THEMES.length)
  })

  it('each define every token as a #rrggbb color', () => {
    for (const t of THEMES) {
      expect(Object.keys(t.colors).sort()).toEqual(Object.keys(TOKENS).sort())
      for (const v of Object.values(t.colors)) expect(v, `${t.id}`).toMatch(/^#[0-9a-f]{6}$/)
    }
  })

  it('Rewind Dark is exactly the design in app.css', () => {
    const css = rootDefaults()
    const dark = themeById('rewind-dark').colors
    for (const [key, prop] of Object.entries(TOKENS) as [keyof ThemeColors, string][]) {
      expect(css[prop], prop).toBe(dark[key])
    }
    // app.css defines no color token that themes do not set.
    expect(Object.keys(css).sort()).toEqual(Object.values(TOKENS).sort())
  })

  // Pairs the UI actually draws. Text needs 4.5:1 (WCAG AA); borders 3:1.
  const pairs: [keyof ThemeColors, keyof ThemeColors, number][] = [
    ['text', 'bg', MIN_TEXT_CONTRAST],
    ['text', 'surface', MIN_TEXT_CONTRAST],
    ['muted', 'bg', MIN_TEXT_CONTRAST],
    ['muted', 'surface', MIN_TEXT_CONTRAST],
    ['accent', 'bg', MIN_TEXT_CONTRAST], // links, focus ring, selection bars
    ['accent', 'surface', MIN_TEXT_CONTRAST],
    ['onAccent', 'accent', MIN_TEXT_CONTRAST], // buttons, "Playing" markers
    ['danger', 'bg', MIN_TEXT_CONTRAST], // field errors are danger-colored text
    ['danger', 'surface', MIN_UI_CONTRAST], // badges and alert borders
    ['border', 'bg', MIN_UI_CONTRAST],
  ]
  for (const t of THEMES) {
    it(`${t.name} has readable contrast`, () => {
      for (const [fg, bg, min] of pairs) {
        const ratio = contrast(t.colors[fg], t.colors[bg])
        expect(ratio, `${t.id}: ${fg} on ${bg} = ${ratio.toFixed(2)}`).toBeGreaterThanOrEqual(min)
      }
    })
  }
})

describe('contrast', () => {
  it('matches the WCAG formula', () => {
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 5)
    expect(contrast('#ffffff', '#ffffff')).toBeCloseTo(1, 5)
    expect(contrast('#777777', '#ffffff')).toBeCloseTo(4.48, 2)
  })

  it('picks black or white text, whichever is more readable', () => {
    expect(textOn('#c6ff00')).toBe('#000000')
    expect(textOn('#3a1fd6')).toBe('#ffffff')
  })
})

describe('theme IDs', () => {
  it('fall back to the default when unknown, outdated or not a string', () => {
    for (const bad of ['solarized', '', 'REWIND-DARK', null, undefined, 42, { id: 'light' }]) {
      expect(isThemeId(bad)).toBe(false)
      expect(themeById(bad).id).toBe('rewind-dark')
    }
    expect(themeById('light').name).toBe('Light')
  })
})

describe('custom accent', () => {
  it('accepts only six-digit lower-case hex', () => {
    const dark = themeById('rewind-dark')
    for (const bad of ['#FF8800', '#f80', 'ff8800', 'orange', 'rgb(255,136,0)', '#ff8800; background:url(x)', 'var(--x)', '', ' #ff8800']) {
      expect(accentProblem(bad, dark), bad).toMatch(/six hex digits/)
    }
    expect(accentProblem('#ff8800', dark)).toBeUndefined()
  })

  it('rejects colors that would be unreadable on the theme', () => {
    expect(accentProblem('#ff8800', themeById('light'))).toMatch(/Too little contrast with the Light background \(1\.9:1/)
    expect(accentProblem('#111111', themeById('rewind-dark'))).toMatch(/Too little contrast/)
    expect(accentProblem('#777777', themeById('light'))).toMatch(/Too little contrast/)
  })

  it('always finds readable text for an accent', () => {
    // Why accentProblem needs no check of text on the accent: black or white
    // reaches 4.5:1 on every color. Sampled across the whole color cube.
    for (let r = 0; r < 256; r += 17)
      for (let g = 0; g < 256; g += 17)
        for (let b = 0; b < 256; b += 17) {
          const c = '#' + [r, g, b].map((v) => v.toString(16).padStart(2, '0')).join('')
          expect(contrast(c, textOn(c)), c).toBeGreaterThanOrEqual(MIN_TEXT_CONTRAST)
        }
  })

  it('replaces the accent and its text color only when readable', () => {
    const ok = resolveTheme('rewind-dark', '#ff8800')
    expect(ok.accentIgnored).toBe(false)
    expect(ok.colors).toEqual({ ...themeById('rewind-dark').colors, accent: '#ff8800', onAccent: '#000000' })

    const unreadable = resolveTheme('light', '#ff8800')
    expect(unreadable.accentIgnored).toBe(true)
    expect(unreadable.colors).toEqual(themeById('light').colors)

    for (const injected of ['red;}body{display:none', 'url(https://evil.example/x.png)', 42, null]) {
      const r = resolveTheme('light', injected)
      expect(r.colors).toEqual(themeById('light').colors)
    }
    expect(resolveTheme('light', '').accentIgnored).toBe(false)
  })
})

describe('applying themes', () => {
  afterEach(resetDocument)

  it('sets every token, the color scheme and the browser theme color', () => {
    document.head.innerHTML = '<meta name="theme-color" content="#0b0b0b"><meta name="color-scheme" content="dark">'
    applyTheme(resolveTheme('light', ''))
    const root = document.documentElement
    for (const [key, prop] of Object.entries(TOKENS) as [keyof ThemeColors, string][]) {
      expect(root.style.getPropertyValue(prop), prop).toBe(themeById('light').colors[key])
    }
    expect(root.style.colorScheme).toBe('light')
    expect(root.dataset.theme).toBe('light')
    expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe('#f5f5f0')
    expect(document.querySelector('meta[name="color-scheme"]')?.getAttribute('content')).toBe('light')

    // Switching replaces every value; nothing from the previous theme remains.
    applyTheme(resolveTheme('oled-black', ''))
    for (const [key, prop] of Object.entries(TOKENS) as [keyof ThemeColors, string][]) {
      expect(root.style.getPropertyValue(prop)).toBe(themeById('oled-black').colors[key])
    }
  })

  it('never writes a value that is not a plain color', () => {
    const bad = resolveTheme('light', '')
    applyTheme({ ...bad, colors: { ...bad.colors, accent: 'red;background:url(x)' } })
    expect(document.documentElement.style.getPropertyValue('--rw-color-accent')).toBe('')
    expect(document.documentElement.getAttribute('style')).not.toContain('url(')
  })

  it('remembers the last theme for the next start, ignoring tampered hints', () => {
    rememberTheme('light', '#3a1fd6')
    applyRememberedTheme()
    expect(document.documentElement.dataset.theme).toBe('light')
    resetDocument()

    for (const hint of ['not json', '{"theme":"evil"}', '{"theme":"light","accent":"red;x:url(y)"}', '"light"']) {
      localStorage.setItem('rewind.theme', hint)
      applyRememberedTheme()
      if (hint.includes('"light"') && hint.startsWith('{')) {
        // A valid theme with an invalid accent: the theme applies, the accent does not.
        expect(document.documentElement.style.getPropertyValue('--rw-color-accent')).toBe('#3a1fd6')
      } else {
        expect(document.documentElement.dataset.theme, hint).toBeUndefined()
      }
      resetDocument()
    }
  })
})

describe('styles', () => {
  it('components use color tokens only, never literal colors', () => {
    const literal = /#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(/
    let styled = 0
    for (const [file, source] of Object.entries(components)) {
      const style = /<style>([\s\S]*?)<\/style>/.exec(source)?.[1]
      if (style === undefined) continue
      styled++
      expect(literal.test(style), file).toBe(false)
    }
    expect(styled).toBeGreaterThanOrEqual(10)
  })

  it('keeps visible focus and reduced motion in the base styles', () => {
    expect(appCss).toMatch(/:focus-visible\s*\{\s*outline: var\(--rw-focus-ring\)/)
    expect(appCss).toMatch(/--rw-focus-ring: 3px solid var\(--rw-color-accent\)/)
    expect(appCss).toMatch(/@media \(prefers-reduced-motion: reduce\)/)
  })
})
