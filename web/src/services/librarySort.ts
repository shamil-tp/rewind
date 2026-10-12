import type { LibrarySortChoice, TrackSort } from './api'

/**
 * The library's sort menu. Each choice maps to an API sort key and
 * direction; the values are also the server's library_sort preference values.
 */
export const LIBRARY_SORTS: { value: LibrarySortChoice; label: string; sort: TrackSort; order: 'asc' | 'desc' }[] = [
  { value: 'artist', label: 'Artist', sort: 'artist', order: 'asc' },
  { value: 'album', label: 'Album', sort: 'album', order: 'asc' },
  { value: 'title', label: 'Title', sort: 'title', order: 'asc' },
  { value: 'added', label: 'Recently added', sort: 'added', order: 'desc' },
  { value: 'year', label: 'Year', sort: 'year', order: 'asc' },
  { value: 'duration', label: 'Longest', sort: 'duration', order: 'desc' },
]
