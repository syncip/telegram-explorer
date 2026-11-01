<script>
  import { copyText, post } from './api.js'
  import Dialog from './Dialog.svelte'

  let { node, onclose } = $props()

  let password = $state('')
  let hours = $state(168)
  let maxDownloads = $state(0)
  let link = $state('')
  let error = $state('')
  let copied = $state(false)

  async function create(e) {
    e.preventDefault()
    error = ''
    try {
      const res = await post('/shares', { nodeId: node.id, password, expiresHours: Number(hours), maxDownloads: Number(maxDownloads) })
      link = `${location.origin}/s/${res.token}`
    } catch (err) {
      error = err.message
    }
  }

  async function copy() {
    try {
      await copyText(link)
      copied = true
    } catch (err) {
      error = err.message
    }
  }
</script>

<Dialog title="„{node.name}“ teilen" {onclose}>
  {#if link}
    <p>Der Link ist bereit. Wer ihn hat, kann die Datei entschlüsselt herunterladen.</p>
    <input readonly value={link} aria-label="Freigabelink" onfocus={(e) => e.target.select()} />
    <div class="actions">
      <button class="primary" onclick={copy}>{copied ? 'Kopiert' : 'Link kopieren'}</button>
      <button class="secondary" onclick={onclose}>Fertig</button>
    </div>
  {:else}
    <form onsubmit={create}>
      <label>Gültig für
        <select bind:value={hours}>
          <option value={1}>1 Stunde</option>
          <option value={24}>1 Tag</option>
          <option value={168}>7 Tage</option>
          <option value={720}>30 Tage</option>
          <option value={0}>Unbegrenzt</option>
        </select>
      </label>
      <label>Kennwort (optional)<input type="password" bind:value={password} autocomplete="new-password" /></label>
      <label>Maximale Downloads (0 = unbegrenzt)<input type="number" min="0" bind:value={maxDownloads} /></label>
      {#if maxDownloads > 0}<p class="note">Links mit Limit können abgebrochene Downloads nicht fortsetzen.</p>{/if}
      {#if error}<p class="err" role="alert">{error}</p>{/if}
      <div class="actions">
        <button class="primary">Link erstellen</button>
        <button type="button" class="secondary" onclick={onclose}>Abbrechen</button>
      </div>
    </form>
  {/if}
</Dialog>

<style>
  label {
    display: grid;
    gap: 4px;
    margin-bottom: 12px;
  }
  input[readonly] {
    width: 100%;
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
