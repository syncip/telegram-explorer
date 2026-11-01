<script>
  import { api, formatDate } from './api.js'
  import Icon from './Icon.svelte'

  let { onerror } = $props()
  let shares = $state(null)

  async function load() {
    try {
      shares = await api('/shares')
    } catch (err) {
      onerror(err.message)
    }
  }

  async function remove(token) {
    try {
      await api(`/shares/${token}`, { method: 'DELETE' })
      await load()
    } catch (err) {
      onerror(err.message)
    }
  }

  const expired = (s) => (s.expiresAt && s.expiresAt * 1000 < Date.now()) || (s.maxDownloads && s.downloads >= s.maxDownloads)

  $effect(() => {
    load()
  })

  const link = (token) => `${location.origin}/s/${token}`
</script>

{#if shares && shares.length === 0}
  <div class="empty">Noch keine Freigaben. Rechtsklick auf eine Datei und „Teilen“.</div>
{:else if shares}
  <div class="wrap">
    <table>
      <thead><tr><th>Name</th><th>Status</th><th>Läuft ab</th><th>Downloads</th><th>Kennwort</th><th></th></tr></thead>
      <tbody>
        {#each shares as share (share.token)}
          <tr>
            <td class="n">{share.name}</td>
            <td>{share.trashed ? 'Datei im Papierkorb' : expired(share) ? 'Abgelaufen' : 'Aktiv'}</td>
            <td>{share.expiresAt ? formatDate(share.expiresAt) : 'Nie'}</td>
            <td>{share.downloads}{share.maxDownloads ? ` / ${share.maxDownloads}` : ''}</td>
            <td>{share.hasPassword ? 'Ja' : 'Nein'}</td>
            <td class="a">
              <button class="icon" title="Link kopieren" aria-label="Link kopieren" onclick={() => navigator.clipboard.writeText(link(share.token))}><Icon name="copy" size={15} /></button>
              <button class="secondary end" onclick={() => remove(share.token)}>Freigabe beenden</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .empty {
    display: grid;
    place-items: center;
    min-height: 240px;
    height: 100%;
    padding: 16px;
    text-align: center;
    color: var(--muted);
  }
  .wrap {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    white-space: nowrap;
  }
  th {
    text-align: left;
    font-weight: 400;
    color: var(--muted);
    border-bottom: 1px solid var(--line);
    padding: 6px 10px;
  }
  td {
    padding: 2px 10px;
    height: 36px;
  }
  tbody tr:hover {
    background: var(--hover);
  }
  .n {
    max-width: 320px;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .a {
    text-align: right;
    white-space: nowrap;
  }
  .end {
    height: 28px;
    padding: 0 10px;
  }
</style>
