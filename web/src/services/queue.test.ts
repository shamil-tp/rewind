import { describe, expect, it } from 'vitest'
import type { Track } from './api'
import {
  entriesOf,
  insertNext,
  insertNextOriginal,
  move,
  nextIndex,
  previousIndex,
  remove,
  shuffleAll,
  shuffled,
  shuffleUpcoming,
  unshuffle,
  withoutKeys,
  type Entry,
} from './queue'

function track(id: number, available = true): Track {
  return { id, root_id: 1, title: `T${id}`, format: 'mp3', status: 'present', available, metadata_status: 'ok' }
}

const ids = (es: Entry[]) => es.map((e) => e.track.id)
// A deterministic "random" source.
const seq = (...values: number[]) => {
  let i = 0
  return () => values[i++ % values.length]
}

describe('queue rules', () => {
  it('gives every entry its own key, even for the same track', () => {
    const es = entriesOf([track(1), track(1)])
    expect(es[0].key).not.toBe(es[1].key)
  })

  it('next skips unavailable tracks and wraps only with repeat all', () => {
    const es = entriesOf([track(1), track(2, false), track(3), track(4, false)])
    expect(nextIndex(es, 0, 'off')).toBe(2)
    expect(nextIndex(es, 2, 'off')).toBe(-1)
    expect(nextIndex(es, 2, 'all')).toBe(0)
    expect(nextIndex(es, 2, 'one')).toBe(-1) // repeat one replays only when a track ends
    expect(nextIndex(entriesOf([track(1)]), 0, 'all')).toBe(0) // a one-track queue repeats itself
    expect(nextIndex(entriesOf([track(1, false)]), -1, 'all')).toBe(-1)
  })

  it('previous skips unavailable tracks and wraps only with repeat all', () => {
    const es = entriesOf([track(1, false), track(2), track(3)])
    expect(previousIndex(es, 2, 'off')).toBe(1)
    expect(previousIndex(es, 1, 'off')).toBe(-1)
    expect(previousIndex(es, 1, 'all')).toBe(2)
  })

  it('shuffles upcoming tracks only, keeping played ones and the current one', () => {
    const es = entriesOf([1, 2, 3, 4, 5].map((i) => track(i)))
    const s = shuffleUpcoming(es, 1, seq(0, 0, 0))
    expect(ids(s.entries).slice(0, 2)).toEqual([1, 2])
    expect(ids(s.entries).slice(2).sort()).toEqual([3, 4, 5])
    expect(ids(s.original)).toEqual([1, 2, 3, 4, 5])
    expect(ids(s.entries)).not.toEqual([1, 2, 3, 4, 5])
  })

  it('shuffling a whole list puts the chosen track first', () => {
    const es = entriesOf([1, 2, 3, 4].map((i) => track(i)))
    expect(shuffleAll(es, 2, seq(0.9, 0.1, 0.5))[0].track.id).toBe(3)
    expect(shuffleAll(es, -1, seq(0.3)).length).toBe(4)
    expect(shuffled([1, 2, 3]).sort()).toEqual([1, 2, 3])
  })

  it('turning shuffle off restores the original order and keeps the current entry', () => {
    const es = entriesOf([1, 2, 3, 4].map((i) => track(i)))
    const s = shuffleUpcoming(es, 0, seq(0.99, 0.01))
    const current = s.entries[2]
    const u = unshuffle(s.original, current)
    expect(ids(u.entries)).toEqual([1, 2, 3, 4])
    expect(u.entries[u.index]).toBe(current)
  })

  it('play next inserts after the current entry, also in the original order', () => {
    const es = entriesOf([track(1), track(2), track(3)])
    const added = entriesOf([track(9)])
    expect(ids(insertNext(es, 0, added))).toEqual([1, 9, 2, 3])
    expect(ids(insertNext([], -1, added))).toEqual([9])
    expect(ids(insertNextOriginal(es, es[2], added))).toEqual([1, 2, 3, 9])
  })

  it('moving upcoming entries never changes the current entry', () => {
    const es = entriesOf([1, 2, 3, 4].map((i) => track(i)))
    const m = move(es, 1, 3, 2)
    expect(ids(m.entries)).toEqual([1, 2, 4, 3])
    expect(m.index).toBe(1)
    // Moving the current entry itself: the index follows it.
    const c = move(es, 1, 1, 3)
    expect(ids(c.entries)).toEqual([1, 3, 4, 2])
    expect(c.index).toBe(3)
    expect(move(es, 1, 9, 0).entries).toEqual(es)
  })

  it('removing entries keeps the current one, or moves to the following one', () => {
    const es = entriesOf([1, 2, 3].map((i) => track(i)))
    expect(remove(es, 1, 0)).toMatchObject({ index: 0, currentRemoved: false })
    expect(remove(es, 1, 2)).toMatchObject({ index: 1, currentRemoved: false })
    const cur = remove(es, 1, 1)
    expect(cur.currentRemoved).toBe(true)
    expect(ids(cur.entries)[cur.index]).toBe(3)
    const last = remove(es, 2, 2)
    expect(ids(last.entries)[last.index]).toBe(2)
    expect(remove(entriesOf([track(1)]), 0, 0).index).toBe(-1)
    expect(ids(withoutKeys(es, new Set([es[0].key])))).toEqual([2, 3])
  })
})
