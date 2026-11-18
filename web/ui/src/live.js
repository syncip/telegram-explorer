// One event stream from /api/events for the whole UI, instead of polling.
// Without it (a proxy that buffers, an old browser) callers fall back to
// polling: live() tells them whether the stream is working.

const NAMES = ['state', 'files', 'stats', 'jobs', 'verify', 'ping', 'logout']
const listeners = new Map()
let source = null
let wanted = false
let retry = 0
let lastEvent = 0

function emit(name, data) {
  for (const fn of listeners.get(name) ?? []) fn(data)
}

function open() {
  clearTimeout(retry)
  if (source || !wanted || typeof EventSource === 'undefined') return
  source = new EventSource('/api/events')
  for (const name of NAMES) {
    source.addEventListener(name, (e) => {
      lastEvent = Date.now()
      let data = null
      try {
        data = JSON.parse(e.data)
      } catch {}
      emit(name, data)
    })
  }
  source.onopen = () => emit('open')
  source.onerror = () => {
    // The browser retries by itself while the stream is merely interrupted.
    if (source?.readyState !== EventSource.CLOSED) return
    source = null
    lastEvent = 0
    emit('closed')
    retry = setTimeout(open, 5000)
  }
}

export function startLive() {
  wanted = true
  open()
}

export function stopLive() {
  wanted = false
  clearTimeout(retry)
  source?.close()
  source = null
  lastEvent = 0
}

// live reports whether events arrived recently; the server pings every 20 s.
export const live = () => Date.now() - lastEvent < 30000

export function on(name, fn) {
  if (!listeners.has(name)) listeners.set(name, new Set())
  listeners.get(name).add(fn)
  return () => listeners.get(name).delete(fn)
}
