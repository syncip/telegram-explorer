<script>
  import { api, post } from './api.js'
  import Dialog from './Dialog.svelte'
  import { getTheme, setTheme } from './theme.js'

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
  let totpCurrent = $state('')
  let theme = $state(getTheme())
  let limitUp = $state(0)
  let limitDown = $state(0)
  let tab = $state('general')

  const TABS = [
    { id: 'general', label: 'Allgemein' },
    { id: 'storage', label: 'Speicher' },
    { id: 'security', label: 'Sicherheit' },
    { id: 'sftp', label: 'SFTP' },
  ]

  function show(id) {
    tab = id
    message = error = ''
  }

  const BLOCK_SIZES = [128, 256, 512, 1024, 2000]

  async function load() {
    info = await api('/settings')
    blockMiB = info.blockMiB
    limitUp = toMiB(info.limitUp)
    limitDown = toMiB(info.limitDown)
  }

  $effect(() => {
    load()
  })

  async function run(fn, success) {
    message = error = ''
    try {
      await fn()
      message = success
      current = next = sftpCurrent = sftpPassword = totpCurrent = ''
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
  const MIB = 1 << 20
  const toMiB = (bytes) => Math.round((bytes / MIB) * 10) / 10
  const saveLimits = (e) => {
    e.preventDefault()
    run(() => post('/settings/limits', { up: Math.round(Number(limitUp) * MIB), down: Math.round(Number(limitDown) * MIB) }), 'Geschwindigkeit gespeichert.')
  }
  const saveBlocks = () => run(() => post('/settings/blocks', { mib: Number(blockMiB) }), 'Segmentgröße gespeichert. Sie gilt für neue Uploads.')

  const beginTotp = () => run(async () => (totp = await post('/settings/totp/begin')), '')
  const confirmTotp = () => run(async () => {
    await post('/settings/totp/confirm', { code: totpCode.trim() })
    totp = null
    totpCode = ''
  }, 'Zwei-Faktor-Anmeldung ist aktiv.')

  function disableTotp(e) {
    e.preventDefault()
    run(() => post('/settings/totp/disable', { current: totpCurrent }), 'Zwei-Faktor-Anmeldung ist aus.')
  }
</script>

<Dialog title="Einstellungen" {onclose}>
  {#if info}
    <div class="tabs" role="tablist" aria-label="Bereiche">
      {#each TABS as t (t.id)}
        <button role="tab" aria-selected={tab === t.id} class:active={tab === t.id} onclick={() => show(t.id)}>{t.label}</button>
      {/each}
    </div>

    <div class="content" role="tabpanel">
      {#if tab === 'general'}
        <dl>
          <dt>Telegram</dt><dd>{app.telegram?.user ?? 'nicht verbunden'}</dd>
          <dt>Segmentgröße</dt><dd>{info.blockMiB} MiB</dd>
          <dt>Letztes Backup</dt><dd>{info.backupAt ? new Date(info.backupAt).toLocaleString('de-DE') : 'Noch keines'}</dd>
          <dt>Zwei-Faktor</dt><dd>{info.totpEnabled ? 'Aktiv' : 'Aus'}</dd>
          <dt>SFTP</dt><dd>{info.sftpEnabled ? 'Aktiv' : 'Aus'}</dd>
        </dl>
        <label class="theme">Darstellung
          <select bind:value={theme} onchange={() => setTheme(theme)}>
            <option value="auto">Automatisch (wie das System)</option>
            <option value="light">Hell</option>
            <option value="dark">Dunkel</option>
          </select>
        </label>
      {:else if tab === 'storage'}
        <section>
          <h3>Segmentgröße</h3>
          <p>Größere Dateien werden in Segmente geteilt, jedes ist eine Nachricht bei Telegram. Bei einem Abbruch wird nur das laufende Segment wiederholt.</p>
          <p>Die Größe gilt für neue Uploads. Unverschlüsselte Dateien bleiben bis zur doppelten Größe am Stück, höchstens bis 2000 MiB.</p>
          <label>Größe pro Segment
            <select bind:value={blockMiB} onchange={saveBlocks}>
              {#if !BLOCK_SIZES.includes(info.blockMiB)}<option value={info.blockMiB}>{info.blockMiB} MiB</option>{/if}
              {#each BLOCK_SIZES as size}<option value={size}>{size} MiB{size === 1024 ? ' (Standard)' : ''}</option>{/each}
            </select>
          </label>
        </section>

        <section>
          <h3>Geschwindigkeit</h3>
          <p>Begrenzt den Verkehr zwischen Server und Telegram, 0 heißt unbegrenzt. Schnell umstellen geht auch unten rechts in der Statusleiste.</p>
          <form onsubmit={saveLimits}>
            <div class="pair">
              <label>Upload in MB/s<input type="number" min="0" step="0.1" required bind:value={limitUp} /></label>
              <label>Download in MB/s<input type="number" min="0" step="0.1" required bind:value={limitDown} /></label>
            </div>
            <button class="secondary">Speichern</button>
          </form>
        </section>

        <section>
          <h3>Backup der Metadaten</h3>
          <p>{info.backupAt ? `Letztes Backup: ${new Date(info.backupAt).toLocaleString('de-DE')}` : 'Noch kein Backup'}. Backups liegen verschlüsselt im Speicherkanal.</p>
          <button class="secondary" onclick={() => run(() => post('/backup'), 'Backup wurde gespeichert.')}>Jetzt sichern</button>
        </section>
      {:else if tab === 'security'}
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
            <p>Aktiv.</p>
            <form onsubmit={disableTotp}>
              <label>Aktuelles Web-Kennwort<input type="password" bind:value={totpCurrent} required autocomplete="current-password" /></label>
              <button class="secondary">Abschalten</button>
            </form>
          {:else if totp}
            <p>Trage diesen Schlüssel in deine Authenticator-App ein:</p>
            <code>{totp.secret}</code>
            <label>Code aus der App<input bind:value={totpCode} inputmode="numeric" autocomplete="one-time-code" /></label>
            <button class="secondary" onclick={confirmTotp}>Bestätigen</button>
          {:else}
            <p>Aus. Bei der Anmeldung wird nur das Kennwort abgefragt.</p>
            <button class="secondary" onclick={beginTotp}>Einrichten</button>
          {/if}
        </section>
      {:else}
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
      {/if}
    </div>

    {#if message}<p class="ok" role="status">{message}</p>{/if}
    {#if error}<p class="err" role="alert">{error}</p>{/if}
  {/if}
</Dialog>

<style>
  .tabs {
    display: flex;
    gap: 2px;
    margin-bottom: 14px;
    border-bottom: 1px solid var(--line);
  }
  .tabs button {
    padding: 6px 10px;
    border: 0;
    border-bottom: 2px solid transparent;
    background: none;
    color: var(--muted);
    cursor: pointer;
  }
  .tabs button.active {
    color: var(--text);
    border-bottom-color: var(--accent);
    font-weight: 600;
  }
  .content {
    min-height: 230px;
  }
  dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 8px 16px;
    margin: 0;
  }
  dt {
    color: var(--muted);
  }
  .theme {
    margin-top: 20px;
  }
  .pair {
    display: flex;
    gap: 12px;
  }
  .pair label {
    flex: 1;
    min-width: 0;
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
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
