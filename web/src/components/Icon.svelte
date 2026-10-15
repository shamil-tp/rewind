<script lang="ts" module>
  // One icon set for the whole app (24×24, filled, currentColor). Icons are
  // always decorative here: the button or link around them carries the label.
  export const ICONS = {
    play: 'M7 4.5v15L20 12z',
    pause: 'M6 4.5h4.5v15H6zM13.5 4.5H18v15h-4.5z',
    previous: 'M6 5h2v14H6zM20 5v14L9 12z',
    next: 'M16 5h2v14h-2zM4 5v14l11-7z',
    shuffle: 'M16 4h4v4l-1.5-1.5-3.6 3.6-1.4-1.4 3.6-3.6zM4 6.4 5.4 5 19 18.6 17.6 20zM14.5 15.9l1.4-1.4 2.6 2.6L20 15.6V20h-4.4l1.5-1.5z',
    repeat: 'M7 7h10v3l4-4-4-4v3H5v6h2zm10 10H7v-3l-4 4 4 4v-3h12v-6h-2z',
    repeatOne: 'M7 7h10v3l4-4-4-4v3H5v6h2zm10 10H7v-3l-4 4 4 4v-3h12v-6h-2zm-4-2V9h-1.5l-2 1v1.3l1.5-.6V15z',
    heart: 'M12 20.5 3.9 12.7A5 5 0 0 1 12 6.2a5 5 0 0 1 8.1 6.5zm0-2.8 6.6-6.4A3 3 0 0 0 13.4 8L12 9.6 10.6 8a3 3 0 0 0-5.2 3.3z',
    heartFilled: 'M12 20.5 3.9 12.7A5 5 0 0 1 12 6.2a5 5 0 0 1 8.1 6.5z',
    queue: 'M3 5h13v2H3zm0 4h13v2H3zm0 4h9v2H3zm12 0v6l5-3z',
    more: 'M5 10.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zm7 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3zm7 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3z',
    volume: 'M4 9h4l5-4v14l-5-4H4zm12.5 3a4.5 4.5 0 0 0-2.5-4v8a4.5 4.5 0 0 0 2.5-4zM14 3.2v2.1a7 7 0 0 1 0 13.4v2.1a9 9 0 0 0 0-17.6z',
    mute: 'M4 9h4l5-4v14l-5-4H4zm12.3.3 1.4-1.4 2.1 2.1 2.1-2.1 1.4 1.4-2.1 2.1 2.1 2.1-1.4 1.4-2.1-2.1-2.1 2.1-1.4-1.4 2.1-2.1z',
    close: 'M5.6 4.2 12 10.6l6.4-6.4 1.4 1.4-6.4 6.4 6.4 6.4-1.4 1.4-6.4-6.4-6.4 6.4-1.4-1.4 6.4-6.4-6.4-6.4z',
    collapse: 'M5.4 8.6 12 15.2l6.6-6.6L20 10l-8 8-8-8z',
    expand: 'M5.4 15.4 12 8.8l6.6 6.6L20 14l-8-8-8 8z',
    lyrics: 'M4 4h16v12H7l-3 3zm3 4v2h10V8zm0 4v2h7v-2z',
    up: 'M12 5l7 7-1.4 1.4-4.6-4.6V20h-2V8.8l-4.6 4.6L5 12z',
    down: 'M12 19l-7-7 1.4-1.4 4.6 4.6V4h2v11.2l4.6-4.6L19 12z',
    drag: 'M8 5h2v2H8zm6 0h2v2h-2zM8 11h2v2H8zm6 0h2v2h-2zM8 17h2v2H8zm6 0h2v2h-2z',
    trash: 'M9 3h6l1 2h4v2H4V5h4zM6 9h12l-1 12H7z',
    note: 'M9 18.5a2.5 2.5 0 1 1-2-2.45V5.5l11-2v11a2.5 2.5 0 1 1-2-2.45V7.1l-7 1.27z',
    person: 'M12 12a4 4 0 1 1 0-8 4 4 0 0 1 0 8zm-8 8a8 8 0 0 1 16 0z',
    disc: 'M12 3a9 9 0 1 1 0 18 9 9 0 0 1 0-18zm0 6.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5z',
    list: 'M4 5h16v2H4zm0 6h16v2H4zm0 6h16v2H4z',
    gear: 'M10.3 2h3.4l.5 2.6 1.6.9 2.5-.9 1.7 2.9-2 1.8v1.8l2 1.8-1.7 2.9-2.5-.9-1.6.9-.5 2.6h-3.4l-.5-2.6-1.6-.9-2.5.9-1.7-2.9 2-1.8v-1.8l-2-1.8 1.7-2.9 2.5.9 1.6-.9zM12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z',
    info: 'M12 2a10 10 0 1 1 0 20 10 10 0 0 1 0-20zm-1 8v7h2v-7zm0-4v2h2V6z',
    clock: 'M12 2a10 10 0 1 1 0 20 10 10 0 0 1 0-20zm-1 5v6l5 3 1-1.7-4-2.4V7z',
    menu: 'M3 6h18v2H3zm0 5h18v2H3zm0 5h18v2H3z',
  } as const
  export type IconName = keyof typeof ICONS
</script>

<script lang="ts">
  interface Props {
    name: IconName
    size?: string
  }
  let { name, size = '1.25rem' }: Props = $props()

  function sized(node: SVGElement, s: string) {
    node.style.setProperty('--icon-size', s)
    return { update: (n: string) => node.style.setProperty('--icon-size', n) }
  }
</script>

<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false" use:sized={size}><path d={ICONS[name]} /></svg>

<style>
  .icon {
    flex: none;
    width: var(--icon-size);
    height: var(--icon-size);
    fill: currentColor;
  }
</style>
