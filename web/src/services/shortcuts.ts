// Global keyboard shortcuts, registered once (Shell). They call the same
// player and UI actions as the visible controls. They never fire while
// typing in a text field, never override browser shortcuts (Ctrl, Alt or
// Meta combinations), and only Escape works while a modal dialog is open.

import { player, SEEK_STEP_S } from './player.svelte'
import { listening } from './listening.svelte'
import { ui } from './ui.svelte'

export interface Shortcut {
  keys: string
  action: string
}

/** The reference shown in the shortcuts dialog. */
export const SHORTCUTS: Shortcut[] = [
  { keys: 'Space', action: 'Play or pause' },
  { keys: '← / →', action: 'Seek back or forward 10 seconds' },
  { keys: 'Shift+← / Shift+→', action: 'Previous or next track' },
  { keys: '- / +', action: 'Volume down or up' },
  { keys: 'M', action: 'Mute or unmute' },
  { keys: 'S', action: 'Shuffle on or off' },
  { keys: 'R', action: 'Repeat: off, all, one' },
  { keys: 'F', action: 'Favorite the playing track' },
  { keys: 'Q', action: 'Show or hide the queue' },
  { keys: 'N', action: 'Open Now Playing' },
  { keys: '/', action: 'Search the library' },
  { keys: '?', action: 'Show these shortcuts' },
  { keys: 'Esc', action: 'Close menus, dialogs and panels' },
]

/** True when focus is in a control that takes typed text or keys. */
export function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  if (target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return true
  if (target instanceof HTMLInputElement) {
    // Buttons and checkboxes do not take text; ranges use the arrow keys.
    return !['button', 'checkbox', 'radio', 'submit', 'reset', 'color'].includes(target.type)
  }
  return false
}

/**
 * Handles one keydown. Returns true when it was a shortcut. Space and Enter
 * on a focused button keep their normal meaning (activating the button).
 */
export function handleShortcut(event: KeyboardEvent, goSearch: () => void): boolean {
  if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey) return false
  if (event.key === 'Escape') return ui.closeTop()
  if (isTyping(event.target) || ui.modalOpen) return false
  const onButton = event.target instanceof HTMLElement && ['BUTTON', 'A', 'SUMMARY'].includes(event.target.tagName)
  const t = player.current
  switch (event.key) {
    case ' ':
      if (onButton || !t) return false
      player.toggle()
      return true
    case 'ArrowLeft':
    case 'ArrowRight': {
      if (!t) return false
      const forward = event.key === 'ArrowRight'
      if (event.shiftKey) {
        if (forward) player.next()
        else player.previous()
      } else player.seekBy(forward ? SEEK_STEP_S : -SEEK_STEP_S)
      return true
    }
    case '-':
    case '_':
      player.setVolume(player.volume - 0.1)
      return true
    case '+':
    case '=':
      player.setVolume(player.volume + 0.1)
      return true
    case 'm':
    case 'M':
      player.toggleMute()
      return true
    case 's':
    case 'S':
      player.toggleShuffle()
      return true
    case 'r':
    case 'R':
      player.cycleRepeat()
      return true
    case 'f':
    case 'F':
      if (!t) return false
      void listening.toggleFavorite(t).catch(() => listening.announce('Could not change the favorite.'))
      return true
    case 'q':
    case 'Q':
      ui.toggleQueue()
      return true
    case 'n':
    case 'N':
      if (!t) return false
      ui.nowPlayingOpen = true
      return true
    case '/':
      goSearch()
      return true
    case '?':
      ui.openShortcuts()
      return true
  }
  return false
}
