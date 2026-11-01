<script>
  import { api, post } from './api.js'
  import Dialog from './Dialog.svelte'

  let { app, onclose } = $props()

  let info = $state(null)
  let current = $state('')
  let next = $state('')
  let target = $state('password')
  let message = $state('')
  let error = $state('')
  let totp = $state(null)
  let totpCode = $state('')

  async function load() {
    info = await api('/settings')
  }

  $effect(() => {
    load()
  })

  async function run(fn, success) {
    message = error = ''
    try {
      await fn()
      message = success
      current = next = ''
      await load()
    } catch (err) {
      error = err.message
    }
  }

  function saveCredential(e) {
    e.preventDefault()
    const body = { current, ...(target === 'passphrase' ? { passphrase: next } : { password: next }) }
    const labels = { password: 'Kennwort', passphrase: 'Passphrase', sftp: 'SFTP-Zugang' }
    run(() => post(`/settings/${target}`, body), `${labels[target]} gespeichert.`)
  }

  const beginTotp = () => run(async () => (totp = await post('/settings/totp/begin')), '')
  const confirmTotp = () => run(async () => {
    await post('/settings/totp/confirm', { code: totpCode.trim() })
    totp = null
    totpCode = ''
  }, 'Zwei-Faktor-Anmeldung ist aktiv.')
</script>

<Dialog title="Einstellungen" {onclose}>
  {#if info}
    <section>
      <h3>Verbindung</h3>
      <p>Telegram: {app.telegram?.user ?? 'nicht verbunden'}</p>
      <p>SFTP: {info.sftpEnabled ? `aktiv, Benutzer „${info.username}“, Port 2222` : 'aus, bis ein SFTP-Kennwort gesetzt ist'}</p>
    </section>

    <section>
      <h3>Zugangsdaten ändern</h3>
      <form onsubmit={saveCredential}>
        <label>Was ändern?
          <select bind:value={target}>
            <option value="password">Web-Kennwort</option>
            <option value="sftp">SFTP-Kennwort (leer = SFTP aus)</option>
            <option value="passphrase">Passphrase der Verschlüsselung</option>
          </select>
        </label>
        <label>Aktuelles Web-Kennwort<input type="password" bind:value={current} required autocomplete="current-password" /></label>
        <label>Neuer Wert<input type="password" bind:value={next} required={target !== 'sftp'} autocomplete="new-password" /></label>
        <button class="secondary">Speichern</button>
      </form>
    </section>

    <section>
      <h3>Zwei-Faktor-Anmeldung</h3>
      {#if info.totpEnabled}
        <p>Aktiv. Zum Abschalten aktuelles Web-Kennwort oben eintragen.</p>
        <button class="secondary" onclick={() => run(() => post('/settings/totp/disable', { current }), 'Zwei-Faktor-Anmeldung ist aus.')}>Abschalten</button>
      {:else if totp}
        <p>Trage diesen Schlüssel in deine Authenticator-App ein:</p>
        <code>{totp.secret}</code>
        <label>Code aus der App<input bind:value={totpCode} inputmode="numeric" autocomplete="one-time-code" /></label>
        <button class="secondary" onclick={confirmTotp}>Bestätigen</button>
      {:else}
        <button class="secondary" onclick={beginTotp}>Einrichten</button>
      {/if}
    </section>

    <section>
      <h3>Backup der Metadaten</h3>
      <p>{info.backupAt ? `Letztes Backup: ${new Date(info.backupAt).toLocaleString('de-DE')}` : 'Noch kein Backup'}. Backups liegen verschlüsselt im Speicherkanal.</p>
      <button class="secondary" onclick={() => run(() => post('/backup'), 'Backup wurde gespeichert.')}>Jetzt sichern</button>
    </section>

    {#if message}<p class="ok" role="status">{message}</p>{/if}
    {#if error}<p class="err" role="alert">{error}</p>{/if}
  {/if}
</Dialog>

<style>
  section {
    padding: 12px 0;
    border-top: 1px solid var(--line);
  }
  section:first-child {
    border-top: 0;
    padding-top: 0;
  }
  h3 {
    margin: 0 0 8px;
    font-size: 14px;
    font-weight: 600;
  }
  p {
    margin: 0 0 8px;
    color: var(--muted);
  }
  label {
    display: grid;
    gap: 4px;
    margin-bottom: 10px;
  }
  code {
    display: block;
    padding: 8px;
    margin-bottom: 10px;
    background: var(--surface-2);
    border-radius: 4px;
    overflow-wrap: anywhere;
    user-select: all;
  }
  .ok {
    color: var(--text);
  }
  .err {
    color: var(--danger);
  }
</style>
