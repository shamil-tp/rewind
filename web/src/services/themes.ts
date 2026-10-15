// Themes: declarative sets of design-token values (colors, type, shape,
// surfaces) defined on the server (internal/theme). The token contract,
// with each token's type and limits, is mirrored in theme-tokens.json, which
// a Go test keeps identical to the server's.
//
// A theme never supplies CSS. This module turns validated token values into
// the --rw-* custom properties of app.css with code of its own, and sets
// them through the CSS object model on <html> (or on one element, for
// previews). So switching needs no reload, no stylesheet is ever loaded, and
// no theme or user text can become CSS, HTML or script.
//
// On top of a theme, the user may customize the tokens the theme allows.
// Customized colors must stay readable: values that would make text
// unreadable are ignored (and reported), never applied.
//
// See docs/themes.md.

import contract from './theme-tokens.json'

export type TokenKind = 'color' | 'length' | 'number' | 'choice'
export type TokenGroup = 'colors' | 'typography' | 'shape' | 'surface'
export type TokenValue = string | number
/** Token name → value, e.g. {"color.accent": "#c6ff00", "radius.button": 10}. */
export type Tokens = Record<string, TokenValue>

export interface TokenSpec {
  name: string
  group: TokenGroup
  label: string
  help: string
  kind: TokenKind
  min?: number
  max?: number
  step?: number
  choices?: { value: string; label: string }[]
  default: TokenValue
  required: boolean
  customizable: boolean
}

export const THEME_API: number = contract.theme_api
export const TOKEN_SPECS = contract.tokens as readonly TokenSpec[]
const SPECS = new Map(TOKEN_SPECS.map((s) => [s.name, s]))

export function tokenSpec(name: string): TokenSpec | undefined {
  return SPECS.get(name)
}

export const TOKEN_GROUPS: readonly { id: TokenGroup; label: string }[] = [
  { id: 'colors', label: 'Colors' },
  { id: 'typography', label: 'Typography' },
  { id: 'shape', label: 'Borders and shape' },
  { id: 'surface', label: 'Surfaces and spacing' },
]

/** The default theme's ID and values (Rewind Dark, equal to app.css). */
export const DEFAULT_THEME = 'rewind-dark'
export const DEFAULT_TOKENS: Readonly<Tokens> = Object.freeze(Object.fromEntries(TOKEN_SPECS.map((s) => [s.name, s.default])))

/** The only accepted color format, as on the server. */
export const COLOR_PATTERN = /^#[0-9a-f]{6}$/

/**
 * The value if it is valid for the token, else undefined. Numbers are
 * rounded to the token's step so floating-point noise never reaches CSS.
 */
export function validToken(name: string, value: unknown): TokenValue | undefined {
  const spec = SPECS.get(name)
  if (!spec) return undefined
  switch (spec.kind) {
    case 'color':
      return typeof value === 'string' && COLOR_PATTERN.test(value) ? value : undefined
    case 'choice':
      return typeof value === 'string' && spec.choices?.some((c) => c.value === value) ? value : undefined
    default: {
      if (typeof value !== 'number' || !Number.isFinite(value)) return undefined
      const min = spec.min ?? 0
      const max = spec.max ?? 0
      const step = spec.step ?? 1
      if (value < min - 1e-9 || value > max + 1e-9) return undefined
      const steps = (value - min) / step
      if (Math.abs(steps - Math.round(steps)) > 1e-6) return undefined
      return Math.round((min + Math.round(steps) * step) * 1e6) / 1e6
    }
  }
}

// --- Contrast (WCAG 2.x relative luminance) ---

/** Minimum contrast for text, including links and the focus ring. */
export const MIN_TEXT_CONTRAST = 4.5
/** Minimum contrast for borders and other non-text indicators. */
export const MIN_UI_CONTRAST = 3

function luminance(hex: string): number {
  const channel = (i: number) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5)
}

/** WCAG contrast ratio of two #rrggbb colors, from 1 to 21. */
export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

/** Black or white, whichever is more readable on color. */
export function textOn(color: string): string {
  return contrast(color, '#000000') >= contrast(color, '#ffffff') ? '#000000' : '#ffffff'
}

/** "dark" when white text is more readable on the background. */
export function schemeFor(background: string): 'dark' | 'light' {
  return textOn(background) === '#ffffff' ? 'dark' : 'light'
}

export interface ContrastProblem {
  fg: string
  bg: string
  ratio: number
  min: number
  message: string
}

// Pairs the UI draws, as in internal/theme. Themes must meet all of them. For
// a user's customization the text pairs (4.5:1) block saving and the others
// are notes: an outline that is hard to see does not make text unreadable.
const PAIRS: [fg: string, bg: string, min: number, what: string][] = [
  ['color.text', 'color.background', MIN_TEXT_CONTRAST, 'Text on the background'],
  ['color.text', 'color.surface', MIN_TEXT_CONTRAST, 'Text on surfaces'],
  ['color.text-muted', 'color.background', MIN_TEXT_CONTRAST, 'Secondary text on the background'],
  ['color.text-muted', 'color.surface', MIN_TEXT_CONTRAST, 'Secondary text on surfaces'],
  ['color.primary', 'color.background', MIN_TEXT_CONTRAST, 'Primary color (links) on the background'],
  ['color.primary', 'color.surface', MIN_TEXT_CONTRAST, 'Primary color (links) on surfaces'],
  ['color.on-primary', 'color.primary', MIN_TEXT_CONTRAST, 'Text on primary buttons'],
  ['color.on-secondary', 'color.secondary', MIN_TEXT_CONTRAST, 'Text on secondary buttons'],
  ['color.accent', 'color.background', MIN_TEXT_CONTRAST, 'Accent (focus outlines) on the background'],
  ['color.accent', 'color.surface', MIN_TEXT_CONTRAST, 'Accent (focus outlines) on surfaces'],
  ['color.on-accent', 'color.accent', MIN_TEXT_CONTRAST, 'Text on accent markers'],
  ['color.danger', 'color.background', MIN_TEXT_CONTRAST, 'Error text on the background'],
  ['color.danger', 'color.surface', MIN_UI_CONTRAST, 'Error badges on surfaces'],
  ['color.border', 'color.background', MIN_UI_CONTRAST, 'Borders on the background'],
  ['color.border', 'color.secondary', MIN_UI_CONTRAST, 'Outlines of secondary buttons'],
]

/** Contrast problems of complete, valid tokens: blocking (text) and notes. */
export function checkContrast(tokens: Tokens): { blocking: ContrastProblem[]; notes: ContrastProblem[] } {
  const blocking: ContrastProblem[] = []
  const notes: ContrastProblem[] = []
  for (const [fg, bg, min, what] of PAIRS) {
    const ratio = contrast(String(tokens[fg]), String(tokens[bg]))
    if (ratio >= min - 1e-9) continue
    const problem = { fg, bg, ratio, min, message: `${what}: ${ratio.toFixed(1)}:1, at least ${min}:1 is needed.` }
    ;(min === MIN_TEXT_CONTRAST ? blocking : notes).push(problem)
  }
  return { blocking, notes }
}

// --- Resolving: theme values + the user's customization ---

/** The parts of a theme (from GET /api/themes) that resolving needs. */
export interface ThemeLike {
  id?: unknown
  tokens?: unknown
  customizable?: unknown
}

export interface ResolvedTheme {
  themeId: string
  /** A valid value for every token. */
  tokens: Tokens
  scheme: 'dark' | 'light'
  /** Customized tokens that are not used: invalid, not allowed, or unreadable. */
  ignored: string[]
}

// Customized colors are tried in this order when not all of them can be used.
const COLOR_ORDER = [
  'color.background',
  'color.surface',
  'color.text',
  'color.text-muted',
  'color.border',
  'color.secondary',
  'color.primary',
  'color.accent',
]

// Error colors tried when the theme's own is unreadable on a customized
// background: the dark and light themes' danger colors.
const DANGER_FALLBACKS = ['#ff5c5c', '#b3001b']

/**
 * Theme colors with custom ones on top. Text on a customized fill is
 * derived from the fill, and the error color follows a customized
 * background, so only the customizable colors can make text unreadable.
 */
export function withColors(base: Tokens, custom: Tokens): Tokens {
  const t: Tokens = { ...base, ...custom }
  const has = (n: string) => n in custom
  if (has('color.primary')) t['color.on-primary'] = textOn(String(t['color.primary']))
  if (has('color.accent')) t['color.on-accent'] = textOn(String(t['color.accent']))
  if (has('color.secondary')) {
    const text = String(t['color.text'])
    const fill = String(t['color.secondary'])
    t['color.on-secondary'] = contrast(text, fill) >= MIN_TEXT_CONTRAST ? text : textOn(fill)
  }
  if (has('color.background') || has('color.surface')) {
    const bg = String(t['color.background'])
    const surface = String(t['color.surface'])
    const options = [String(base['color.danger']), ...DANGER_FALLBACKS]
    t['color.danger'] =
      options.find((c) => contrast(c, bg) >= MIN_TEXT_CONTRAST && contrast(c, surface) >= MIN_UI_CONTRAST) ??
      options.reduce((a, b) => (contrast(b, bg) > contrast(a, bg) ? b : a))
  }
  return t
}

/** A theme's own values: valid tokens from the server, defaults elsewhere. */
export function themeTokens(theme: ThemeLike | undefined): Tokens {
  const out: Tokens = { ...DEFAULT_TOKENS }
  const given = theme?.tokens && typeof theme.tokens === 'object' ? (theme.tokens as Record<string, unknown>) : {}
  for (const spec of TOKEN_SPECS) {
    const v = validToken(spec.name, given[spec.name])
    if (v !== undefined) out[spec.name] = v
  }
  // The server only lists readable themes; anything else (a damaged hint, a
  // mismatched server) falls back to the default colors.
  if (checkContrast(out).blocking.length > 0) {
    for (const spec of TOKEN_SPECS) if (spec.kind === 'color') out[spec.name] = DEFAULT_TOKENS[spec.name]
  }
  return out
}

/**
 * The tokens to show for a theme and a customization. Customized values the
 * theme does not allow, that are invalid, or whose colors would make text
 * unreadable are not used and are listed in `ignored`; they are never
 * changed or deleted.
 */
export function resolveTheme(theme: ThemeLike | undefined, custom?: unknown): ResolvedTheme {
  const base = themeTokens(theme)
  const allowed = new Set(Array.isArray(theme?.customizable) ? theme.customizable.filter((n) => typeof n === 'string') : [])
  const wanted = custom && typeof custom === 'object' ? (custom as Record<string, unknown>) : {}
  const tokens: Tokens = { ...base }
  const ignored: string[] = []
  const colors: Tokens = {}

  for (const [name, raw] of Object.entries(wanted)) {
    const v = allowed.has(name) ? validToken(name, raw) : undefined
    if (v === undefined) ignored.push(name)
    else if (SPECS.get(name)?.kind === 'color') colors[name] = v
    else tokens[name] = v
  }

  let accepted: Tokens = colors
  if (checkContrast(withColors(base, colors)).blocking.length > 0) {
    // Not all at once: keep each color that is readable with those kept so far.
    accepted = {}
    for (const name of COLOR_ORDER) {
      if (!(name in colors)) continue
      const next = { ...accepted, [name]: colors[name] }
      if (checkContrast(withColors(base, next)).blocking.length > 0) ignored.push(name)
      else accepted = next
    }
  }
  const colored = withColors(base, accepted)
  for (const spec of TOKEN_SPECS) if (spec.kind === 'color') tokens[spec.name] = colored[spec.name]
  const themeId = typeof theme?.id === 'string' ? theme.id : DEFAULT_THEME
  return { themeId, tokens, scheme: schemeFor(String(tokens['color.background'])), ignored: ignored.sort() }
}

// --- CSS custom properties ---

// Font stacks of locally installed fonts only; nothing is downloaded.
export const FONT_STACKS: Record<string, string> = {
  system: "system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif",
  humanist: "Seravek, 'Gill Sans Nova', Ubuntu, Calibri, 'DejaVu Sans', source-sans-pro, sans-serif",
  geometric: "Avenir, Montserrat, Corbel, 'URW Gothic', source-sans-pro, sans-serif",
  serif: "Charter, 'Bitstream Charter', 'Sitka Text', Cambria, serif",
  mono: "ui-monospace, 'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace",
}
const WEIGHTS: Record<string, [bold: number, black: number]> = { regular: [600, 700], strong: [700, 800], heavy: [800, 900] }
const SHADOWS: Record<string, number> = { none: 0, small: 2, medium: 4, large: 6 }
const DENSITY: Record<string, number> = { compact: 0.8, comfortable: 1, spacious: 1.2 }
const SPACE_REM = [0.25, 0.5, 0.75, 1, 1.5, 2.5]

const COLOR_VARS: Record<string, string> = {
  'color.background': '--rw-color-bg',
  'color.surface': '--rw-color-surface',
  'color.text': '--rw-color-text',
  'color.text-muted': '--rw-color-muted',
  'color.border': '--rw-color-border',
  'color.primary': '--rw-color-primary',
  'color.on-primary': '--rw-color-on-primary',
  'color.secondary': '--rw-color-secondary',
  'color.on-secondary': '--rw-color-on-secondary',
  'color.accent': '--rw-color-accent',
  'color.on-accent': '--rw-color-on-accent',
  'color.danger': '--rw-color-danger',
}
const RADIUS_VARS: Record<string, string> = {
  'radius.small': '--rw-radius-sm',
  'radius.medium': '--rw-radius-md',
  'radius.large': '--rw-radius-lg',
  'radius.xlarge': '--rw-radius-xl',
  'radius.button': '--rw-radius-button',
  'radius.input': '--rw-radius-input',
  'radius.artwork': '--rw-radius-art',
}

const px = (n: number) => `${n}px`
const rem = (n: number) => `${Math.round(n * 10000) / 10000}rem`

/**
 * The value of every themeable custom property for valid tokens. Every
 * value is built here from numbers, fixed names and #rrggbb colors; none is
 * text from a theme or a user.
 */
export function cssVariables(t: Tokens): Record<string, string> {
  const get = (name: string): TokenValue => validToken(name, t[name]) ?? DEFAULT_TOKENS[name]
  const num = (name: string) => Number(get(name))
  const str = (name: string) => String(get(name))
  const vars: Record<string, string> = {}
  for (const [name, prop] of Object.entries(COLOR_VARS)) vars[prop] = str(name)
  for (const [name, prop] of Object.entries(RADIUS_VARS)) vars[prop] = px(num(name))
  vars['--rw-radius-xs'] = px(Math.min(4, num('radius.small')))

  const width = num('border.width')
  vars['--rw-border-width'] = px(width)
  vars['--rw-border-style'] = str('border.style')
  vars['--rw-border'] = `${px(width)} ${str('border.style')} ${str('color.border')}`

  const shadow = SHADOWS[str('shadow.size')] ?? 4
  vars['--rw-shadow-offset'] = px(shadow)
  vars['--rw-shadow'] = shadow === 0 ? 'none' : `${px(shadow)} ${px(shadow)} 0 0 ${str('color.accent')}`
  vars['--rw-focus-ring'] = `3px solid ${str('color.accent')}`

  const size = num('font.size') / 16
  const scale = num('font.heading-scale')
  const [bold, black] = WEIGHTS[str('font.weight')] ?? WEIGHTS.heavy
  vars['--rw-font-sans'] = FONT_STACKS[str('font.family')] ?? FONT_STACKS.system
  vars['--rw-font-size-sm'] = rem(size * 0.875)
  vars['--rw-font-size-md'] = rem(size)
  vars['--rw-font-size-lg'] = rem(size * 1.25 * scale)
  vars['--rw-heading-scale'] = String(scale)
  vars['--rw-font-weight-bold'] = String(bold)
  vars['--rw-font-weight-black'] = String(black)
  vars['--rw-line-height'] = String(num('font.line-height'))

  const density = DENSITY[str('spacing.density')] ?? 1
  SPACE_REM.forEach((v, i) => (vars[`--rw-space-${i + 1}`] = rem(v * density)))
  vars['--rw-space-panel'] = rem(num('spacing.panel') / 16)
  return vars
}

/** Every custom property a theme sets. */
export const THEME_VARIABLES: readonly string[] = Object.keys(cssVariables(DEFAULT_TOKENS))

// --- Applying ---

/**
 * Sets every theme property on one element, so everything inside it uses
 * these values (previews use the real tokens and classes this way).
 */
export function applyTokens(element: HTMLElement, tokens: Tokens): void {
  for (const [prop, value] of Object.entries(cssVariables(tokens))) element.style.setProperty(prop, value)
}

/**
 * Svelte action: styles an element (a preview) with tokens, through the
 * CSS object model, which the Content-Security-Policy allows (inline style
 * attributes are blocked).
 */
export function styleWith(element: HTMLElement, tokens: Tokens) {
  applyTokens(element, tokens)
  return { update: (next: Tokens) => applyTokens(element, next) }
}

/**
 * Applies a resolved theme to the page: every property on <html>, the
 * native color scheme and the browser's theme color. Every property is set
 * each time, so nothing from a previous theme remains.
 */
export function applyTheme(resolved: ResolvedTheme, doc: Document = document): void {
  const root = doc.documentElement
  applyTokens(root, resolved.tokens)
  root.style.colorScheme = resolved.scheme
  root.dataset.theme = resolved.themeId
  doc.querySelector('meta[name="theme-color"]')?.setAttribute('content', String(resolved.tokens['color.background']))
  doc.querySelector('meta[name="color-scheme"]')?.setAttribute('content', resolved.scheme)
}

// --- Choosing what to show ---

/** What Settings is previewing: a theme, with draft values or its saved ones. */
export interface ThemePreview {
  themeId: string
  /** Draft customization; undefined shows the theme's saved customization. */
  custom?: Tokens
}

/** The parts of the theme state (GET /api/themes) needed to pick a theme. */
export interface ThemeStateLike {
  active: string
  items: (ThemeLike & { id: string; state: string; enabled?: boolean; customization?: unknown })[]
}

/**
 * The theme to show: the preview if any, else the active theme, else the
 * default. Only available themes are used; anything else falls back.
 */
export function resolveShown(state: ThemeStateLike, preview: ThemePreview | null): ResolvedTheme {
  const usable = (id: string) => state.items.find((t) => t.id === id && t.state === 'available')
  const previewed = preview ? usable(preview.themeId) : undefined
  if (previewed && preview) return resolveTheme(previewed, preview.custom ?? previewed.customization)
  const theme = usable(state.active) ?? usable(DEFAULT_THEME)
  return resolveTheme(theme, theme?.customization)
}

// The server is the only record of the chosen theme. Its state is readable
// only after sign-in, so the values last shown are also remembered in this
// browser and applied at startup, to avoid showing the default colors first.
// The hint holds token values only (no account data) and is validated like
// server data; it never overrides the server.
const HINT_KEY = 'rewind.theme'
const HINT_VERSION = 2

export function rememberTheme(resolved: ResolvedTheme): void {
  try {
    localStorage.setItem(HINT_KEY, JSON.stringify({ v: HINT_VERSION, theme: resolved.themeId, values: resolved.tokens }))
  } catch {
    // Storage may be unavailable (private mode); the hint is optional.
  }
}

/** Applies the remembered theme, if any. Invalid hints are ignored. */
export function applyRememberedTheme(doc: Document = document): void {
  let hint: unknown
  try {
    hint = JSON.parse(localStorage.getItem(HINT_KEY) ?? 'null')
  } catch {
    return
  }
  if (!hint || typeof hint !== 'object') return
  const { v, theme, values } = hint as Record<string, unknown>
  // Hints from before themes had tokens (no "v") are ignored once.
  if (v !== HINT_VERSION || typeof theme !== 'string' || !/^[a-z][a-z0-9-]{0,39}$/.test(theme) || !values || typeof values !== 'object') return
  applyTheme(resolveTheme({ id: theme, tokens: values }), doc)
}
