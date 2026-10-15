# Themes

A Rewind theme is a **declarative set of design-token values**: colors,
typography, borders and corner radii, shadows and spacing, plus a name and
version. Users choose a theme, customize what the theme allows, preview
changes live, and save them. Each theme keeps its own customization.

Themes are **data, not code**. A theme package is one JSON file. It cannot
contain CSS, HTML, JavaScript, fonts, images or URLs, and installing or
applying one never runs anything. The web UI turns validated token values into
CSS custom properties with its own code. This is what keeps themes safe.

Themes are separate from [plugins](plugins.md). Plugins are compiled-in,
trusted Go code. Themes are untrusted data that is validated before use, and
they get no capabilities. Enabling a theme gives it no plugin privileges, and
the plugin system cannot add themes.

## Bundled themes

| ID | Name | Use |
| --- | --- | --- |
| `rewind-dark` | Rewind Dark | The default: dark surfaces, strong 2px borders, rounded panels and the acid-lime accent. Rewind's fallback theme; it cannot be disabled. |
| `light` | Rewind Light | Near-white paper background, black text and borders, deep violet accent. For bright rooms and daylight. |
| `oled-black` | Rewind OLED | Pure black background and near-black surfaces, so OLED screens light fewer pixels; dimmer borders and a smaller shadow. Its background cannot be customized, so it stays black. |

The bundled themes are the manifests in `internal/theme/bundled/`, embedded in
the binary and validated at startup like any other theme. The IDs `light` and
`oled-black` are kept from earlier versions so saved choices still apply.

## Using themes

**Settings → Theme and style** lists every theme as a card with:

- a generated preview drawn with the theme's own tokens;
- its name, description, author, version and whether it is bundled or
  installed;
- its status: Active, Disabled, Customized, Previewing;
- its compatibility. Invalid and incompatible themes show Rewind's reason
  and have no actions.

Actions (a button appears only when the action applies):

| Action | What it does | Saved? |
| --- | --- | --- |
| **Apply** | Makes the theme active. | Yes, on the server. Reported only after the server confirms. |
| **Preview** | Shows the theme across the whole app until you apply it or press **Stop preview**. | No. |
| **Customize** | Opens the editor (below). | Only when you press Save. |
| **Disable** / **Enable** | Hides a theme from use. Disabling the active theme returns to the theme you applied before it (if it is still available and enabled), otherwise to Rewind Dark. Rewind Dark cannot be disabled. | Yes. |

Switching themes only changes CSS custom properties, so it never reloads the
page and never interrupts playback.

### The editor

**Customize** opens an editor for one theme. It has a control for every token
the theme lets users change, grouped as Colors, Typography, Borders and
shape, and Surfaces and spacing:

- **Colors:** a color picker and a hex field that stay in sync, with
  validation. Only `#rrggbb` is accepted; upper case is lower-cased.
- **Lengths and numbers:** a slider showing the value (keyboard operable).
- **Presets** (font, font weight, border style, shadow, density): a menu.
- Each control has a **Reset** button that returns it to the theme's value,
  and a label that says where the value comes from: Theme default, Custom,
  Not saved yet, Earlier custom color, Not readable here, or Invalid.

**Live preview.** Every change shows at once in two places. The first is the
whole app, as long as Settings is open: the navigation, the Settings panels,
inputs and buttons, and the mini player if something is playing. The second
is a preview panel with the parts Settings does not show: navigation, a track
list with playing, selected and unavailable rows, a dialog, notices, the
mini player with its progress slider, and the Now Playing screen. The preview
panel uses the app's shared classes (`rw-button`, `rw-input`, `rw-panel`, …)
and the same tokens, so it is drawn by the same CSS. Leaving Settings shows
the saved theme on other pages, and coming back shows the edits again.

**Buttons:**

| Button | Behavior |
| --- | --- |
| **Save** (active theme, or a disabled one) | Saves the customization. For the active theme the app keeps showing it. |
| **Save and apply** (inactive, enabled theme) | Saves the customization, then makes the theme active. Without changes, it only applies. |
| **Save only** (inactive, enabled theme) | Saves the customization without applying the theme. |
| **Reset to defaults** | Sets every control to the theme's value. Press Save to keep that; saving stores an empty customization. |
| **Cancel** / **Close** | Closes the editor. With unsaved changes it asks first ("Discard unsaved changes?"), and the saved settings are shown again. |

"Unsaved changes." is shown while edits differ from the saved values.
Opening another theme's editor, or leaving the page, with unsaved changes asks
first. If saving fails, the editor says "Not saved" with the reason and keeps
your edits. If the customization was saved but applying failed, it says so.

**Readability.** Text must stay readable: **4.5:1** contrast (WCAG AA) for
text, secondary text, links (primary), the focus ring (accent) and error
text, on both the background and surfaces. Edits that break this are listed
with their measured ratios and cannot be saved, and the preview leaves them
out. Borders and outlines below **3:1** only get a note: they make an outline
harder to see, not text unreadable. Text on primary, secondary and accent
fills is chosen automatically, black or white, whichever is readable. The
error color switches to a readable red when you customize the background.

## How settings are stored

| What | Where |
| --- | --- |
| Theme definitions | The binary (bundled) and the theme directory (installed). Never in the database. |
| Active theme | The existing `theme` preference in `user_preferences`, so `GET /api/preferences` still reports it. |
| Per-theme customization, enabled flag, last applied time | The `theme_settings` table (migration `0005_themes`), one row per user and theme. A theme without a row is enabled and not customized. |
| Startup hint | `localStorage` key `rewind.theme`: the theme ID and the token values last shown. See below. |

- Everything is per user. Rewind has one owner account, and the tables are
  already keyed by user.
- Saving validates every value on the server: known token, allowed by the
  theme, right type and range. Nothing is saved if any value is invalid.
  Values equal to the theme's own are dropped.
- Settings of a theme that is later removed are **kept**, and come back if the
  theme is installed again. Theme operations never touch playlists, tracks,
  music files, the account or other preferences.
- **Startup:** theme settings can only be read after sign-in. So the browser
  remembers the values it last showed (`rewind.theme`: version, theme ID and
  token values, nothing about the account) and applies them before the first
  render, to avoid a flash of the default palette. They are validated like
  server data, and the server's state replaces them as soon as it loads.
  Previews are never remembered. Hints written by earlier versions are
  ignored once, so the first start after upgrading may show Rewind Dark
  until you sign in.

### Custom colors from earlier versions

Earlier versions had three custom colors (`primary_color`, `secondary_color`,
`accent_color`) shared by all themes. They are **kept unchanged** in
`user_preferences`, and still apply to every theme you have not customized.
The server reports them as that theme's "legacy" customization. As before,
an unset primary follows a custom accent, so your colors look exactly the
same. The editor marks them "Earlier custom color". Saving a theme (or
resetting it) stores a customization for that theme only, and from then on
the earlier colors no longer apply to it. Nothing is migrated or deleted, so
going back to an older version still works.

## Recovery

| Situation | Result |
| --- | --- |
| The active theme is removed, invalid or incompatible after an upgrade | Rewind Dark is shown, and Settings explains why. Its saved settings are kept. |
| The active theme is disabled | The previous theme (or Rewind Dark) becomes active in the same transaction. |
| A saved customization has values that are now invalid or not allowed | Those values are ignored; the rest apply. |
| Saved colors are unreadable with the theme | They are ignored and listed in the editor; nothing is changed or deleted. |
| Themes cannot be loaded (network or server error) | The remembered theme stays; Settings shows the error with Try again. Other settings work. |
| Saving fails | The editor says the change was not saved and keeps the edits; the app shows the saved theme. |
| The theme directory is missing or unreadable, or a package is invalid | Logged at startup; bundled themes still work. |

## Installing a theme (administrator)

There is **no upload, download or install button**, and no API endpoint to
install, update or remove themes. That matches the [plugin system](plugins.md):
changing what the server offers is an administrator task on the server
itself. Nothing is ever fetched from the internet.

1. Choose a theme directory **outside** the data directory, the music folders
   and the plugin directory, writable only by the administrator, for example
   `/etc/rewind/themes`. Rewind refuses to start if it overlaps any of them.
2. Put each theme in its own directory named after its ID:

   ```sh
   sudo install -d -m 0755 /etc/rewind/themes/sunset
   sudo install -m 0644 theme.json /etc/rewind/themes/sunset/theme.json
   ```

3. Set `REWIND_THEME_DIR=/etc/rewind/themes` in the server's configuration
   (`/etc/rewind/rewind.env` for the systemd installation) and restart Rewind.
   The hardened systemd unit can read `/etc`; no unit change is needed.
4. Check the startup log (`theme installed` or `theme not available` with a
   reason) or **Settings → Theme and style**.

**Updating:** replace `theme.json` and restart. Users' customizations are
kept. Values the new version no longer allows are ignored.
**Removing:** delete the theme's directory and restart. Users who had it
active see Rewind Dark, and their settings stay in the database.

Themes are read **once at startup**. There is no hot reload and no
directory polling.

### Directory rules

Discovery uses the same protections as plugin manifests:

- Files are opened through Go's `os.Root`, so no path can leave the theme
  directory.
- Symbolic links and Windows junctions, as a theme directory or as a
  manifest, are rejected and never followed.
- `theme.json` must be a regular file of at most 32 KiB. At most 64 entries
  are examined, and hidden entries and plain files are ignored.
- **Only `theme.json` is read.** Any other file in a theme's directory
  (scripts, images, stylesheets) is ignored: there are no theme assets.
- The directory name must equal the manifest's `id`. An ID that a bundled
  theme already uses is rejected.

## Manifest format

`theme.json`: UTF-8 JSON, one object, at most 32 KiB. Unknown fields,
duplicate keys and trailing data are rejected.

```json
{
  "schema": 1,
  "id": "sunset",
  "name": "Sunset",
  "version": "1.0.0",
  "description": "A warm dark theme.",
  "author": "Your name",
  "homepage": "https://example.org/sunset",
  "theme_api": 1,
  "scheme": "dark",
  "tokens": {
    "color.background": "#1a1010",
    "color.surface": "#241818",
    "color.text": "#fbeee6",
    "color.text-muted": "#c9b3a8",
    "color.border": "#fbeee6",
    "color.primary": "#ffb347",
    "color.secondary": "#3a2626",
    "color.accent": "#ff7f50",
    "color.danger": "#ff6b6b",
    "radius.button": 4,
    "font.family": "serif"
  },
  "customizable": ["color.accent", "color.primary", "font.size", "radius.button"]
}
```

| Field | Required | Rules |
| --- | --- | --- |
| `schema` | yes | Manifest format version. Must be `1`. |
| `id` | yes | Lower-case letters, digits and single hyphens, starting with a letter, at most 40 characters. Stable: users' settings are keyed by it. |
| `name` | yes | 1–64 characters, plain text, no control characters, no leading or trailing spaces. |
| `version` | yes | The theme's own version, `MAJOR.MINOR.PATCH`. |
| `description` | no | At most 280 characters, plain text. |
| `author` | no | At most 100 characters, plain text. |
| `homepage` | no | An `https://` URL, at most 200 characters, without credentials. Shown as text only; Rewind never loads it. |
| `theme_api` | yes | The token contract the theme was written for. Must equal this Rewind's (currently `1`); otherwise the theme is listed as **incompatible** and not used. |
| `scheme` | yes | `dark` or `light`: the theme's category, and how browsers draw native controls. Must match the background (dark when white text is more readable on it). |
| `tokens` | yes | Token values (below). The required tokens must be present; others default to Rewind Dark's. |
| `customizable` | no | The tokens users may change. Omitted: every customizable token. `[]`: none. Only customizable tokens may be listed. |

Name, description, author and homepage are untrusted. The UI renders them as
plain text, never as HTML.

Beyond the per-field rules, every theme must pass the **contrast checks**:
every pair in the readability rule above, plus borders, error badges and
secondary-button outlines at 3:1. An unreadable theme is rejected with the
failing pair and its ratio.

**Compatibility** is checked against the theme API version, not Rewind's
release version, because the token contract is what a theme depends on. The
theme API changes only when a token is removed or its meaning or range
changes incompatibly; the release notes will say so. Adding an optional
token does not change it, so existing themes keep working and get the
default for the new token.

## Token contract (theme API 1)

The contract is defined in `internal/theme/tokens.go` and mirrored for the web
UI in `web/src/services/theme-tokens.json`. A test fails if the two differ;
regenerate the mirror with
`go test ./internal/theme -run TestTokensMatchFrontend -update`.

### Colors

All colors are lower-case `#rrggbb`.

| Token | Required | Customizable | CSS property | Used for |
| --- | --- | --- | --- | --- |
| `color.background` | yes | yes | `--rw-color-bg` | Page and input backgrounds, the Now Playing screen |
| `color.surface` | yes | yes | `--rw-color-surface` | Panels, the mini player, hovered and selected rows |
| `color.text` | yes | yes | `--rw-color-text` | Primary text |
| `color.text-muted` | yes | yes | `--rw-color-muted` | Secondary text: artists, counts, hints |
| `color.border` | yes | yes | `--rw-color-border` | Structural borders |
| `color.primary` | yes | yes | `--rw-color-primary` | Main buttons, links, the current page in the menu |
| `color.secondary` | yes | yes | `--rw-color-secondary` | Secondary buttons, the artwork placeholder |
| `color.accent` | yes | yes | `--rw-color-accent` | Progress, playing markers, focus ring, offset shadows |
| `color.danger` | yes | no | `--rw-color-danger` | Errors and "Unavailable" badges |
| `color.on-primary` | no | no | `--rw-color-on-primary` | Text on primary fills (derived when omitted) |
| `color.on-secondary` | no | no | `--rw-color-on-secondary` | Text on secondary fills (derived when omitted) |
| `color.on-accent` | no | no | `--rw-color-on-accent` | Text on accent fills (derived when omitted) |

The eight customizable colors are the roles the editor offers. Text on fills
and the error color are derived or theme-only, so a customization can never
make them unreadable.

### Typography

| Token | Values | Default | Effect |
| --- | --- | --- | --- |
| `font.family` | `system`, `humanist`, `geometric`, `serif`, `mono` | `system` | Body typeface (`--rw-font-sans`). Each is a stack of fonts commonly installed on devices; **nothing is downloaded**, and arbitrary font names or URLs are not accepted. Labels keep the monospace stack. |
| `font.size` | 14–20 px | 16 | Body text size at the browser's default text size. Written in `rem`, so it follows the user's browser setting. Small and large text scale with it. |
| `font.heading-scale` | 0.8–1.3, steps of 0.05 | 1 | Size of page titles, the Now Playing title and large text. |
| `font.weight` | `regular`, `strong`, `heavy` | `heavy` | Weight of headings, titles and labels (600/700, 700/800, 800/900). |
| `font.line-height` | 1.3–1.8, steps of 0.05 | 1.5 | Line height of body text. |

### Borders and shape

| Token | Values | Default | CSS property |
| --- | --- | --- | --- |
| `border.width` | 1–4 px | 2 | `--rw-border-width` (and `--rw-border`) |
| `border.style` | `solid`, `dashed` | `solid` | `--rw-border-style` (structural borders) |
| `radius.small` | 0–12 px | 8 | `--rw-radius-sm`: list rows, small controls. Badges use the smaller of this and 4 px. |
| `radius.medium` | 0–16 px | 10 | `--rw-radius-md`: notices, menu items |
| `radius.large` | 0–24 px | 14 | `--rw-radius-lg`: **cards** and panels |
| `radius.xlarge` | 0–32 px | 20 | `--rw-radius-xl`: the mini player |
| `radius.button` | 0–16 px | 10 | `--rw-radius-button`: buttons |
| `radius.input` | 0–14 px | 8 | `--rw-radius-input`: text fields and menus |
| `radius.artwork` | 0–32 px | 18 | `--rw-radius-art`: album artwork |

The limits keep Rewind's rounded neo-brutalist character. Buttons and inputs
can never become pills: their height is 2.75 rem and their radius is capped
well below half of it. The only round elements are the main play button and
status dots (`--rw-radius-round`), which themes do not change.

### Surfaces and spacing

| Token | Values | Default | Effect |
| --- | --- | --- | --- |
| `shadow.size` | `none`, `small`, `medium`, `large` | `medium` | Hard offset shadow under panels, the player and artwork: 0, 2, 4 or 6 px (`--rw-shadow`). |
| `spacing.density` | `compact`, `comfortable`, `spacious` | `comfortable` | Scales the spacing scale `--rw-space-1` to `--rw-space-6` by 0.8, 1 or 1.2. Touch targets keep their 2.75 rem minimum. |
| `spacing.panel` | 12–40 px, steps of 4 | 24 | Padding inside panels (`--rw-space-panel`). |

Surface contrast has no separate control: the surface color covers it, and
the contrast checks keep it readable.

### How tokens become CSS

`cssVariables()` in `web/src/services/themes.ts` builds every property value
from validated numbers, fixed preset names and `#rrggbb` colors. No text from
a theme or a user ever becomes CSS. `applyTheme()` sets all of them on
`<html>` through `style.setProperty` (the CSS object model, allowed by the
Content-Security-Policy, which blocks inline styles and stylesheets from
elsewhere), together with `color-scheme` and the `theme-color` meta tag. Each
switch sets every property, so nothing from a previous theme remains.
Previews set the same properties on one element.

`app.css` defines every themed property with Rewind Dark's value, and
components use only these properties. Tests fail if a component's styles
contain a literal color, or if `app.css` and the contract's defaults differ.

## Security

- Themes are untrusted until validated. The manifest parser rejects unknown
  fields and tokens, duplicate keys, wrong types, out-of-range values,
  control characters and unreadable color combinations.
- No field is executed, used as a file path, fetched as a URL or inserted as
  HTML or CSS text. Values reach the page only as generated custom-property
  values.
- No theme API can install, upload or remove themes, read the theme
  directory, or return a file path. All theme routes require a session and
  the existing cross-origin protection.
- A theme cannot affect authentication, CSRF protection, security headers,
  configuration, the database or music files. It only supplies token values.
- The theme directory may not overlap the data directory, the music folders
  or the plugin directory.

## Developing and testing a theme

1. Copy a bundled manifest from `internal/theme/bundled/` as a starting
   point, and change `id`, `name` and the tokens.
2. Install it in a test theme directory and start Rewind with
   `REWIND_THEME_DIR` pointing there. If the theme is rejected, the log and
   the Settings card say why. The usual reasons are a contrast pair below its
   minimum (the message names the tokens and the ratio) or a `scheme` that
   does not match the background.
3. Look at every screen in the theme: sign-in (after one visit), Library
   (with a search, a selected track and an unavailable track), Playlists and
   a playlist, Settings, Status, the mini player while playing, and the Now
   Playing screen. Check focus outlines with the keyboard and the layout at
   phone width.

To **bundle** a theme with Rewind, add its manifest to
`internal/theme/bundled/` (file name `<id>.json`) and its ID to
`prefs.Themes` in `internal/prefs/prefs.go`. Then run
`go test ./internal/theme ./internal/prefs` and `npm test` in `web/`. A
bundled theme that fails validation stops Rewind at startup, so a broken
theme cannot ship. Keep the set small, and add a theme only when it is
clearly different in use, not only in hue.

## Limitations

- Themes are read at startup only. Installing, updating or removing one needs
  a restart.
- No theme assets: no images, fonts or preview pictures. Card previews are
  generated from the tokens.
- No in-app installation, import or download of theme packages; the
  administrator installs them on the server.
- Fonts are limited to the five system font stacks. What each looks like
  depends on the fonts installed on the device.
- Not every size is a token. Some small labels use fixed sizes, and the
  layout (sidebar width, breakpoints) is not themeable.
- The offline page (`offline.html`), the web-app manifest and the splash
  screen of an installed app are always Rewind Dark: they are static files
  shown without a signed-in user.
- The sign-in screen uses the theme this browser last showed, or Rewind Dark
  in a new browser.
- Theme switching has no animation. The only animation in the app is the
  short slide-in of the Now Playing screen, which reduced-motion settings
  turn off.
