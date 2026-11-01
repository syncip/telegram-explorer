<script>
  import { copyText, post } from './api.js'
  import Dialog from './Dialog.svelte'
  import Lifetime from './Lifetime.svelte'

  let { node, onclose } = $props()

  let password = $state('')
  let amount = $state(7)
  let unit = $state(1440)
  let maxDownloads = $state(0)
  let link = $state('')
  let error = $state('')
  let copied = $state(false)

  async function create(e) {
    e.preventDefault()
    error = ''
    try {
      const res = await post('/shares', { nodeId: node.id, password, expiresMinutes: unit * Number(amount), maxDownloads: Number(maxDownloads) })
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
    <p>{node.isDir ? 'Der Link ist bereit. Wer ihn hat, kann den Ordner entschlüsselt als ZIP laden.' : maxDownloads > 0 ? 'Der Link ist bereit. Er führt zu einer Seite mit Download-Knopf, damit Link-Vorschauen das Limit nicht verbrauchen.' : 'Der Link ist bereit. Bilder, Videos und PDFs öffnen sich direkt, andere Dateien starten als Download.'}</p>
    <input readonly value={link} aria-label="Freigabelink" onfocus={(e) => e.target.select()} />
    <div class="actions">
      <button class="primary" onclick={copy}>{copied ? 'Kopiert' : 'Link kopieren'}</button>
      <button class="secondary" onclick={onclose}>Fertig</button>
    </div>
  {:else}
    <form onsubmit={create}>
      <Lifetime bind:amount bind:unit />
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
