// State shared by every view that shows tracks, so they stay consistent
// without reloading each other:
// - favorites changed in this session (a list loaded earlier shows the new
//   state at once);
// - version counters that views watch to reload after metadata edits
//   (libraryVersion) or a newly recorded play (historyVersion);
// - short global announcements (one live region in Shell).

import { setFavorite, type Track } from './api'

class Listening {
  favorites = $state<Record<number, boolean>>({})
  libraryVersion = $state(0)
  historyVersion = $state(0)
  announcement = $state('')

  isFavorite(track: Pick<Track, 'id' | 'favorite'>): boolean {
    return this.favorites[track.id] ?? !!track.favorite
  }

  /** Flips a favorite at once and saves it; reverts and rethrows on failure. */
  async toggleFavorite(track: Pick<Track, 'id' | 'favorite' | 'title'>): Promise<boolean> {
    const next = !this.isFavorite(track)
    this.favorites[track.id] = next
    try {
      await setFavorite(track.id, next)
      this.announce(next ? `Added “${track.title}” to favorites.` : `Removed “${track.title}” from favorites.`)
      return next
    } catch (err) {
      this.favorites[track.id] = !next
      throw err
    }
  }

  /** Tells views that track metadata changed (after edits). */
  libraryChanged() {
    this.libraryVersion++
  }

  historyChanged() {
    this.historyVersion++
  }

  announce(message: string) {
    // Re-setting the same text would not be announced again.
    this.announcement = ''
    queueMicrotask(() => (this.announcement = message))
  }
}

export const listening = new Listening()
