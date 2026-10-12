// Built-in color themes. A theme is only a set of colors for the --rw-color-*
// tokens in app.css; layout, typography and spacing never change. Themes are
// applied by setting those custom properties on <html>, so switching needs no
// reload and no stylesheet is ever loaded or generated from user input.
//
// On top of a theme, the user may choose three colors (preferences
// primary_color, secondary_color and accent_color; "" means the theme's):
//   primary    main buttons, links, selected navigation, primary actions
//   secondary  secondary buttons and supporting surfaces
//   accent     highlights: playback progress, the playing track, focus ring
// Before there were three roles, the accent also colored buttons. So a
// primary that was never chosen follows a custom accent, and existing accent
// preferences look exactly as before.
//
// To add a theme, see docs/themes.md: add an entry here, add its ID to
// prefs.Themes in internal/prefs/prefs.go, and run the tests (they check
// contrast and that both lists match).

export type ThemeId = 'rewind-dark' | 'light' | 'oled-black'

/** One color per --rw-color-* token, as lower-case #rrggbb. */
export interface ThemeColors {
  /** Page and player background. */
  bg: string
  /** Secondary surfaces: hovered and selected rows, panels. */
  surface: string
  /** Primary text. */
  text: string
  /** Secondary text. */
  muted: string
  /** Structural borders. */
  border: string
  /** Main buttons, links, selected navigation. Also used as text. */
  primary: string
  /** Text on primary backgrounds. */
  onPrimary: string
  /** Secondary buttons and supporting surfaces (artwork placeholder). */
  secondary: string
  /** Text on secondary backgrounds. */
  onSecondary: string
  /** Highlights: progress, playing markers, focus ring. Also used as text. */
  accent: string
  /** Text on accent backgrounds. */
  onAccent: string
  /** Errors and unavailable badges. Also used as text (field errors). */
  danger: string
}

export interface Theme {
  id: ThemeId
  name: string
  /** Tells the browser how to draw native controls and scrollbars. */
  scheme: 'dark' | 'light'
  colors: ThemeColors
}

export const DEFAULT_THEME: ThemeId = 'rewind-dark'

export const THEMES: readonly Theme[] = [
  {
    id: 'rewind-dark',
    name: 'Rewind Dark',
    scheme: 'dark',
    // Must equal the defaults in app.css (checked by a test).
    colors: {
      bg: '#0b0b0b',
      surface: '#151515',
      text: '#f2f2f2',
      muted: '#a3a3a3',
      border: '#f2f2f2',
      primary: '#c6ff00',
      onPrimary: '#0b0b0b',
      secondary: '#262626',
      onSecondary: '#f2f2f2',
      accent: '#c6ff00',
      onAccent: '#0b0b0b',
      danger: '#ff5c5c',
    },
  },
  {
    id: 'light',
    name: 'Light',
    scheme: 'light',
    colors: {
      bg: '#f5f5f0',
      surface: '#e4e4dc',
      text: '#0b0b0b',
      muted: '#4d4d4d',
      border: '#0b0b0b',
      primary: '#3a1fd6',
      onPrimary: '#ffffff',
      secondary: '#d6d6cc',
      onSecondary: '#0b0b0b',
      accent: '#3a1fd6',
      onAccent: '#ffffff',
      danger: '#b3001b',
    },
  },
  {
    id: 'oled-black',
    name: 'OLED Black',
    scheme: 'dark',
    colors: {
      bg: '#000000',
      surface: '#0c0c0c',
      text: '#ededed',
      muted: '#9e9e9e',
      border: '#d9d9d9',
      primary: '#c6ff00',
      onPrimary: '#000000',
      secondary: '#1c1c1c',
      onSecondary: '#ededed',
      accent: '#c6ff00',
      onAccent: '#000000',
      danger: '#ff5c5c',
    },
  },
]

/** The CSS custom property for each color. */
export const TOKENS: Record<keyof ThemeColors, string> = {
  bg: '--rw-color-bg',
  surface: '--rw-color-surface',
  text: '--rw-color-text',
  muted: '--rw-color-muted',
  border: '--rw-color-border',
  primary: '--rw-color-primary',
  onPrimary: '--rw-color-on-primary',
  secondary: '--rw-color-secondary',
  onSecondary: '--rw-color-on-secondary',
  accent: '--rw-color-accent',
  onAccent: '--rw-color-on-accent',
  danger: '--rw-color-danger',
}

export function isThemeId(value: unknown): value is ThemeId {
  return THEMES.some((t) => t.id === value)
}

/** The theme with this ID, or the default for unknown or outdated IDs. */
export function themeById(id: unknown): Theme {
  return THEMES.find((t) => t.id === id) ?? THEMES[0]
}

// --- Contrast (WCAG 2.x relative luminance) ---

/** The only accepted custom color format, as on the server. */
export const COLOR_PATTERN = /^#[0-9a-f]{6}$/

/** Minimum contrast for text, including accent-colored text. */
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

/** The colors a user can choose. "" (or a missing value) means the theme's. */
export type ColorRole = 'primary' | 'secondary' | 'accent'
export type CustomColors = Record<ColorRole, string>
export const COLOR_ROLES: readonly ColorRole[] = ['primary', 'secondary', 'accent']
export const NO_CUSTOM_COLORS: CustomColors = { primary: '', secondary: '', accent: '' }

/**
 * Why value cannot be used for role with theme, or undefined if it can.
 * Primary (links) and accent (focus ring, markers) are drawn as text on the
 * background and on surfaces, so both must reach the text contrast minimum.
 * Secondary is only ever a fill, with the theme's text or black or white on
 * it (whichever is readable), so it needs only the format check. Text on a
 * primary or accent fill needs no check: textOn picks black or white, and
 * one of them always reaches at least 4.58:1 against any color.
 */
export function colorProblem(role: ColorRole, value: string, theme: Theme): string | undefined {
  if (!COLOR_PATTERN.test(value)) return 'Use a color like #c6ff00 (# and six hex digits).'
  if (role === 'secondary') return undefined
  const worst = Math.min(contrast(value, theme.colors.bg), contrast(value, theme.colors.surface))
  if (worst < MIN_TEXT_CONTRAST) {
    return `Too little contrast with the ${theme.name} background (${worst.toFixed(1)}:1; at least ${MIN_TEXT_CONTRAST}:1 is needed).`
  }
  return undefined
}

/** The accent check alone; kept for the single-accent callers. */
export function accentProblem(accent: string, theme: Theme): string | undefined {
  return colorProblem('accent', accent, theme)
}

/**
 * A readability note that does not block saving, or undefined. A secondary
 * fill close to the border color hides the outline of secondary controls;
 * their text stays readable.
 */
export function colorWarning(role: ColorRole, value: string, theme: Theme): string | undefined {
  if (role !== 'secondary' || !COLOR_PATTERN.test(value)) return undefined
  const ratio = contrast(value, theme.colors.border)
  if (ratio < MIN_UI_CONTRAST) {
    return `Outlines of secondary buttons will be hard to see on this color (${ratio.toFixed(1)}:1 with the border). Text stays readable.`
  }
  return undefined
}

export interface ResolvedTheme {
  theme: Theme
  colors: ThemeColors
  /** Custom colors that are set but not used with this theme (unreadable or invalid). */
  ignored: ColorRole[]
  /** True when a custom accent was set but is unreadable with this theme. */
  accentIgnored: boolean
}

/** Text for a fill: the theme's text color if readable, else black or white. */
function textFor(fill: string, theme: Theme): string {
  return contrast(theme.colors.text, fill) >= MIN_TEXT_CONTRAST ? theme.colors.text : textOn(fill)
}

/**
 * The colors to show for a saved theme ID and custom colors. A string is
 * taken as the accent alone (the preference that existed first). Values that
 * are not plain colors, or are unreadable with the theme, are not used and
 * are listed in `ignored`, so the user can be told; they are never changed.
 */
export function resolveTheme(themeId: unknown, custom: unknown): ResolvedTheme {
  const theme = themeById(themeId)
  const wanted: Record<string, unknown> =
    typeof custom === 'string' ? { accent: custom } : custom && typeof custom === 'object' ? (custom as Record<string, unknown>) : {}
  const colors: ThemeColors = { ...theme.colors }
  const ignored: ColorRole[] = []
  const pick = (role: ColorRole): string | undefined => {
    const v = wanted[role]
    if (typeof v !== 'string' || v === '') return undefined
    if (colorProblem(role, v, theme)) {
      ignored.push(role)
      return undefined
    }
    return v
  }

  const accent = pick('accent')
  if (accent) Object.assign(colors, { accent, onAccent: textOn(accent) })
  // A primary that was never chosen follows a custom accent (see the top).
  const primary = pick('primary') ?? accent
  if (primary) Object.assign(colors, { primary, onPrimary: textOn(primary) })
  const secondary = pick('secondary')
  if (secondary) Object.assign(colors, { secondary, onSecondary: textFor(secondary, theme) })

  return { theme, colors, ignored, accentIgnored: ignored.includes('accent') }
}

// --- Applying ---

/**
 * Sets every color token on <html>, plus the native color scheme and the
 * browser's theme-color. Only validated #rrggbb values reach setProperty.
 */
export function applyTheme(resolved: ResolvedTheme, doc: Document = document): void {
  const root = doc.documentElement
  applyTokens(root, resolved.colors)
  root.style.colorScheme = resolved.theme.scheme
  root.dataset.theme = resolved.theme.id
  doc.querySelector('meta[name="theme-color"]')?.setAttribute('content', resolved.colors.bg)
  doc.querySelector('meta[name="color-scheme"]')?.setAttribute('content', resolved.theme.scheme)
}

/**
 * Sets the color tokens on one element, so everything inside it uses these
 * colors (the settings preview uses the real tokens this way). Only
 * validated #rrggbb values are written, through the CSS object model.
 */
export function applyTokens(element: HTMLElement, colors: ThemeColors): void {
  for (const [key, prop] of Object.entries(TOKENS) as [keyof ThemeColors, string][]) {
    const value = colors[key]
    if (COLOR_PATTERN.test(value)) element.style.setProperty(prop, value)
  }
}

// The server is the only record of the chosen theme. Its preferences are
// readable only after sign-in, so the last applied theme is also remembered in
// this browser and applied at startup, to avoid showing the default colors
// first: the theme ID and the three custom colors. This hint never overrides
// the server; it holds no secrets.
const HINT_KEY = 'rewind.theme'

export function rememberTheme(themeId: ThemeId, custom: CustomColors | string): void {
  const colors = typeof custom === 'string' ? { ...NO_CUSTOM_COLORS, accent: custom } : custom
  try {
    localStorage.setItem(HINT_KEY, JSON.stringify({ theme: themeId, ...colors }))
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
  const { theme, ...rest } = hint as Record<string, unknown>
  if (!isThemeId(theme)) return
  const custom = { ...NO_CUSTOM_COLORS }
  for (const role of COLOR_ROLES) {
    const v = rest[role]
    if (typeof v === 'string' && COLOR_PATTERN.test(v)) custom[role] = v
  }
  applyTheme(resolveTheme(theme, custom), doc)
}
