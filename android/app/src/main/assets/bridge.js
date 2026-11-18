// Runs at document start on the server's origin only (addDocumentStartJavaScript).
// It watches the web interface's own API calls so the app can offer to keep the login
// and the vault passphrase behind the fingerprint, and it lets the app log in and
// unlock with stored values. The secrets go only to the native app, never elsewhere.
(() => {
  if (window.__telex || !window.TelexBridge) return
  const bridge = window.TelexBridge
  const send = (msg) => {
    try {
      bridge.postMessage(JSON.stringify(msg))
    } catch (e) {}
  }
  const nativeFetch = window.fetch.bind(window)
  const WATCH = new Set([
    '/api/setup',
    '/api/login',
    '/api/logout',
    '/api/unlock',
    '/api/settings/password',
    '/api/settings/passphrase',
    '/api/settings/totp/confirm',
    '/api/settings/totp/disable',
  ])

  const pathOf = (input) => {
    try {
      return new URL(typeof input === 'string' ? input : input.url, location.href).pathname
    } catch (e) {
      return ''
    }
  }

  window.fetch = async (input, init = {}) => {
    const path = pathOf(input)
    const method = String(init.method || (input && input.method) || 'GET').toUpperCase()
    let body = null
    if (method === 'POST' && WATCH.has(path)) {
      try {
        body = typeof init.body === 'string' ? JSON.parse(init.body) : {}
      } catch (e) {
        body = {}
      }
    }
    const res = await nativeFetch(input, init)
    try {
      if (path === '/api/state' && res.ok) {
        res
          .clone()
          .json()
          .then((s) => send({ type: 'state', needsSetup: !!s.needsSetup, loggedIn: !!s.loggedIn, locked: !!s.locked }))
          .catch(() => {})
      } else if (body && res.ok) {
        switch (path) {
          case '/api/setup':
            send({ type: 'setup', username: body.username, password: body.password, passphrase: body.passphrase })
            break
          case '/api/login':
            send({ type: 'login', username: body.username, password: body.password, totp: !!String(body.code || '').trim() })
            break
          case '/api/unlock':
            send({ type: 'unlock', passphrase: body.passphrase })
            break
          case '/api/settings/password':
            send({ type: 'changed', field: 'password', value: body.password })
            break
          case '/api/settings/passphrase':
            send({ type: 'changed', field: 'passphrase', value: body.passphrase })
            break
          case '/api/settings/totp/confirm':
            send({ type: 'totp', on: true })
            break
          case '/api/settings/totp/disable':
            send({ type: 'totp', on: false })
            break
          case '/api/logout':
            send({ type: 'logout' })
            break
        }
      }
    } catch (e) {}
    return res
  }

  const post = (path, body) =>
    nativeFetch('/api' + path, {
      method: 'POST',
      headers: { 'X-Telex': '1', 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })

  // Svelte's bind:value listens for input events, a plain value assignment is not enough.
  const fill = (selector, value) => {
    const el = document.querySelector(selector)
    if (!el) return null
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
    setter.call(el, value)
    el.dispatchEvent(new Event('input', { bubbles: true }))
    return el
  }

  window.__telex = {
    // Logs in, then unlocks the vault when it is locked and a passphrase came along.
    async login(username, password, passphrase) {
      const res = await post('/login', { username, password, code: '' })
      if (!res.ok) {
        send({ type: 'auto', step: 'login', status: res.status })
        return
      }
      if (passphrase) {
        const state = await (await nativeFetch('/api/state')).json()
        if (state.locked) {
          await this.unlock(passphrase, false)
          location.reload()
          return
        }
      }
      send({ type: 'auto', step: 'done', status: 200 })
      location.reload()
    },

    async unlock(passphrase, reload = true) {
      const res = await post('/unlock', { passphrase })
      send({ type: 'auto', step: res.ok ? 'done' : 'unlock', status: res.status })
      if (reload) location.reload()
    },

    // With 2FA the code still has to be typed, so only the known fields are filled.
    fillLogin(username, password) {
      fill('input[autocomplete="username"]', username)
      fill('input[autocomplete="current-password"]', password)
      const code = document.querySelector('input[autocomplete="one-time-code"]')
      if (code) code.focus()
      return !!code
    },

    // Relative paths for the files of a folder picked in the app, in the same order.
    setFolderPaths(paths) {
      window.__telexRelativePaths = paths
    },
  }
})()
