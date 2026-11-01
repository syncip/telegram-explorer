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
  let sftpCurrent = $state('')
  let sftpPassword = $state('')
  let blockMiB = $state(1024)

  const BLOCK_SIZES = [128, 256, 512, 1024, 2000]

  async function load() {
    info = await api('/settings')
    blockMiB = info.blockMiB
  }

  $effect(() => {
    load()
  })

  async function run(fn, success) {
    message = error = ''
    try {
      await fn()
      message = success
      current = next = sftpCurrent = sftpPassword = ''
      await load()
    } catch (err) {
      error = err.message
    }
  }

  function saveCredential(e) {
    e.preventDefault()
    const body = { current, ...(target === 'passphrase' ? { passphrase: next } : { password: next }) }
    const labels = { password: 'Kennwort', passphrase: 'Passphrase' }
    run(() => post(`/settings/${target}`, body), `${labels[target]} gespeichert.`)
  }

  function saveSftp(e) {
    e.preventDefault()
    run(() => post('/settings/sftp', { current: sftpCurrent, password: sftpPassword }), 'SFTP-Kennwort gespeichert, SFTP ist aktiv.')
  }

  const disableSftp = () => run(() => post('/settings/sftp', { current: sftpCurrent, password: '' }), 'SFTP ist aus, der Port ist geschlossen.')
  const saveBlocks = () => run(() => post('/settings/blocks', { mib: Number(blockMiB) }), 'Segmentgröße gespeichert. Sie gilt für neue Uploads.')

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
    </section>

    <section>
      <h3>SFTP</h3>
      <p>{info.sftpEnabled ? `Aktiv, Benutzer „${info.username}“, Port 2222.` : 'Aus, der Port ist geschlossen. Mit einem Kennwort wird SFTP eingeschaltet.'}</p>
      <form onsubmit={saveSftp}>
        <label>Aktuelles Web-Kennwort<input type="password" bind:value={sftpCurrent} required autocomplete="current-password" /></label>
        <label>{info.sftpEnabled ? 'Neues SFTP-Kennwort' : 'SFTP-Kennwort'}<input type="password" bind:value={sftpPassword} required autocomplete="new-password" /></label>
        <div class="row">
          <button class="secondary">{info.sftpEnabled ? 'Kennwort ändern' : 'SFTP einschalten'}</button>
          {#if info.sftpEnabled}<button type="button" class="secondary" disabled={!sftpCurrent} onclick={disableSftp}>SFTP abschalten</button>{/if}
        </div>
      </form>
    </section>

    <section>
      <h3>Segmentgröße</h3>
      <p>Größere Dateien werden in Segmente dieser Größe geteilt, jedes Segment ist eine Nachricht bei Telegram. Bei einem Abbruch wird nur das laufende Segment wiederholt. Die Größe gilt für neue Uploads, gespeicherte Dateien bleiben wie sie sind.</p>
      <p>Unverschlüsselte Dateien bleiben bis zur doppelten Segmentgröße am Stück, höchstens bis 2000 MiB.</p>
      <label>Größe pro Segment
        <select bind:value={blockMiB} onchange={saveBlocks}>
          {#if !BLOCK_SIZES.includes(info.blockMiB)}<option value={info.blockMiB}>{info.blockMiB} MiB</option>{/if}
          {#each BLOCK_SIZES as size}<option value={size}>{size} MiB{size === 1024 ? ' (Standard)' : ''}</option>{/each}
        </select>
      </label>
    </section>

    <section>
      <h3>Zugangsdaten ändern</h3>
      <form onsubmit={saveCredential}>
        <label>Was ändern?
          <select bind:value={target}>
            <option value="password">Web-Kennwort</option>
            <option value="passphrase">Passphrase der Verschlüsselung</option>
          </select>
        </label>
        <label>Aktuelles Web-Kennwort<input type="password" bind:value={current} required autocomplete="current-password" /></label>
        <label>Neuer Wert<input type="password" bind:value={next} required autocomplete="new-password" /></label>
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
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
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
