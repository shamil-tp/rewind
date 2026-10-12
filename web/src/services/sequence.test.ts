import { describe, expect, it } from 'vitest'
import type { Track } from './api'
import { nextPlayable } from './sequence'

const t = (id: number, available = true) => ({ id, available }) as Track

describe('nextPlayable', () => {
  const tracks = [t(1, false), t(2), t(3, false), t(4, false), t(5), t(6, false)]

  it('finds the first playable track from the start', () => {
    expect(nextPlayable(tracks, -1, 1)).toBe(1)
  })

  it('skips unavailable tracks in both directions', () => {
    expect(nextPlayable(tracks, 1, 1)).toBe(4)
    expect(nextPlayable(tracks, 4, -1)).toBe(1)
  })

  it('does not wrap around', () => {
    expect(nextPlayable(tracks, 4, 1)).toBe(-1)
    expect(nextPlayable(tracks, 1, -1)).toBe(-1)
    expect(nextPlayable([], -1, 1)).toBe(-1)
  })
})
