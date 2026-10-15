// App-wide overlays, opened from any view or by keyboard shortcuts. Shell
// renders each overlay once.

class UI {
  queueOpen = $state(false)
  nowPlayingOpen = $state(false)
  shortcutsOpen = $state(false)
  /** The track whose metadata editor is open. */
  editTrackId = $state<number | null>(null)
  /** Element to focus again when an overlay closes. */
  private returnFocus: HTMLElement | null = null

  /** Whether a modal dialog is open (global shortcuts then only handle Escape). */
  get modalOpen(): boolean {
    return this.shortcutsOpen || this.editTrackId !== null
  }

  private remember() {
    const el = document.activeElement
    this.returnFocus = el instanceof HTMLElement && el !== document.body ? el : null
  }

  restoreFocus() {
    const el = this.returnFocus
    this.returnFocus = null
    if (el?.isConnected) el.focus()
  }

  openQueue() {
    this.remember()
    this.queueOpen = true
  }

  toggleQueue() {
    if (this.queueOpen) this.closeQueue()
    else this.openQueue()
  }

  closeQueue() {
    this.queueOpen = false
    this.restoreFocus()
  }

  openShortcuts() {
    this.remember()
    this.shortcutsOpen = true
  }

  closeShortcuts() {
    this.shortcutsOpen = false
    this.restoreFocus()
  }

  editTrack(id: number) {
    this.remember()
    this.editTrackId = id
  }

  closeEditor() {
    this.editTrackId = null
    this.restoreFocus()
  }

  /** Closes the topmost overlay; returns false if none was open. */
  closeTop(): boolean {
    if (this.editTrackId !== null) this.closeEditor()
    else if (this.shortcutsOpen) this.closeShortcuts()
    else if (this.queueOpen) this.closeQueue()
    else if (this.nowPlayingOpen) this.nowPlayingOpen = false
    else return false
    return true
  }
}

export const ui = new UI()

/** Navigates with the hash router (history and back/forward keep working). */
export function navigate(hash: string) {
  if (location.hash !== hash) location.hash = hash
}

export const albumHref = (id: string) => `#/albums/${id}`
export const artistHref = (id: string) => `#/artists/${id}`
