<script>
  import { post } from './api.js'

  let { state: app, onchange } = $props()

  let f = $state({ setupCode: '', username: '', password: '', passphrase: '', passphrase2: '', code: '', phone: '', tgCode: '', tgPassword: '' })
  let error = $state('')
  let busy = $state(false)

  const step = $derived.by(() => {
    if (app.needsSetup) return 'setup'
    if (!app.loggedIn) return 'login'
    if (app.locked) return 'unlock'
    return app.telegram?.step ?? 'connecting'
  })

  async function submit(e) {
    e.preventDefault()
    error = ''
    busy = true
    try {
      if (step === 'setup') {
        if (f.passphrase !== f.passphrase2) throw new Error('Die Passphrasen stimmen nicht überein')
        await post('/setup', { setupCode: f.setupCode.trim(), username: f.username, password: f.password, passphrase: f.passphrase })
      } else if (step === 'login') {
        await post('/login', { username: f.username, password: f.password, code: f.code.trim() })
      } else if (step === 'unlock') {
        await post('/unlock', { passphrase: f.passphrase })
      } else if (step === 'phone') {
        await post('/telegram/phone', { phone: f.phone })
      } else if (step === 'code') {
        await post('/telegram/code', { code: f.tgCode })
      } else if (step === 'password') {
        await post('/telegram/password', { password: f.tgPassword })
      }
      f.password = f.passphrase = f.passphrase2 = f.tgPassword = ''
      await onchange()
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }

  const titles = {
    setup: ['Einrichtung', 'Lege dein Konto und die Passphrase für die Verschlüsselung an.'],
    login: ['Anmelden', 'Telegram Explorer'],
    unlock: ['Tresor entsperren', 'Nach einem Neustart wird die Passphrase einmal benötigt.'],
    phone: ['Mit Telegram verbinden', 'Telefonnummer mit Ländervorwahl, zum Beispiel +49 170 1234567.'],
    code: ['Code eingeben', 'Telegram hat dir einen Code geschickt.'],
    password: ['Zweistufige Bestätigung', 'Dein Telegram-Passwort, nicht das dieser Anwendung.'],
    connecting: ['Verbinde mit Telegram', ''],
  }
</script>

<main class="gate">
  <form onsubmit={submit}>
    <h1>{titles[step][0]}</h1>
    <p>{titles[step][1]}</p>

    {#if step === 'setup'}
      <label>Einrichtungscode <small>(steht im Container-Log)</small><input bind:value={f.setupCode} required autocomplete="off" /></label>
      <label>Benutzername<input bind:value={f.username} required autocomplete="username" /></label>
      <label>Kennwort<input type="password" bind:value={f.password} required minlength="8" autocomplete="new-password" /></label>
      <label>Passphrase für die Verschlüsselung<input type="password" bind:value={f.passphrase} required minlength="10" autocomplete="off" /></label>
      <label>Passphrase wiederholen<input type="password" bind:value={f.passphrase2} required autocomplete="off" /></label>
      <p class="warn">Ohne diese Passphrase sind verschlüsselte Dateien für immer verloren. Niemand kann sie zurücksetzen.</p>
    {:else if step === 'login'}
      <label>Benutzername<input bind:value={f.username} required autocomplete="username" /></label>
      <label>Kennwort<input type="password" bind:value={f.password} required autocomplete="current-password" /></label>
      <label>2FA-Code <small>(falls aktiviert)</small><input bind:value={f.code} inputmode="numeric" autocomplete="one-time-code" /></label>
    {:else if step === 'unlock'}
      <label>Passphrase<input type="password" bind:value={f.passphrase} required autocomplete="off" /></label>
    {:else if step === 'phone'}
      <label>Telefonnummer<input type="tel" bind:value={f.phone} required autocomplete="tel" /></label>
    {:else if step === 'code'}
      <label>Code<input bind:value={f.tgCode} required inputmode="numeric" autocomplete="one-time-code" /></label>
    {:else if step === 'password'}
      <label>Telegram-Passwort<input type="password" bind:value={f.tgPassword} required autocomplete="off" /></label>
    {:else}
      <p class="wait">Einen Moment bitte.{app.telegram?.error ? ` Letzter Fehler: ${app.telegram.error}` : ''}</p>
    {/if}

    {#if error}<p class="err" role="alert">{error}</p>{/if}
    {#if step !== 'connecting'}
      <button class="primary" disabled={busy}>{busy ? 'Bitte warten' : step === 'setup' ? 'Einrichten' : 'Weiter'}</button>
    {/if}
  </form>
</main>

<style>
  .gate {
    min-height: 100vh;
    display: grid;
    place-items: center;
    padding: 16px;
  }
  form {
    width: min(380px, 100%);
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 28px;
    box-shadow: var(--shadow-lg);
  }
  h1 {
    margin: 0 0 4px;
    font-size: 20px;
    font-weight: 600;
  }
  p {
    margin: 0 0 18px;
    color: var(--muted);
  }
  label {
    display: block;
    margin-bottom: 12px;
    font-weight: 500;
  }
  small {
    color: var(--muted);
    font-weight: 400;
  }
  input {
    margin-top: 4px;
    width: 100%;
  }
  .primary {
    width: 100%;
    height: 34px;
  }
  .warn {
    color: var(--warning-text);
  }
  .err {
    color: var(--danger);
  }
</style>
