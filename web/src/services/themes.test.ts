import { afterEach, describe, expect, it } from 'vitest'
import appCss from '../app.css?raw'
import { MANIFESTS, themeInfo, themeState } from '../test/themes'
import {
  applyRememberedTheme,
  applyTheme,
  applyTokens,
  checkContrast,
  contrast,
  cssVariables,
  DEFAULT_THEME,
  DEFAULT_TOKENS,
  FONT_STACKS,
  MIN_TEXT_CONTRAST,
  rememberTheme,
  resolveShown,
  resolveTheme,
  textOn,
  THEME_API,
  THEME_VARIABLES,
  TOKEN_SPECS,
  validToken,
} from './themes'

// Component sources, to check that their styles use tokens only.
const components = import.meta.glob(['../App.svelte', '../components/*.svelte'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

const [darkInfo, lightInfo, oledInfo] = MANIFESTS.map((m) => themeInfo(m))

function resetDocument() {
  document.documentElement.removeAttribute('style')
  delete document.documentElement.dataset.theme
  document.head.innerHTML = ''
  localStorage.clear()
}

function rootDefaults(): Record<string, string> {
  const root = /:root\s*\{([^}]*)\}/.exec(appCss)?.[1] ?? ''
  return Object.fromEntries([...root.matchAll(/(--rw-[a-z0-9-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]))
}

describe('token contract', () => {
  it('is theme API 1, with a valid default for every token', () => {
    expect(THEME_API).toBe(1)
    expect(TOKEN_SPECS.length).toBeGreaterThanOrEqual(25)
    for (const s of TOKEN_SPECS) expect(validToken(s.name, s.default), s.name).toBe(s.default)
    // The customizable colors are the eight roles of the editor.
    expect(TOKEN_SPECS.filter((s) => s.kind === 'color' && s.customizable).map((s) => s.name)).toEqual([
      'color.background', 'color.surface', 'color.text', 'color.text-muted', 'color.border',
      'color.primary', 'color.secondary', 'color.accent',
    ])
  })

  it('app.css defines every themed property, with Rewind Dark values', () => {
    const css = rootDefaults()
    const vars = cssVariables(DEFAULT_TOKENS)
    for (const prop of THEME_VARIABLES) {
      expect(css[prop], `${prop} missing from app.css`).toBeDefined()
      // Composite properties refer to others with var() in app.css.
      if (!css[prop].includes('var(')) expect(css[prop], prop).toBe(vars[prop])
    }
    expect(css['--rw-font-sans']).toBe(FONT_STACKS.system)
  })

  it('Rewind Dark is the default', () => {
    expect(DEFAULT_THEME).toBe('rewind-dark')
    expect(resolveTheme(darkInfo).tokens).toEqual(DEFAULT_TOKENS)
  })
})

describe('validToken', () => {
  it('accepts only lower-case six-digit hex colors', () => {
    for (const bad of ['#FF8800', '#f80', 'ff8800', 'orange', 'rgb(255,136,0)', '#ff8800; background:url(x)', 'var(--x)', '', ' #ff8800', 42, null]) {
      expect(validToken('color.accent', bad), String(bad)).toBeUndefined()
    }
    expect(validToken('color.accent', '#ff8800')).toBe('#ff8800')
  })

  it('accepts only listed choices', () => {
    expect(validToken('font.family', 'serif')).toBe('serif')
    for (const bad of ['Comic Sans', 'serif; color: red', "url('x')", 1]) expect(validToken('font.family', bad)).toBeUndefined()
  })

  it('checks ranges and steps, and rounds away floating-point noise', () => {
    expect(validToken('radius.button', 16)).toBe(16)
    expect(validToken('radius.button', 17)).toBeUndefined()
    expect(validToken('radius.button', -1)).toBeUndefined()
    expect(validToken('border.width', 2.5)).toBeUndefined()
    expect(validToken('border.width', '2')).toBeUndefined()
    expect(validToken('font.line-height', 1.3 + 7 * 0.05)).toBe(1.65)
    expect(validToken('font.line-height', 1.52)).toBeUndefined()
    expect(validToken('spacing.panel', 26)).toBeUndefined()
    expect(validToken('font.size', Number.NaN)).toBeUndefined()
    expect(validToken('font.size', Infinity)).toBeUndefined()
  })

  it('knows no other tokens', () => {
    expect(validToken('selector', 'body')).toBeUndefined()
    expect(validToken('__proto__', '#ffffff')).toBeUndefined()
  })
})

describe('contrast', () => {
  it('matches the WCAG formula', () => {
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 5)
    expect(contrast('#ffffff', '#ffffff')).toBeCloseTo(1, 5)
    expect(contrast('#777777', '#ffffff')).toBeCloseTo(4.48, 2)
  })

  it('always finds readable black or white text for a fill', () => {
    // Why customized primary and accent colors need no check of the text on
    // them: black or white reaches 4.5:1 on every color.
    for (let r = 0; r < 256; r += 17)
      for (let g = 0; g < 256; g += 17)
        for (let b = 0; b < 256; b += 17) {
          const c = '#' + [r, g, b].map((v) => v.toString(16).padStart(2, '0')).join('')
          expect(contrast(c, textOn(c)), c).toBeGreaterThanOrEqual(MIN_TEXT_CONTRAST)
        }
  })

  it('every bundled theme passes every check', () => {
    for (const info of [darkInfo, lightInfo, oledInfo]) {
      const { blocking, notes } = checkContrast(resolveTheme(info).tokens)
      expect([...blocking, ...notes], info.id).toEqual([])
    }
  })
})

describe('resolveTheme', () => {
  it("uses the theme's own values without a customization", () => {
    const r = resolveTheme(lightInfo)
    expect(r.themeId).toBe('light')
    expect(r.scheme).toBe('light')
    expect(r.tokens['color.background']).toBe('#f5f5f0')
    expect(r.tokens['radius.button']).toBe(10)
    expect(r.ignored).toEqual([])
  })

  it('applies valid, allowed customizations and derives text on customized fills', () => {
    const r = resolveTheme(darkInfo, { 'color.primary': '#80b3ff', 'color.secondary': '#eeeeee', 'radius.button': 4, 'font.family': 'mono' })
    expect(r.tokens).toMatchObject({
      'color.primary': '#80b3ff',
      'color.on-primary': '#000000',
      'color.secondary': '#eeeeee',
      'color.on-secondary': '#000000', // the theme's light text would be unreadable on it
      'radius.button': 4,
      'font.family': 'mono',
    })
    expect(r.ignored).toEqual([])
  })

  it('ignores tokens the theme does not allow, unknown tokens and invalid values', () => {
    const r = resolveTheme(oledInfo, {
      'color.background': '#202020', // OLED keeps black
      'color.on-primary': '#ffffff', // derived, never customizable
      selector: 'body',
      'font.family': 'Papyrus',
      'color.accent': 'red;}body{display:none',
    })
    expect(r.tokens).toEqual(resolveTheme(oledInfo).tokens)
    expect(r.ignored).toEqual(['color.accent', 'color.background', 'color.on-primary', 'font.family', 'selector'])
  })

  it('ignores colors that would be unreadable, keeping the readable ones', () => {
    const r = resolveTheme(lightInfo, { 'color.accent': '#ff8800', 'color.secondary': '#aabbcc' })
    expect(r.ignored).toEqual(['color.accent'])
    expect(r.tokens['color.accent']).toBe('#3a1fd6')
    expect(r.tokens['color.secondary']).toBe('#aabbcc')
    // An unreadable text color, or a background alone that makes text unreadable.
    expect(resolveTheme(darkInfo, { 'color.text': '#222222' }).ignored).toEqual(['color.text'])
    expect(resolveTheme(darkInfo, { 'color.background': '#ffffff' }).ignored).toEqual(['color.background'])
  })

  it('accepts colors that are only readable together, and adapts the error color', () => {
    const lightOnDark = {
      'color.background': '#ffffff', 'color.surface': '#eeeeee', 'color.text': '#111111', 'color.text-muted': '#555555',
      'color.border': '#111111', 'color.primary': '#3a1fd6', 'color.accent': '#3a1fd6',
    }
    const r = resolveTheme(darkInfo, lightOnDark)
    expect(r.ignored).toEqual([])
    expect(r.scheme).toBe('light')
    expect(r.tokens['color.danger']).toBe('#b3001b') // Rewind Dark's red is unreadable on white
    expect(checkContrast(r.tokens).blocking).toEqual([])
  })

  it('falls back to the default colors for unreadable or broken theme data', () => {
    const broken = { id: 'x', tokens: { 'color.background': '#ffffff', 'color.text': '#fefefe' }, customizable: [] }
    expect(resolveTheme(broken).tokens['color.text']).toBe(DEFAULT_TOKENS['color.text'])
    expect(resolveTheme(undefined).tokens).toEqual(DEFAULT_TOKENS)
    expect(resolveTheme({ tokens: 'nope', customizable: 'nope' }, 'nope').tokens).toEqual(DEFAULT_TOKENS)
  })
})

describe('cssVariables', () => {
  it('maps tokens to properties built from fixed values', () => {
    const vars = cssVariables({
      ...DEFAULT_TOKENS,
      'border.width': 3, 'border.style': 'dashed', 'shadow.size': 'none', 'spacing.density': 'compact',
      'font.size': 18, 'font.heading-scale': 1.2, 'font.weight': 'regular', 'font.family': 'serif', 'radius.small': 2,
    })
    expect(vars).toMatchObject({
      '--rw-border-width': '3px',
      '--rw-border': '3px dashed #f2f2f2',
      '--rw-shadow': 'none',
      '--rw-shadow-offset': '0px',
      '--rw-space-4': '0.8rem',
      '--rw-font-size-md': '1.125rem',
      '--rw-font-size-lg': '1.6875rem',
      '--rw-heading-scale': '1.2',
      '--rw-font-weight-bold': '600',
      '--rw-font-sans': FONT_STACKS.serif,
      '--rw-radius-xs': '2px',
      '--rw-focus-ring': '3px solid #c6ff00',
    })
  })

  it('never outputs text that is not a valid token value', () => {
    const vars = cssVariables({ 'color.accent': 'red;}body{display:none', 'font.family': "x'; } * { color: red", 'radius.button': '1px; x' })
    const all = Object.values(vars).join(' ')
    expect(all).not.toMatch(/display|url\(|;|\{/)
    expect(vars['--rw-color-accent']).toBe('#c6ff00')
  })
})

describe('applying themes', () => {
  afterEach(resetDocument)

  it('sets every property, the color scheme and the browser theme color', () => {
    document.head.innerHTML = '<meta name="theme-color" content="#0b0b0b"><meta name="color-scheme" content="dark">'
    applyTheme(resolveTheme(lightInfo))
    const root = document.documentElement
    const vars = cssVariables(resolveTheme(lightInfo).tokens)
    for (const prop of THEME_VARIABLES) expect(root.style.getPropertyValue(prop), prop).toBe(vars[prop])
    expect(root.style.colorScheme).toBe('light')
    expect(root.dataset.theme).toBe('light')
    expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe('#f5f5f0')
    expect(document.querySelector('meta[name="color-scheme"]')?.getAttribute('content')).toBe('light')

    // Switching replaces every value; nothing from the previous theme remains.
    applyTheme(resolveTheme(darkInfo, { 'radius.button': 2 }))
    applyTheme(resolveTheme(oledInfo))
    const oledVars = cssVariables(resolveTheme(oledInfo).tokens)
    for (const prop of THEME_VARIABLES) expect(root.style.getPropertyValue(prop)).toBe(oledVars[prop])
  })

  it('styles a single element for previews', () => {
    const el = document.createElement('div')
    applyTokens(el, resolveTheme(lightInfo).tokens)
    expect(el.style.getPropertyValue('--rw-color-primary')).toBe('#3a1fd6')
    expect(document.documentElement.style.getPropertyValue('--rw-color-primary')).toBe('')
  })
})

describe('resolveShown', () => {
  it('shows the active theme with its saved customization', () => {
    const state = themeState('light', { light: { customization: { 'radius.button': 3 } } })
    const r = resolveShown(state, null)
    expect(r.themeId).toBe('light')
    expect(r.tokens['radius.button']).toBe(3)
  })

  it('shows a preview, with drafts or the saved customization', () => {
    const state = themeState('rewind-dark', { 'oled-black': { customization: { 'font.size': 18 } } })
    expect(resolveShown(state, { themeId: 'oled-black' }).tokens['font.size']).toBe(18)
    expect(resolveShown(state, { themeId: 'oled-black', custom: { 'font.size': 15 } }).tokens['font.size']).toBe(15)
    expect(resolveShown(state, { themeId: 'oled-black', custom: {} }).tokens['font.size']).toBe(16)
  })

  it('falls back to the default theme for anything unavailable', () => {
    const state = themeState('rewind-dark')
    expect(resolveShown({ ...state, active: 'gone' }, null).themeId).toBe('rewind-dark')
    expect(resolveShown(state, { themeId: 'gone' }).themeId).toBe('rewind-dark')
    const invalid = { ...state, active: 'light', items: state.items.map((t) => (t.id === 'light' ? { ...t, state: 'invalid' as const } : t)) }
    expect(resolveShown(invalid, null).themeId).toBe('rewind-dark')
  })
})

describe('remembered theme', () => {
  afterEach(resetDocument)

  it('applies the last shown theme at the next start', () => {
    rememberTheme(resolveTheme(lightInfo, { 'radius.button': 4 }))
    applyRememberedTheme()
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(document.documentElement.style.getPropertyValue('--rw-color-bg')).toBe('#f5f5f0')
    expect(document.documentElement.style.getPropertyValue('--rw-radius-button')).toBe('4px')
  })

  it('ignores tampered, outdated and invalid hints', () => {
    for (const hint of [
      'not json',
      '"light"',
      '{"theme":"light","accent":"#3a1fd6"}', // written by the previous version
      '{"v":2,"theme":"../x","values":{}}',
      '{"v":3,"theme":"light","values":{}}',
      '{"v":2,"theme":"light"}',
    ]) {
      localStorage.setItem('rewind.theme', hint)
      applyRememberedTheme()
      expect(document.documentElement.dataset.theme, hint).toBeUndefined()
      resetDocument()
    }
    // Invalid values are replaced by defaults; nothing else reaches the page.
    localStorage.setItem('rewind.theme', JSON.stringify({ v: 2, theme: 'light', values: { 'color.accent': 'red;x:url(y)', 'radius.button': 99 } }))
    applyRememberedTheme()
    expect(document.documentElement.style.getPropertyValue('--rw-color-accent')).toBe('#c6ff00')
    expect(document.documentElement.style.getPropertyValue('--rw-radius-button')).toBe('10px')
    expect(document.documentElement.getAttribute('style')).not.toContain('url(')
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
    expect(styled).toBeGreaterThanOrEqual(12)
  })

  it('buttons, inputs and panels use their own shape tokens', () => {
    expect(appCss).toMatch(/\.rw-button \{[^}]*border-radius: var\(--rw-radius-button\)/)
    expect(appCss).toMatch(/\.rw-input \{[^}]*border-radius: var\(--rw-radius-input\)/)
    expect(appCss).toMatch(/\.rw-panel \{[^}]*padding: var\(--rw-space-panel\)/)
  })

  it('keeps visible focus and reduced motion in the base styles', () => {
    expect(appCss).toMatch(/:focus-visible\s*\{\s*outline: var\(--rw-focus-ring\)/)
    expect(appCss).toMatch(/--rw-focus-ring: 3px solid var\(--rw-color-accent\)/)
    expect(appCss).toMatch(/@media \(prefers-reduced-motion: reduce\)/)
  })
})
