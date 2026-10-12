// Playlist playback order. The player plays one track at a time; while a
// playlist is playing, the shell keeps its tracks and the current index and
// uses these helpers to step through them, skipping unavailable tracks.

import type { Track } from './api'

/** Previous/next controls the player shows while a playlist is playing. */
export interface Sequence {
  /** Where the track is, e.g. "Road trip · 3 of 12". */
  label: string
  hasPrevious: boolean
  hasNext: boolean
  onPrevious: () => void
  onNext: () => void
}

/**
 * Returns the index of the nearest playable track after (step 1) or before
 * (step -1) index from, or -1 if there is none. Playback never wraps around.
 */
export function nextPlayable(tracks: readonly Track[], from: number, step: 1 | -1): number {
  for (let i = from + step; i >= 0 && i < tracks.length; i += step) {
    if (tracks[i].available) return i
  }
  return -1
}
