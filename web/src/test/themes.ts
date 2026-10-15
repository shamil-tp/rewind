// Theme state as GET /api/themes returns it, built from the real bundled
// manifests (internal/theme/bundled) the way the server builds it: omitted
// tokens take the contract defaults, and an omitted customizable list means
// every customizable token.
import dark from '../../../internal/theme/bundled/rewind-dark.json'
import light from '../../../internal/theme/bundled/light.json'
import oled from '../../../internal/theme/bundled/oled-black.json'
import type { ThemeInfo, ThemeState } from '../services/api'
import { DEFAULT_TOKENS, TOKEN_SPECS } from '../services/themes'

interface Manifest {
  id: string
  name: string
  version: string
  description?: string
  author?: string
  scheme: string
  tokens: Record<string, string | number>
  customizable?: string[]
}

export const MANIFESTS: Manifest[] = [dark, light, oled]

export function themeInfo(m: Manifest, over: Partial<ThemeInfo> = {}): ThemeInfo {
  return {
    id: m.id,
    name: m.name,
    version: m.version,
    description: m.description,
    author: m.author,
    scheme: m.scheme as 'dark' | 'light',
    source: 'bundled',
    state: 'available',
    enabled: true,
    active: false,
    tokens: { ...DEFAULT_TOKENS, ...m.tokens },
    customizable: m.customizable ?? TOKEN_SPECS.filter((s) => s.customizable).map((s) => s.name),
    customization: {},
    customization_source: 'none',
    ...over,
  }
}

/** The bundled themes, with per-theme overrides, and active marked. */
export function themeState(active = 'rewind-dark', over: Record<string, Partial<ThemeInfo>> = {}, extra: ThemeInfo[] = []): ThemeState {
  const items = [...MANIFESTS.map((m) => themeInfo(m, over[m.id])), ...extra].map((t) => ({ ...t, active: t.id === active }))
  return { theme_api: 1, directory_configured: false, active, default_theme: 'rewind-dark', items }
}

/** An installed theme that failed validation. */
export function brokenTheme(id: string, state: 'invalid' | 'incompatible', error: string): ThemeInfo {
  return { id, source: 'installed', state, error, enabled: false, active: false, customization: {}, customization_source: 'none' }
}
