<script>
  import { api, formatDate, formatSize, post, ROOT, typeLabel } from './api.js'
  import Dialog from './Dialog.svelte'

  let { node, onclose, onchange } = $props()

  let policy = $state(node.policy)
  let result = $state('')
  let busy = $state(false)

  const stateLabels = { ready: 'Bei Telegram gespeichert', pending: 'Wird übertragen', failed: 'Fehlgeschlagen' }

  async function savePolicy() {
    await api(`/nodes/${node.id}`, { method: 'PATCH', body: { policy: Number(policy) } })
    onchange()
  }

  async function verify(deep) {
    busy = true
    try {
      await post('/verify', { ids: [node.id], deep })
      result = 'Prüfung läuft. Fortschritt und Ergebnis stehen bei den Übertragungen und in der Spalte „Zustand“.'
    } catch (err) {
      result = `Prüfung fehlgeschlagen: ${err.message}`
    } finally {
      busy = false
    }
  }
</script>

<Dialog title="Eigenschaften" {onclose}>
  <dl>
    <dt>Name</dt><dd>{node.name}</dd>
    <dt>Typ</dt><dd>{typeLabel(node)}</dd>
    {#if node.isDir}
      <dt>Inhalt</dt><dd>{node.fileCount ?? 0} Dateien, {formatSize(node.size)}</dd>
    {:else}
      <dt>Größe</dt><dd>{formatSize(node.size)}</dd>
      <dt>Schutz</dt><dd>{node.encrypted ? 'Verschlüsselt' : 'Unverschlüsselt, in Telegram lesbar'}</dd>
      <dt>Status</dt><dd>{stateLabels[node.state]}</dd>
      <dt>Segmente</dt><dd>{node.segments}</dd>
      {#if node.md5}<dt>MD5</dt><dd class="hash">{node.md5}</dd>{/if}
      {#if node.sha256}<dt>SHA-256</dt><dd class="hash">{node.sha256}</dd>{/if}
    {/if}
    <dt>Geändert</dt><dd>{formatDate(node.mtime)}</dd>
    <dt>Erstellt</dt><dd>{formatDate(node.ctime)}</dd>
  </dl>
  {#if node.isDir}
    <label>
      Uploads in diesem Ordner
      <select bind:value={policy} onchange={savePolicy}>
        {#if node.id !== ROOT}<option value={0}>Wie übergeordneter Ordner</option>{/if}
        <option value={1}>Verschlüsseln</option>
        <option value={2}>Nicht verschlüsseln</option>
      </select>
    </label>
  {:else}
    <div class="checks">
      <button class="secondary" disabled={busy} title="Prüft ohne Download, ob die Datei noch bei Telegram liegt" onclick={() => verify(false)}>Datei prüfen</button>
      <button class="secondary" disabled={busy} title="Lädt die Datei herunter und vergleicht die Prüfsummen" onclick={() => verify(true)}>Inhalt prüfen</button>
    </div>
    {#if result}<p>{result}</p>{/if}
  {/if}
</Dialog>

<style>
  .checks {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  dl {
    display: grid;
    grid-template-columns: 90px 1fr;
    gap: 6px 12px;
    margin: 0 0 16px;
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .hash {
    font: 12px Consolas, monospace;
  }
  label {
    display: grid;
    gap: 4px;
  }
  p {
    margin: 10px 0 0;
  }
</style>
