<script>
  import { api, formatDate } from './api.js'
  import Dialog from './Dialog.svelte'
  import Lifetime, { KEEP } from './Lifetime.svelte'

  let { share, onclose, onsaved } = $props()

  let amount = $state(7)
  let unit = $state(KEEP)
  let passwordMode = $state('keep')
  let password = $state('')
  let maxDownloads = $state(share.maxDownloads)
  let newLink = $state(false)
  let error = $state('')
  let busy = $state(false)

  const current = share.expiresAt ? `Nicht ändern (bis ${formatDate(share.expiresAt)})` : 'Nicht ändern (unbegrenzt)'

  async function save(e) {
    e.preventDefault()
    error = ''
    busy = true
    const body = { maxDownloads: Number(maxDownloads), newLink }
    if (unit !== KEEP) body.expiresMinutes = unit * Number(amount)
    if (passwordMode !== 'keep') body.password = passwordMode === 'set' ? password : ''
    try {
      await api(`/shares/${share.token}`, { method: 'PATCH', body })
      onsaved()
    } catch (err) {
      error = err.message
      busy = false
    }
  }
</script>

<Dialog title="„{share.name}“ bearbeiten" {onclose}>
  <form onsubmit={save}>
    <Lifetime bind:amount bind:unit keep={current} />
    <label>Kennwort
      <select bind:value={passwordMode}>
        <option value="keep">{share.hasPassword ? 'Beibehalten' : 'Kein Kennwort'}</option>
        <option value="set">{share.hasPassword ? 'Ändern' : 'Kennwort setzen'}</option>
        {#if share.hasPassword}<option value="remove">Entfernen</option>{/if}
      </select>
    </label>
    {#if passwordMode === 'set'}
      <label>Neues Kennwort<input type="password" bind:value={password} required autocomplete="new-password" /></label>
    {/if}
    <label>Maximale Downloads (0 = unbegrenzt, bisher {share.downloads})<input type="number" min="0" step="1" required bind:value={maxDownloads} /></label>
    <label class="check"><input type="checkbox" bind:checked={newLink} /> Neuen Link erzeugen</label>
    {#if newLink}<p class="note">Der bisherige Link funktioniert danach nicht mehr.</p>{/if}
    {#if error}<p class="err" role="alert">{error}</p>{/if}
    <div class="actions">
      <button class="primary" disabled={busy}>Speichern</button>
      <button type="button" class="secondary" onclick={onclose}>Abbrechen</button>
    </div>
  </form>
</Dialog>

<style>
  label {
    display: grid;
    gap: 4px;
    margin-bottom: 12px;
  }
  label.check {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  p {
    margin: 0 0 12px;
  }
  .note {
    color: var(--muted);
  }
  .err {
    color: var(--danger);
  }
  .actions {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
    margin-top: 16px;
  }
</style>
