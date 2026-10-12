// Built-in color themes. A theme is only a set of colors for the --rw-color-*
// tokens in app.css; layout, typography and spacing never change. Themes are
// applied by setting those custom properties on <html>, so switching needs no
// reload and no stylesheet is ever loaded or generated from user input.
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
  /** Buttons, links, focus ring, selection markers. Also used as text. */
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

/**
 * Why accent cannot be used with theme, or undefined if it can. The accent is
 * used as text on the background and on surfaces, so both must reach the text
 * contrast minimum. Text on the accent needs no check: textOn picks black or
 * white, and one of them always reaches at least 4.58:1 against any color.
 */
export function accentProblem(accent: string, theme: Theme): string | undefined {
  if (!COLOR_PATTERN.test(accent)) return 'Use a color like #c6ff00 (six hex digits).'
  const worst = Math.min(contrast(accent, theme.colors.bg), contrast(accent, theme.colors.surface))
  if (worst < MIN_TEXT_CONTRAST) {
    return `Too little contrast with the ${theme.name} background (${worst.toFixed(1)}:1; at least ${MIN_TEXT_CONTRAST}:1 is needed).`
  }
  return undefined
}

export interface ResolvedTheme {
  theme: Theme
  colors: ThemeColors
  /** True when a custom accent was set but is unreadable with this theme. */
  accentIgnored: boolean
}

/** The colors to show for a saved theme ID and custom accent ("" for none). */
export function resolveTheme(themeId: unknown, accent: unknown): ResolvedTheme {
  const theme = themeById(themeId)
  if (typeof accent !== 'string' || accent === '') return { theme, colors: theme.colors, accentIgnored: false }
  if (accentProblem(accent, theme)) return { theme, colors: theme.colors, accentIgnored: true }
  return { theme, colors: { ...theme.colors, accent, onAccent: textOn(accent) }, accentIgnored: false }
}

// --- Applying ---

/**
 * Sets every color token on <html>, plus the native color scheme and the
 * browser's theme-color. Only validated #rrggbb values reach setProperty.
 */
export function applyTheme(resolved: ResolvedTheme, doc: Document = document): void {
  const root = doc.documentElement
  for (const [key, prop] of Object.entries(TOKENS) as [keyof ThemeColors, string][]) {
    const value = resolved.colors[key]
    if (COLOR_PATTERN.test(value)) root.style.setProperty(prop, value)
  }
  root.style.colorScheme = resolved.theme.scheme
  root.dataset.theme = resolved.theme.id
  doc.querySelector('meta[name="theme-color"]')?.setAttribute('content', resolved.colors.bg)
  doc.querySelector('meta[name="color-scheme"]')?.setAttribute('content', resolved.theme.scheme)
}

// The server is the only record of the chosen theme. Its preferences are
// readable only after sign-in, so the last applied theme is also remembered in
// this browser and applied at startup, to avoid showing the default colors
// first. This hint never overrides the server; it holds no secrets.
const HINT_KEY = 'rewind.theme'

export function rememberTheme(themeId: ThemeId, accent: string): void {
  try {
    localStorage.setItem(HINT_KEY, JSON.stringify({ theme: themeId, accent }))
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
  const { theme, accent } = hint as { theme?: unknown; accent?: unknown }
  if (!isThemeId(theme)) return
  applyTheme(resolveTheme(theme, typeof accent === 'string' && COLOR_PATTERN.test(accent) ? accent : ''), doc)
}
