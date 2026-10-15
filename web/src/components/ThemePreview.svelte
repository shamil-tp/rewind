<script lang="ts">
  // A non-interactive picture of the main screens, drawn with the shared
  // classes of app.css (rw-button, rw-input, rw-panel, rw-notice, …) and the
  // real tokens set on its root, so it uses the same CSS as the app. It shows
  // what Settings does not: navigation, the track list, a dialog, the mini
  // player and the Now Playing screen. The rest of the app is previewed live
  // while Settings is open.
  import { styleWith, type Tokens } from '../services/themes'

  interface Props {
    tokens: Tokens
  }
  let { tokens }: Props = $props()
</script>

<div class="specimen" use:styleWith={tokens} inert aria-hidden="true">
  <div class="sp-app">
    <div class="sp-rail">
      <span class="sp-brand">Rewind</span>
      <span class="sp-nav sp-current">Library ←</span>
      <span class="sp-nav">Playlists</span>
      <span class="sp-nav">Settings</span>
    </div>

    <div class="sp-main">
      <span class="sp-view-title">Library</span>
      <div class="sp-search">
        <span class="rw-input sp-input">Search artists, albums…</span>
        <span class="rw-button sp-button">Search</span>
      </div>
      <ul class="sp-tracks">
        <li class="sp-row sp-playing">
          <span class="sp-cell"><span class="sp-track">Opening Track</span><span class="sp-meta">Artist · Album</span></span>
          <span class="sp-tag">Playing</span>
        </li>
        <li class="sp-row sp-selected">
          <span class="sp-cell"><span class="sp-track">Second Song</span><span class="sp-meta">Artist · Album</span></span>
          <span class="sp-time">3:41</span>
        </li>
        <li class="sp-row">
          <span class="sp-cell"><span class="sp-track">Missing File</span><span class="sp-meta">Artist · Album</span></span>
          <span class="sp-badge">Unavailable</span>
        </li>
      </ul>

      <div class="rw-panel sp-dialog">
        <span class="rw-panel-title">Delete playlist?</span>
        <span class="rw-hint">“Road trip” will be removed. Its tracks stay in the library.</span>
        <span class="rw-label">Playlist name</span>
        <span class="rw-input sp-input">Road trip</span>
        <span class="sp-actions">
          <span class="rw-button sp-button">Delete</span>
          <span class="rw-button rw-button-secondary sp-button">Cancel</span>
          <span class="sp-link">Details</span>
        </span>
      </div>
      <span class="rw-notice sp-message">Scan finished: 3 tracks added.</span>
      <span class="rw-alert sp-message">Could not reach the Rewind server.</span>
    </div>
  </div>

  <div class="sp-mini">
    <span class="sp-art sp-art-small"></span>
    <span class="sp-cell"><span class="sp-track">Opening Track</span><span class="sp-meta">Artist · Album</span></span>
    <span class="sp-transport">
      <span class="rw-button rw-button-secondary sp-step">‹</span>
      <span class="rw-button sp-button">Pause</span>
      <span class="rw-button rw-button-secondary sp-step">›</span>
      <input class="sp-seek" type="range" min="0" max="100" value="40" tabindex="-1" aria-hidden="true" />
    </span>
  </div>

  <div class="sp-screen">
    <span class="sp-art sp-art-large"></span>
    <span class="sp-details">
      <span class="sp-label">Now playing</span>
      <span class="sp-np-title">Opening Track</span>
      <span class="sp-np-artist">Artist</span>
      <input class="sp-seek" type="range" min="0" max="100" value="40" tabindex="-1" aria-hidden="true" />
      <span class="sp-controls">
        <span class="rw-button rw-button-secondary sp-control">‹</span>
        <span class="rw-button sp-control sp-main-control">❚❚</span>
        <span class="rw-button rw-button-secondary sp-control">›</span>
      </span>
    </span>
  </div>
</div>

<style>
  /* The root re-reads the font and color tokens, because inherited values
     were resolved on <body> with the page's tokens, not the preview's. */
  .specimen {
    display: grid;
    gap: var(--rw-space-3);
    min-width: 0;
    padding: var(--rw-space-3);
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-bg);
    color: var(--rw-color-text);
    font-family: var(--rw-font-sans);
    font-size: var(--rw-font-size-md);
    line-height: var(--rw-line-height);
    overflow: hidden;
    user-select: none;
  }

  .sp-app {
    display: grid;
    grid-template-columns: 7.5rem minmax(0, 1fr);
    min-width: 0;
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    overflow: hidden;
  }

  .sp-rail {
    display: grid;
    align-content: start;
    gap: var(--rw-space-1);
    padding: var(--rw-space-3) var(--rw-space-2);
    border-right: var(--rw-border);
  }

  .sp-brand {
    margin-bottom: var(--rw-space-2);
    font-size: var(--rw-font-size-lg);
    font-weight: var(--rw-font-weight-black);
    line-height: 1;
    letter-spacing: -0.04em;
    text-transform: uppercase;
  }

  .sp-nav {
    padding: var(--rw-space-1) var(--rw-space-2);
    border: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-md);
    color: var(--rw-color-muted);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    font-weight: var(--rw-font-weight-bold);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .sp-current {
    border-color: var(--rw-color-border);
    background: var(--rw-color-primary);
    color: var(--rw-color-on-primary);
  }

  .sp-main {
    display: grid;
    gap: var(--rw-space-3);
    min-width: 0;
    padding: var(--rw-space-3);
  }

  .sp-view-title {
    font-size: calc(1.75rem * var(--rw-heading-scale));
    font-weight: var(--rw-font-weight-black);
    line-height: 1;
    letter-spacing: -0.03em;
    text-transform: uppercase;
  }

  .sp-search,
  .sp-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--rw-space-2);
  }

  .sp-input {
    display: flex;
    align-items: center;
    flex: 1 1 8rem;
    width: auto;
    min-width: 0;
    color: var(--rw-color-muted);
  }

  .sp-button {
    display: inline-grid;
    place-items: center;
    padding-inline: var(--rw-space-3);
    cursor: default;
  }

  .sp-tracks {
    display: grid;
    gap: var(--rw-space-1);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .sp-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--rw-space-2);
    min-height: 3rem;
    padding: var(--rw-space-2) var(--rw-space-3);
    border-left: var(--rw-border-width) solid transparent;
    border-radius: var(--rw-radius-sm);
  }

  .sp-selected {
    background: var(--rw-color-surface);
  }

  .sp-playing {
    border-left-color: var(--rw-color-accent);
  }

  .sp-cell {
    display: grid;
    min-width: 0;
  }

  .sp-track {
    overflow: hidden;
    font-weight: var(--rw-font-weight-bold);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sp-meta,
  .sp-time {
    overflow: hidden;
    color: var(--rw-color-muted);
    font-size: var(--rw-font-size-sm);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sp-time {
    font-family: var(--rw-font-mono);
  }

  .sp-tag,
  .sp-badge {
    flex: none;
    padding: 0 var(--rw-space-1);
    border-radius: var(--rw-radius-xs);
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    text-transform: uppercase;
  }

  .sp-tag {
    background: var(--rw-color-accent);
    color: var(--rw-color-on-accent);
  }

  .sp-badge {
    border: var(--rw-border-width) solid var(--rw-color-danger);
    color: var(--rw-color-danger);
  }

  .sp-dialog {
    gap: var(--rw-space-2);
  }

  .sp-link {
    color: var(--rw-color-primary);
    text-decoration: underline;
  }

  .sp-message {
    display: block;
    font-size: var(--rw-font-size-sm);
  }

  .sp-mini {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) minmax(0, 1.4fr);
    align-items: center;
    gap: var(--rw-space-3);
    padding: var(--rw-space-2) var(--rw-space-3);
    border: var(--rw-border);
    border-radius: var(--rw-radius-xl);
    background: var(--rw-color-surface);
    box-shadow: var(--rw-shadow);
  }

  .sp-art {
    display: block;
    aspect-ratio: 1;
    border: var(--rw-border);
    background: var(--rw-color-secondary);
  }

  .sp-art-small {
    width: 2.75rem;
    border-radius: var(--rw-radius-sm);
  }

  .sp-transport {
    display: flex;
    align-items: center;
    gap: var(--rw-space-1);
    min-width: 0;
  }

  .sp-step {
    min-width: 2.25rem;
    padding: 0 var(--rw-space-2);
    cursor: default;
  }

  .sp-seek {
    flex: 1 1 4rem;
    min-width: 3rem;
    margin: 0;
    accent-color: var(--rw-color-accent);
  }

  .sp-screen {
    display: grid;
    grid-template-columns: minmax(0, 9rem) minmax(0, 1fr);
    align-items: center;
    gap: var(--rw-space-4);
    padding: var(--rw-space-4);
    border: var(--rw-border);
    border-radius: var(--rw-radius-lg);
    background: var(--rw-color-bg);
  }

  .sp-art-large {
    width: 100%;
    border-radius: var(--rw-radius-art);
    box-shadow: var(--rw-shadow);
  }

  .sp-details {
    display: grid;
    gap: var(--rw-space-1);
    min-width: 0;
  }

  .sp-label {
    font-family: var(--rw-font-mono);
    font-size: 0.7rem;
    font-weight: var(--rw-font-weight-bold);
    letter-spacing: 0.12em;
    text-transform: uppercase;
  }

  .sp-np-title {
    overflow: hidden;
    font-size: calc(1.6rem * var(--rw-heading-scale));
    font-weight: var(--rw-font-weight-black);
    line-height: 1.05;
    letter-spacing: -0.02em;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sp-np-artist {
    font-size: var(--rw-font-size-lg);
    font-weight: var(--rw-font-weight-bold);
  }

  .sp-controls {
    display: flex;
    align-items: center;
    gap: var(--rw-space-3);
    margin-top: var(--rw-space-2);
  }

  .sp-control {
    display: grid;
    place-items: center;
    width: 2.75rem;
    height: 2.75rem;
    min-height: 0;
    padding: 0;
    cursor: default;
  }

  .sp-main-control {
    width: 3.5rem;
    height: 3.5rem;
    border-radius: var(--rw-radius-round);
    box-shadow: var(--rw-shadow);
  }

  @media (max-width: 36rem) {
    .sp-app {
      grid-template-columns: minmax(0, 1fr);
    }

    .sp-rail {
      grid-auto-flow: column;
      justify-content: start;
      align-items: center;
      border-right: 0;
      border-bottom: var(--rw-border);
    }

    .sp-brand {
      margin: 0 var(--rw-space-2) 0 0;
    }

    .sp-mini {
      grid-template-columns: auto minmax(0, 1fr);
    }

    .sp-transport {
      grid-column: 1 / -1;
    }

    .sp-screen {
      grid-template-columns: minmax(0, 1fr);
      justify-items: center;
    }

    .sp-art-large {
      width: min(100%, 10rem);
    }

    .sp-details {
      width: 100%;
    }
  }
</style>
