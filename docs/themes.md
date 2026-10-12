# Themes

Rewind has a few built-in color themes and an optional custom accent color.
A theme changes **colors only**: layout, typography, spacing and borders stay
the same, so every screen keeps the neo-brutalist structure.

Themes are part of the web UI. They are not files you install; there is no
theme upload, download, marketplace or CSS editor.

## Built-in themes

| ID | Name | Use |
| --- | --- | --- |
| `rewind-dark` | Rewind Dark | The default: the original dark design with the acid-lime accent. |
| `light` | Light | Near-white paper background, black text and borders, deep violet accent. For bright rooms and daylight. |
| `oled-black` | OLED Black | Pure black background and near-black surfaces, so OLED screens light fewer pixels; slightly dimmer borders. |

Choose one under **Settings → Appearance → Theme**. It applies immediately,
without a reload, and does not interrupt playback.

## How the choice is saved

- The theme is the `theme` preference, stored on the server with the other
  preferences (`PATCH /api/preferences`, see [api.md](api.md#preferences)).
  It belongs to the signed-in user and follows them to every device.
- Choosing a theme applies it at once and saves it at once. The settings page
  says "Theme saved" only after the server confirms. If saving fails, the
  previously saved theme comes back and an error explains why. Quick changes
  (for example with the arrow keys) are saved one at a time, so the last
  choice is the one stored.
- The server accepts only the IDs in the table. An unknown or outdated stored
  ID (for example after a downgrade) is shown as Rewind Dark.
- **Startup:** preferences can only be read after sign-in. So the browser also
  remembers the last theme it showed (`localStorage` key `rewind.theme`,
  holding only the theme ID and accent color) and applies it before the first
  render, to avoid a flash of the default colors. This is a cache: the
  server's value replaces it as soon as it loads, and an invalid or tampered
  value is ignored. It contains nothing about the account or session.
- Rewind does not follow the system's light/dark setting. It has always been
  dark-first, so with no saved theme it shows Rewind Dark, and a saved theme
  is never overridden.

## Custom accent color

**Settings → Appearance → Accent color** replaces the theme's accent (buttons,
links, focus ring, selection markers) with your own color. **Theme default**
removes it.

- Only six-digit lower-case hex colors such as `#ff8800` are accepted. The
  form lower-cases what you type, and the server checks the format again
  (`accent_color` preference). Color names, short hex, `rgb()`, `var()`,
  `url()` and anything else are rejected. The value is only ever set as one
  CSS custom property through the CSS object model, never as CSS text.
- The accent is drawn as text on the background and on surfaces, so it must
  reach **4.5:1** contrast against both in the current theme. Text on accent
  backgrounds is black or white, whichever is more readable (one of them
  always reaches at least 4.58:1).
- The accent is saved once for all themes. If you switch to a theme where it
  is too hard to read, that theme's own accent is shown instead, and Settings
  says so. Switching back shows your color again.
- Background and text colors cannot be customized. Choosing them freely could
  produce unreadable combinations across the many text and surface pairs, so
  the themes cover that instead.

## Design tokens

All colors come from eight custom properties defined in `web/src/app.css`.
Components never use literal colors (a test fails if a component's `<style>`
contains a hex, `rgb()` or `hsl()` color).

| Token | Used for |
| --- | --- |
| `--rw-color-bg` | Page, player bar and input backgrounds |
| `--rw-color-surface` | Hovered and selected rows, panels, the add-to-playlist panel |
| `--rw-color-text` | Primary text |
| `--rw-color-muted` | Secondary text: artists, counts, hints, positions |
| `--rw-color-border` | Structural borders |
| `--rw-color-accent` | Buttons, links, focus ring, current-page and current-track bars, "Playing" markers; also text |
| `--rw-color-on-accent` | Text on accent backgrounds |
| `--rw-color-danger` | Errors, field errors, "Unavailable" badges, the delete confirmation |

Derived tokens follow automatically: `--rw-border`, `--rw-shadow` and
`--rw-focus-ring` refer to the color tokens. Spacing, type and border-width
tokens in `app.css` are not part of themes.

Themes are applied by setting these properties on `<html>`, together with
`color-scheme` (so native controls and scrollbars match) and the
`theme-color` meta tag (the mobile browser bar).

There is no separate "success" color: success is shown in text and with the
accent. Selected and current states always also have text or a shape (a
"Current" label, a filled radio button, "Playing", a bar), never color alone.

## Adding a built-in theme

1. Add an entry to `THEMES` in `web/src/services/themes.ts` with a new `id`,
   a `name`, `scheme` (`dark` or `light`) and all eight colors as lower-case
   `#rrggbb`.
2. Add the same `id` to `Themes` in `internal/prefs/prefs.go`, in the same
   order. `TestThemesMatchFrontend` fails if the two lists differ.
3. Run `npm test` in `web/` and `go test ./internal/prefs`. The theme tests
   check that every token is set and that these pairs reach the minimum
   contrast:

   | Pair | Minimum |
   | --- | --- |
   | text on bg, text on surface | 4.5:1 |
   | muted on bg, muted on surface | 4.5:1 |
   | accent on bg, accent on surface (links, focus ring) | 4.5:1 |
   | on-accent on accent (buttons) | 4.5:1 |
   | danger on bg (field errors) | 4.5:1 |
   | danger on surface, border on bg | 3:1 |

4. Look at every screen in the new theme: sign-in, Library (with a search, a
   selected track and an unavailable track), Playlists and a playlist,
   Settings, Status, and the player while playing. Check focus outlines with
   the keyboard and the layout at phone width.
5. Add the theme to the table above.

Keep the set small. Add a theme only when it is clearly different in use, not
only in hue.

## Limitations

- The offline page (`offline.html`), the web-app manifest and the splash
  screen of an installed app are always Rewind Dark: they are static files
  shown without a signed-in user.
- The sign-in screen uses the theme this browser last showed, or Rewind Dark
  in a new browser.
- Theme switching has no animation, so `prefers-reduced-motion` needs no
  special handling; the base styles already shorten any transitions for users
  who ask for reduced motion.
