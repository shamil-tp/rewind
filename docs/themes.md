# Themes

Rewind has a few built-in color themes, and optional custom primary,
secondary and accent colors. A theme changes **colors only**: layout,
typography, spacing, borders and corner radii stay the same. Every screen
keeps the rounded neo-brutalist structure: strong 2px borders, solid
surfaces, hard offset shadows and consistent rounded corners.

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
  holding only the theme ID and the three custom colors) and applies it
  before the first render, to avoid a flash of the default colors. A hint
  written by an older version, with only an accent, still works. This is a cache: the
  server's value replaces it as soon as it loads, and an invalid or tampered
  value is ignored. It contains nothing about the account or session.
- Rewind does not follow the system's light/dark setting. It has always been
  dark-first, so with no saved theme it shows Rewind Dark, and a saved theme
  is never overridden.

## Custom colors

**Settings → Appearance → Colors** sets three colors on top of the theme:

| Color | Preference | Used for | Default |
| --- | --- | --- | --- |
| **Primary** | `primary_color` | Main buttons (Play, Save, Apply), links, the current page in the menu, the selected theme | The custom accent if one is set, otherwise the theme's accent color |
| **Secondary** | `secondary_color` | Secondary buttons (Sign out, Reset, previous/next), the artwork placeholder, the empty part of the preview's progress bar | The theme's secondary color |
| **Accent** | `accent_color` | Highlights: playback progress, the playing track's marker and bar, focus outlines, notices, offset shadows | The theme's accent color |

- Each color has a color picker and a hex field that stay in sync. A preview
  shows buttons, a link, a "Playing" marker, the mini player and the Now
  Playing screen in the edited colors. It sets the same color tokens on the
  preview that the app uses, so it is drawn by the same CSS. Nothing changes
  in the rest of the app until **Apply colors** is pressed. That saves every
  changed color in one request, and the status line confirms it.
  **Reset to defaults** sets all three back to the theme's (`""`).
- Only six-digit hex colors such as `#ff8800` are accepted. The form
  lower-cases what you type, and the server checks the format again.
  Color names, short hex, `rgb()`, `var()`, `url()` and anything else are
  rejected. Values are only ever set as CSS custom properties through the
  CSS object model, never as CSS text.
- **Primary and accent** are also drawn as text (links, focus outlines) on
  the background and on surfaces. So they must reach **4.5:1** contrast
  against both in the current theme, or they cannot be applied. Text on a
  primary or accent fill is black or white, whichever is more readable;
  one of them always reaches at least 4.58:1.
- **Secondary** is only ever a fill. Text on it is the theme's text color
  when readable, otherwise black or white, so any color can be applied. If
  it is too close to the border color (less than 3:1), Settings notes that
  the outlines of secondary buttons will be hard to see.
- Custom colors are saved once, for all themes. If you switch to a theme
  where a primary or accent color is too hard to read, that theme's own
  color is shown instead, and Settings says so ("Not readable on this
  theme"). Your color is kept, and switching back shows it again.
- Background, surface, text and border colors cannot be customized. Choosing
  them freely could produce unreadable combinations across the many text and
  surface pairs, so the themes cover that instead.

**Compatibility with the single accent color.** Before these three colors
existed, the accent also colored buttons, links and the current page. An
unchosen primary therefore follows a custom accent. A user with only a saved
`accent_color` sees exactly the colors they saw before; nothing is migrated
or reset. Choosing a primary color separates the two. The two new
preferences are ordinary rows in the existing `user_preferences` table. No
database migration was needed, and an older server that does not send them
is treated as "theme default".

## Design tokens

All colors come from twelve custom properties defined in `web/src/app.css`.
Components never use literal colors (a test fails if a component's `<style>`
contains a hex, `rgb()` or `hsl()` color).

| Token | Used for |
| --- | --- |
| `--rw-color-bg` | Page and input backgrounds, the Now Playing screen |
| `--rw-color-surface` | Hovered and selected rows, panels, the mini player, the add-to-playlist panel |
| `--rw-color-text` | Primary text |
| `--rw-color-muted` | Secondary text: artists, counts, hints, positions |
| `--rw-color-border` | Structural borders |
| `--rw-color-primary` | Main buttons, links, current page in the menu, selected theme; also text |
| `--rw-color-on-primary` | Text on primary backgrounds |
| `--rw-color-secondary` | Secondary buttons, the artwork placeholder |
| `--rw-color-on-secondary` | Text and icons on secondary backgrounds |
| `--rw-color-accent` | Playback progress, current-track bars, "Playing" markers, focus ring, notices, shadows; also text |
| `--rw-color-on-accent` | Text on accent backgrounds |
| `--rw-color-danger` | Errors, field errors, "Unavailable" badges, the delete confirmation |

Derived tokens follow automatically: `--rw-border`, `--rw-shadow` and
`--rw-focus-ring` refer to the color tokens. They are resolved where they
are defined (`:root`), so an element that overrides a color token, such as
the settings preview, spells these out itself.

**Shape tokens** are part of the design, not of themes:

| Token | Value | Used for |
| --- | --- | --- |
| `--rw-border-width` | 2px | every structural border |
| `--rw-radius-xs` | 4px | tags and badges ("Playing", "Current") |
| `--rw-radius-sm` | 8px | inputs, list rows, small controls |
| `--rw-radius-md` | 10px | buttons, notices, menu items |
| `--rw-radius-lg` | 14px | cards and panels, the mini player on phones |
| `--rw-radius-xl` | 20px | the mini player |
| `--rw-radius-art` | 18px | album artwork |
| `--rw-radius-round` | 999px | only the main play button and status dots |

Themes are applied by setting these properties on `<html>`, together with
`color-scheme` (so native controls and scrollbars match) and the
`theme-color` meta tag (the mobile browser bar).

There is no separate "success" color: success is shown in text and with the
accent. Selected and current states always also have text or a shape (a
"Current" label, a filled radio button, "Playing", a bar, an arrow), never
color alone.

## Adding a built-in theme

1. Add an entry to `THEMES` in `web/src/services/themes.ts` with a new `id`,
   a `name`, `scheme` (`dark` or `light`) and all twelve colors as lower-case
   `#rrggbb`. Primary is normally the theme's accent color.
2. Add the same `id` to `Themes` in `internal/prefs/prefs.go`, in the same
   order. `TestThemesMatchFrontend` fails if the two lists differ.
3. Run `npm test` in `web/` and `go test ./internal/prefs`. The theme tests
   check that every token is set and that these pairs reach the minimum
   contrast:

   | Pair | Minimum |
   | --- | --- |
   | text on bg, text on surface | 4.5:1 |
   | muted on bg, muted on surface | 4.5:1 |
   | primary on bg, primary on surface (links) | 4.5:1 |
   | on-primary on primary (buttons) | 4.5:1 |
   | on-secondary on secondary (secondary buttons) | 4.5:1 |
   | accent on bg, accent on surface (focus ring, markers) | 4.5:1 |
   | on-accent on accent ("Playing" markers) | 4.5:1 |
   | border on secondary | 3:1 |
   | danger on bg (field errors) | 4.5:1 |
   | danger on surface, border on bg | 3:1 |

4. Look at every screen in the new theme: sign-in, Library (with a search, a
   selected track and an unavailable track), Playlists and a playlist,
   Settings, Status, the mini player while playing, and the Now Playing
   screen. Check focus outlines with
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
- Theme switching has no animation. The only animation in the app is the
  short slide-in of the Now Playing screen, and the base styles turn it off
  for users who ask for reduced motion.
