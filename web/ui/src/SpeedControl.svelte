<script>
  import { formatSize, post } from './api.js'

  let { speed, onchange, onerror } = $props()
  let open = $state(false)

  const MIB = 1 << 20
  const PRESETS = [0, 1, 2, 5, 10, 20, 50, 100].map((n) => n * MIB)
  const DIRECTIONS = [
    { key: 'Down', label: 'Download von Telegram' },
    { key: 'Up', label: 'Upload zu Telegram' },
  ]

  const rate = (n) => `${formatSize(n)}/s`
  const label = (limit) => (limit ? rate(limit) : 'Unbegrenzt')

  async function choose(key, value) {
    const next = { up: speed.limitUp, down: speed.limitDown, [key.toLowerCase()]: value }
    open = false
    try {
      await post('/settings/limits', next)
      onchange()
    } catch (err) {
      onerror(err.message)
    }
  }
</script>

<svelte:window onclick={() => (open = false)} />

<span class="speed">
  <button
    class="toggle"
    class:limited={speed.limitUp || speed.limitDown}
    title="Geschwindigkeit zu Telegram. Klicken, um sie zu begrenzen."
    aria-haspopup="menu"
    aria-expanded={open}
    onclick={(e) => { e.stopPropagation(); open = !open }}
  >
    <span title="Download von Telegram">↓ {rate(speed.down)}{speed.limitDown ? ` / ${label(speed.limitDown)}` : ''}</span>
    <span title="Upload zu Telegram">↑ {rate(speed.up)}{speed.limitUp ? ` / ${label(speed.limitUp)}` : ''}</span>
    <span class="caret">▾</span>
  </button>
  {#if open}
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div class="pop" role="menu" tabindex="-1" onclick={(e) => e.stopPropagation()}>
      {#each DIRECTIONS as dir (dir.key)}
        <div class="col">
          <strong>{dir.label}</strong>
          {#each PRESETS as value (value)}
            <button role="menuitemradio" aria-checked={speed[`limit${dir.key}`] === value} class:on={speed[`limit${dir.key}`] === value} onclick={() => choose(dir.key, value)}>
              {label(value)}
            </button>
          {/each}
          {#if !PRESETS.includes(speed[`limit${dir.key}`])}
            <button role="menuitemradio" aria-checked="true" class="on">{label(speed[`limit${dir.key}`])}</button>
          {/if}
        </div>
      {/each}
      <p>Eigene Werte unter Einstellungen → Speicher.</p>
    </div>
  {/if}
</span>

<style>
  .speed {
    position: relative;
    flex: none;
  }
  .toggle {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    height: 22px;
    padding: 0 8px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--surface-2);
    color: var(--text);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    cursor: pointer;
  }
  .toggle.limited {
    border-color: var(--accent);
  }
  .caret {
    color: var(--muted);
  }
  .pop {
    position: absolute;
    right: 0;
    bottom: calc(100% + 6px);
    z-index: 40;
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 4px 16px;
    width: max-content;
    padding: 10px 12px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 8px;
    box-shadow: var(--shadow-lg);
    color: var(--text);
  }
  .col {
    display: grid;
    gap: 2px;
  }
  strong {
    font-weight: 600;
    margin-bottom: 4px;
  }
  .col button {
    text-align: left;
    padding: 3px 8px;
    border: 0;
    border-radius: 4px;
    background: none;
    cursor: pointer;
  }
  .col button:hover {
    background: var(--hover);
  }
  .col button.on {
    background: var(--selected);
    font-weight: 600;
  }
  p {
    grid-column: 1 / -1;
    margin: 6px 0 0;
    color: var(--muted);
  }
</style>
