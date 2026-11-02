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
  let davCurrent = $state('')
  let davPassword = $state('')
  let blockMiB = $state(1024)
  let totpCurrent = $state('')
  let theme = $state(getTheme())
  let limitUp = $state(0)
  let limitDown = $state(0)
  let tab = $state('general')
  let bots = $state([])
  let botToken = $state('')
  let botBusy = $state(false)

  const TABS = [
    { id: 'general', label: 'Allgemein' },
    { id: 'storage', label: 'Speicher' },
    { id: 'security', label: 'Sicherheit' },
    { id: 'access', label: 'Zugänge' },
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
    if (info.bots) bots = await api('/settings/bots')
  }

  // Bots connect in the background, so their state is fetched again while
  // the storage tab is open.
  $effect(() => {
    if (tab !== 'storage' || !info?.bots) return
    const timer = setInterval(async () => {
      try {
        bots = await api('/settings/bots')
      } catch {}
    }, 4000)
    return () => clearInterval(timer)
  })

  const BOT_STATES = { connecting: 'verbindet', ready: 'bereit', error: 'Fehler' }
  const botName = (b) => (b.username ? '@' + b.username : `Bot ${b.id}`)

  async function botAction(fn) {
    message = error = ''
    botBusy = true
    try {
      const b = await fn()
      if (!b) message = 'Bot entfernt. Bei @BotFather besteht er weiter, dort kannst du ihn mit /deletebot löschen.'
      else if (b.state === 'error') error = `${botName(b)}: ${b.error}`
      else message = b.state === 'ready' ? `${botName(b)} lädt ab jetzt mit.` : `${botName(b)} verbindet sich noch.`
      botToken = ''
    } catch (err) {
      error = err.message
    }
    botBusy = false
    bots = await api('/settings/bots')
  }
  const createBot = () => botAction(() => post('/settings/bots'))
  function addBot(e) {
    e.preventDefault()
    botAction(() => post('/settings/bots', { token: botToken.trim() }))
  }
  function removeBot(b) {
    if (!confirm(`${botName(b)} aus dem Speicherkanal entfernen?`)) return
    botAction(async () => {
      await api(`/settings/bots/${b.id}`, { method: 'DELETE' })
      return null
    })
  }

  $effect(() => {
    load()
  })

  async function run(fn, success) {
    message = error = ''
    try {
      await fn()
      message = success
      current = next = sftpCurrent = sftpPassword = totpCurrent = davCurrent = davPassword = ''
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

  function saveDav(e) {
    e.preventDefault()
    run(() => post('/settings/webdav', { current: davCurrent, password: davPassword }), 'WebDAV-Kennwort gespeichert, WebDAV ist aktiv.')
  }
  const disableDav = () => run(() => post('/settings/webdav', { current: davCurrent, password: '' }), 'WebDAV ist aus, der Port ist geschlossen.')
  const davUrl = $derived(info?.webdavPort ? `https://${location.hostname}:${info.webdavPort}/` : '')

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
          <dt>WebDAV</dt><dd>{info.webdavEnabled ? 'Aktiv' : 'Aus'}</dd>
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

        {#if info.bots}
          <section>
            <h3>Download-Helfer</h3>
            <p>Telegram erlaubt jeder Anmeldung etwa 5 MB/s beim Herunterladen. Jeder Bot, der Admin im Speicherkanal ist, lädt mit und bringt ungefähr so viel dazu. Die App legt Bots bei @BotFather an und trägt sie im Kanal ein.</p>
            {#if bots.length}
              <ul class="bots">
                {#each bots as b (b.id)}
                  <li>
                    <span class="name">{botName(b)}</span>
                    <span class="state {b.state}" title={b.error ?? ''}>{BOT_STATES[b.state] ?? b.state}{b.error ? `: ${b.error}` : ''}</span>
                    {#if b.source === 'env'}
                      <span class="src">aus .env</span>
                    {:else}
                      <button class="secondary" disabled={botBusy} onclick={() => removeBot(b)}>Entfernen</button>
                    {/if}
                  </li>
                {/each}
              </ul>
            {:else}
              <p>Noch keine Download-Helfer.</p>
            {/if}
            <div class="row">
              <button class="secondary" disabled={botBusy} onclick={createBot}>{botBusy ? 'Bitte warten …' : 'Neuen Bot anlegen'}</button>
            </div>
            <form class="token" onsubmit={addBot}>
              <label>Oder Token eines vorhandenen Bots<input bind:value={botToken} required placeholder="123456789:ABC…" autocomplete="off" spellcheck="false" /></label>
              <button class="secondary" disabled={botBusy}>Hinzufügen</button>
            </form>
          </section>
        {/if}

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

        {#if info.webdavPort}
          <section>
            <h3>WebDAV</h3>
            <p>{info.webdavEnabled ? `Aktiv, Benutzer „${info.username}“, Port ${info.webdavPort}, nur verschlüsselt (HTTPS).` : 'Aus, der Port ist geschlossen. Mit einem Kennwort wird WebDAV eingeschaltet.'}</p>
            <p>WebDAV geht nicht über den Cloudflare-Tunnel. Verbinde dich direkt mit dem Server, über seine IP oder einen Namen, der auf ihn zeigt, zum Beispiel {davUrl || 'https://server:8443/'}</p>
            {#if info.webdavCert}
              <div class="cert">
                <p>{info.webdavCert.own ? 'Eigenes Zertifikat' : 'Selbst erstelltes Zertifikat'} für {info.webdavCert.hosts.join(', ')}, gültig bis {new Date(info.webdavCert.notAfter * 1000).toLocaleDateString('de-DE')}.{info.webdavCert.own ? '' : ' Damit Windows, macOS oder dein Programm ihm vertraut, importiere es einmal als vertrauenswürdige Stammzertifizierungsstelle.'}</p>
                <p>Fingerabdruck (SHA-256):</p>
                <code>{info.webdavCert.fingerprint}</code>
                {#if !info.webdavCert.own}<a class="button secondary" href="/api/settings/webdav/cert" download>Zertifikat herunterladen</a>{/if}
              </div>
            {:else if info.webdavError}
              <p class="err">Zertifikat: {info.webdavError}</p>
            {/if}
            <form onsubmit={saveDav}>
              <label>Aktuelles Web-Kennwort<input type="password" bind:value={davCurrent} required autocomplete="current-password" /></label>
              <label>{info.webdavEnabled ? 'Neues WebDAV-Kennwort' : 'WebDAV-Kennwort'}<input type="password" bind:value={davPassword} required autocomplete="new-password" /></label>
              <div class="row">
                <button class="secondary">{info.webdavEnabled ? 'Kennwort ändern' : 'WebDAV einschalten'}</button>
                {#if info.webdavEnabled}<button type="button" class="secondary" disabled={!davCurrent} onclick={disableDav}>WebDAV abschalten</button>{/if}
              </div>
            </form>
          </section>
        {/if}
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
  .cert {
    margin-bottom: 12px;
  }
  a.button {
    display: inline-flex;
    align-items: center;
    height: 32px;
    padding: 0 14px;
    text-decoration: none;
    color: inherit;
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
  .bots {
    list-style: none;
    margin: 0 0 10px;
    padding: 0;
  }
  .bots li {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 4px 0;
  }
  .bots .name {
    font-weight: 600;
  }
  .bots .state {
    flex: 1;
    min-width: 0;
    color: var(--muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .bots .state.error {
    color: var(--danger);
  }
  .bots .src {
    color: var(--muted);
    font-size: 12px;
  }
  .token {
    margin-top: 12px;
  }
  .err {
    color: var(--danger);
  }
</style>
