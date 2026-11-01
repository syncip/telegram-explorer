<script>
  import { api, formatDate } from './api.js'
  import Icon from './Icon.svelte'

  let { onerror } = $props()

  const LEVELS = { '': 'Alle Stufen', info: 'Info', warn: 'Warnungen', error: 'Fehler' }
  const KINDS = { '': 'Alle Bereiche', upload: 'Upload', download: 'Download', share: 'Freigabe', delete: 'Löschen', verify: 'Prüfung', login: 'Anmeldung', telegram: 'Telegram', system: 'System' }

  let events = $state(null)
  let level = $state('')
  let kind = $state('')

  async function load() {
    try {
      events = await api(`/logs?level=${level}&kind=${kind}`)
    } catch (err) {
      onerror(err.message)
    }
  }

  async function clear() {
    await api('/logs', { method: 'DELETE' })
    load()
  }

  $effect(() => {
    level, kind
    load()
    const timer = setInterval(load, 4000)
    return () => clearInterval(timer)
  })
</script>

<div class="head">
  <select bind:value={level} aria-label="Stufe">
    {#each Object.entries(LEVELS) as [value, label] (value)}<option {value}>{label}</option>{/each}
  </select>
  <select bind:value={kind} aria-label="Bereich">
    {#each Object.entries(KINDS) as [value, label] (value)}<option {value}>{label}</option>{/each}
  </select>
  <span class="grow"></span>
  <button class="secondary" onclick={clear}><span class="btn"><Icon name="trash" size={14} />Protokoll leeren</span></button>
</div>

{#if events && events.length === 0}
  <div class="empty">Keine Einträge.</div>
{:else if events}
  <div class="wrap">
    <table>
      <thead><tr><th class="t">Zeit</th><th class="l">Stufe</th><th class="k">Bereich</th><th>Meldung</th></tr></thead>
      <tbody>
        {#each events as e (e.id)}
          <tr class={e.level}>
            <td>{formatDate(e.ts)}</td>
            <td class="lvl">{LEVELS[e.level] ?? e.level}</td>
            <td>{KINDS[e.kind] ?? e.kind}</td>
            <td class="m">{e.message}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .head {
    position: sticky;
    top: 0;
    left: 0;
    z-index: 1;
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    padding: 8px 10px;
    background: var(--surface);
    border-bottom: 1px solid var(--line);
  }
  .grow {
    flex: 1;
  }
  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .empty {
    display: grid;
    place-items: center;
    min-height: 240px;
    color: var(--muted);
  }
  .wrap {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th {
    text-align: left;
    font-weight: 400;
    color: var(--muted);
    border-bottom: 1px solid var(--line);
    padding: 6px 10px;
  }
  .t {
    width: 140px;
  }
  .l {
    width: 100px;
  }
  .k {
    width: 100px;
  }
  td {
    padding: 5px 10px;
    vertical-align: top;
    white-space: nowrap;
  }
  td.m {
    white-space: normal;
    overflow-wrap: anywhere;
    min-width: 200px;
  }
  tbody tr:hover {
    background: var(--hover);
  }
  tr.warn .lvl {
    color: var(--warning-text);
    font-weight: 600;
  }
  tr.error .lvl {
    color: var(--danger);
    font-weight: 600;
  }
</style>
