// Pure queue rules used by the player (services/player.svelte.ts). Every
// function returns new arrays and never mutates its input, so the rules are
// easy to test and the player can replace its state in one assignment.
//
// The queue is a list of entries in play order. An entry wraps a track with a
// key that is unique within the queue, so the same track can be queued twice
// and an entry keeps its identity while it moves. While shuffle is on, the
// order before shuffling ("original") is kept, so turning shuffle off
// restores it.
//
// Rules:
// - Next skips unavailable tracks. At the end, repeat all wraps around;
//   otherwise there is no next track. Repeat one replays the current track
//   only when a track ends on its own; Next always moves on.
// - Previous restarts the current track when more than 3 seconds have been
//   played; otherwise it goes to the previous available track (wrapping
//   with repeat all), or restarts the current one at the start.
// - Removing the current entry makes the following entry current (or the
//   previous one when it was last); the player then loads it.
// - Tracks added while shuffle is on keep the position they were added at in
//   the play order, and are added after the current track (play next) or at
//   the end (add to queue) of the original order.
// - Moving entries never changes which entry is current.

import type { Track } from './api'

export interface Entry {
  key: number
  track: Track
}

let nextKey = 1

/** Wraps tracks in entries with fresh keys. */
export function entriesOf(tracks: readonly Track[]): Entry[] {
  return tracks.map((track) => ({ key: nextKey++, track }))
}

export type Repeat = 'off' | 'all' | 'one'

/** The next playable index after from, or -1. */
export function nextIndex(entries: readonly Entry[], from: number, repeat: Repeat): number {
  const n = entries.length
  for (let i = from + 1; i < n; i++) if (entries[i].track.available) return i
  if (repeat === 'all') {
    for (let i = 0; i <= Math.min(from, n - 1); i++) if (entries[i].track.available) return i
  }
  return -1
}

/** The previous playable index before from, or -1. */
export function previousIndex(entries: readonly Entry[], from: number, repeat: Repeat): number {
  for (let i = from - 1; i >= 0; i--) if (entries[i].track.available) return i
  if (repeat === 'all') {
    for (let i = entries.length - 1; i >= from && i >= 0; i--) if (i !== from && entries[i].track.available) return i
  }
  return -1
}

/** Fisher-Yates shuffle with an injectable random source (for tests). */
export function shuffled<T>(items: readonly T[], random: () => number = Math.random): T[] {
  const out = [...items]
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1))
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out
}

/**
 * Turns shuffle on: entries played so far and the current one stay in
 * place, the upcoming ones are shuffled. Returns the new play order and the
 * original order to restore later.
 */
export function shuffleUpcoming(entries: readonly Entry[], index: number, random?: () => number): { entries: Entry[]; original: Entry[] } {
  const keep = entries.slice(0, index + 1)
  return { entries: [...keep, ...shuffled(entries.slice(index + 1), random)], original: [...entries] }
}

/**
 * Starts a queue in shuffled order: the chosen entry (if any) first, the
 * rest shuffled.
 */
export function shuffleAll(entries: readonly Entry[], start: number, random?: () => number): Entry[] {
  if (start >= 0 && start < entries.length) {
    const first = entries[start]
    return [first, ...shuffled(entries.filter((_, i) => i !== start), random)]
  }
  return shuffled(entries, random)
}

/** Turns shuffle off: the original order, and the current entry's index in it. */
export function unshuffle(original: readonly Entry[], current: Entry | undefined): { entries: Entry[]; index: number } {
  const index = current ? original.findIndex((e) => e.key === current.key) : -1
  return { entries: [...original], index: index >= 0 ? index : original.length > 0 ? 0 : -1 }
}

/** Inserts added entries right after the current one. */
export function insertNext(entries: readonly Entry[], index: number, added: readonly Entry[]): Entry[] {
  const at = Math.min(Math.max(index + 1, 0), entries.length)
  return [...entries.slice(0, at), ...added, ...entries.slice(at)]
}

/** Inserts added into the original order after the current entry. */
export function insertNextOriginal(original: readonly Entry[], current: Entry | undefined, added: readonly Entry[]): Entry[] {
  const at = current ? original.findIndex((e) => e.key === current.key) : -1
  return insertNext(original, at, added)
}

/**
 * Moves the entry at from to position to; returns the new order and the
 * current entry's new index.
 */
export function move(entries: readonly Entry[], index: number, from: number, to: number): { entries: Entry[]; index: number } {
  if (from < 0 || from >= entries.length || to < 0 || to >= entries.length || from === to) return { entries: [...entries], index }
  const current = entries[index]
  const out = [...entries]
  const [moved] = out.splice(from, 1)
  out.splice(to, 0, moved)
  return { entries: out, index: current ? out.indexOf(current) : index }
}

/**
 * Removes the entry at at. currentRemoved says the player must load the
 * entry now at index (the one that followed, or the new last one).
 */
export function remove(entries: readonly Entry[], index: number, at: number): { entries: Entry[]; index: number; currentRemoved: boolean } {
  if (at < 0 || at >= entries.length) return { entries: [...entries], index, currentRemoved: false }
  const out = entries.filter((_, i) => i !== at)
  if (at < index) return { entries: out, index: index - 1, currentRemoved: false }
  if (at > index) return { entries: out, index, currentRemoved: false }
  return { entries: out, index: out.length === 0 ? -1 : Math.min(index, out.length - 1), currentRemoved: true }
}

/** Removes entries from an original order by key. */
export function withoutKeys(original: readonly Entry[], keys: ReadonlySet<number>): Entry[] {
  return original.filter((e) => !keys.has(e.key))
}
