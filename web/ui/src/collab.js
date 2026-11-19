// Live editing against /api/live/{id}: CodeMirror's collab extension keeps
// track of what this client changed, the server orders all changes and hands
// them out through long polls. Peers' cursors come along with each pull.
import { collab, receiveUpdates, sendableUpdates, getSyncedVersion } from '@codemirror/collab'
import { ChangeSet, StateEffect, StateField, Text } from '@codemirror/state'
import { Decoration, EditorView, WidgetType } from '@codemirror/view'

const COLORS = ['#e8590c', '#2f9e44', '#1971c2', '#c2255c', '#7048e8', '#0c8599', '#e67700']
const color = (id) => COLORS[[...id].reduce((h, c) => (h * 31 + c.charCodeAt(0)) >>> 0, 7) % COLORS.length]

export class LiveError extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

async function call(path, { method = 'GET', body, keepalive } = {}) {
  const res = await fetch('/api/live' + path, {
    method,
    keepalive,
    headers: { 'X-Telex': '1', ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 204) return null
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new LiveError(data?.error ?? `Fehler ${res.status}`, res.status)
  return data
}

// Peers' cursors and selections -------------------------------------------

const setPeers = StateEffect.define()

class Caret extends WidgetType {
  constructor(name, color) {
    super()
    this.name = name
    this.color = color
  }
  eq(other) {
    return other.name === this.name && other.color === this.color
  }
  toDOM() {
    const el = document.createElement('span')
    el.className = 'cm-peer-caret'
    el.style.borderColor = this.color
    const label = document.createElement('span')
    label.className = 'cm-peer-name'
    label.style.background = this.color
    label.textContent = this.name
    el.append(label)
    return el
  }
  ignoreEvent() {
    return true
  }
}

const peersField = StateField.define({
  create: () => Decoration.none,
  update(deco, tr) {
    deco = deco.map(tr.changes)
    for (const e of tr.effects) {
      if (!e.is(setPeers)) continue
      const size = tr.state.doc.length
      const ranges = []
      for (const p of e.value) {
        const c = color(p.id)
        const from = Math.min(p.anchor, p.head, size)
        const to = Math.min(Math.max(p.anchor, p.head), size)
        if (from < to) ranges.push(Decoration.mark({ attributes: { style: `background: ${c}33` } }).range(from, to))
        ranges.push(Decoration.widget({ widget: new Caret(p.name || 'Gerät', c), side: 1 }).range(Math.min(p.head, size)))
      }
      deco = Decoration.set(ranges, true)
    }
    return deco
  },
  provide: (f) => EditorView.decorations.from(f),
})

const peersTheme = EditorView.baseTheme({
  '.cm-peer-caret': { position: 'relative', borderLeft: '2px solid', marginLeft: '-1px', marginRight: '-1px' },
  '.cm-peer-name': {
    position: 'absolute',
    top: '-1.35em',
    left: '-2px',
    padding: '0 4px',
    borderRadius: '3px 3px 3px 0',
    color: '#fff',
    fontSize: '11px',
    lineHeight: '1.35em',
    whiteSpace: 'nowrap',
    pointerEvents: 'none',
    fontFamily: 'system-ui, sans-serif',
    opacity: '0.9',
  },
})

// The session -------------------------------------------------------------

/**
 * Joins the live session of a file. make(doc, extensions) builds the editor
 * state; the session adds its own extensions and drives the view returned by
 * attach(). onstatus gets {online, pending, savedAt, problem, gone, peers, node}.
 */
export async function joinLive(nodeId, { name, onstatus, onconflict }) {
  const clientID = crypto.randomUUID?.() ?? Math.random().toString(36).slice(2) + Date.now().toString(36)
  let id = nodeId
  let info = await call(`/${id}/join`, { method: 'POST', body: { clientID, name } })
  let epoch = info.epoch
  // The text as of the synced version, to compare after a session restart.
  let confirmed = Text.of(info.doc.split('\n'))
  let view = null
  let rev = -1
  let closed = false
  let online = true
  let pushing = false
  let presenceTimer = 0
  let status = { online: true }

  const report = (patch) => {
    status = { ...status, ...patch }
    status.pending = status.serverPending || (view ? sendableUpdates(view.state).length > 0 : false)
    keepUnsent()
    onstatus?.(status)
  }

  // Changes the server does not have yet also go to this device's storage, so
  // closing the page, a lock or a restart of the server loses nothing.
  const unsentKey = () => `telex-unsent:${info.node.parentId}/${info.node.name}`
  let unsentTimer = 0
  function keepUnsent() {
    clearTimeout(unsentTimer)
    unsentTimer = setTimeout(() => {
      if (!view) return
      try {
        if (sendableUpdates(view.state).length) {
          localStorage.setItem(unsentKey(), JSON.stringify({ base: confirmed.toString(), text: view.state.doc.toString() }))
        } else localStorage.removeItem(unsentKey())
      } catch {}
    }, 200)
  }

  // Brings back what a previous visit could not send: as local edits when the
  // server still has the text they were made on, else through onconflict.
  function restoreUnsent() {
    let saved = null
    try {
      saved = JSON.parse(localStorage.getItem(unsentKey()) ?? 'null')
    } catch {}
    if (!saved || saved.text === view.state.doc.toString()) return
    if (saved.base === view.state.doc.toString()) {
      const a = saved.base
      const b = saved.text
      let start = 0
      while (start < a.length && start < b.length && a[start] === b[start]) start++
      let end = 0
      while (end < a.length - start && end < b.length - start && a[a.length - 1 - end] === b[b.length - 1 - end]) end++
      view.dispatch({ changes: { from: start, to: a.length - end, insert: b.slice(start, b.length - end) } })
    } else {
      // The dialog holds the text now; taking it makes it unsent again.
      try {
        localStorage.removeItem(unsentKey())
      } catch {}
      onconflict?.(saved.text)
    }
  }

  const extensions = (version) => [collab({ startVersion: version, clientID }), peersField, peersTheme]

  function applyState(st) {
    if (!st) return
    rev = st.rev
    if (st.node) id = st.node.id
    // Positions arrive for the synced version; local changes not sent yet move them on.
    const pending = sendableUpdates(view.state)
    const peers = st.peers.map((p) => {
      let { anchor, head } = p
      for (const u of pending) {
        anchor = u.changes.mapPos(anchor)
        head = u.changes.mapPos(head)
      }
      return { ...p, anchor, head }
    })
    view.dispatch({ effects: setPeers.of(peers) })
    report({ serverPending: st.pending, savedAt: st.savedAt, problem: st.problem, gone: st.gone, peers: st.peers, node: st.node })
  }

  async function push() {
    if (closed || pushing || !view) return
    const updates = sendableUpdates(view.state)
    if (!updates.length) return
    pushing = true
    try {
      await call(`/${id}/push`, {
        method: 'POST',
        body: { epoch, clientID, version: getSyncedVersion(view.state), updates: updates.map((u) => u.changes.toJSON()) },
      })
      // Accepted or not, the next pull brings the server's order.
    } catch (err) {
      handleError(err)
    } finally {
      pushing = false
    }
    report({})
  }

  async function pullLoop() {
    let wait = 500
    while (!closed) {
      try {
        const version = getSyncedVersion(view.state)
        const q = new URLSearchParams({ epoch, clientID, name, version, rev })
        const res = await call(`/${id}/pull?${q}`)
        if (closed) return
        if (!online) {
          online = true
          report({ online })
        }
        wait = 500
        if (res.updates.length) {
          const updates = res.updates.map((u) => ({ changes: ChangeSet.fromJSON(u.changes), clientID: u.clientID }))
          for (const u of updates) confirmed = u.changes.apply(confirmed)
          view.dispatch(receiveUpdates(view.state, updates))
        }
        applyState(res.state)
        if (sendableUpdates(view.state).length) push()
      } catch (err) {
        if (closed) return
        if (err instanceof LiveError && err.status === 410) {
          await rejoin()
          continue
        }
        if (!(err instanceof LiveError) || err.status >= 500) {
          if (online) {
            online = false
            report({ online })
          }
          await new Promise((r) => setTimeout(r, wait))
          wait = Math.min(wait * 2, 10000)
          continue
        }
        report({ problem: err.message })
        await new Promise((r) => setTimeout(r, 5000))
      }
    }
  }

  function handleError(err) {
    if (err instanceof LiveError && err.status === 410) return // the pull loop rejoins
    if (!(err instanceof LiveError) || err.status >= 500) report({ online: (online = false) })
    else report({ problem: err.message })
  }

  // After the server restarted or ended the session: start a new one and
  // carry over what was typed here in the meantime.
  async function rejoin() {
    const pending = sendableUpdates(view.state)
    const mine = view.state.doc.toString()
    info = await call(`/${status.node?.id ?? id}/join`, { method: 'POST', body: { clientID, name } })
    epoch = info.epoch
    id = info.node.id
    rev = -1
    const sel = view.state.selection
    const fresh = Text.of(info.doc.split('\n'))
    let state = makeState(info.doc, info.version)
    if (pending.length && fresh.eq(confirmed)) {
      let changes = pending[0].changes
      for (const u of pending.slice(1)) changes = changes.compose(u.changes)
      state = state.update({ changes, selection: sel }).state
    } else if (pending.length) {
      onconflict?.(mine)
    }
    confirmed = fresh
    view.setState(state)
    report({ online: true })
  }

  let makeState = null

  function sendPresence() {
    clearTimeout(presenceTimer)
    presenceTimer = setTimeout(() => {
      if (closed || !view) return
      // The server knows the synced version; local changes are undone for it.
      let { anchor, head } = view.state.selection.main
      for (const u of sendableUpdates(view.state).reverse()) {
        anchor = u.changes.invertedDesc.mapPos(anchor)
        head = u.changes.invertedDesc.mapPos(head)
      }
      call(`/${id}/presence`, {
        method: 'POST',
        body: { epoch, clientID, name, version: getSyncedVersion(view.state), anchor, head },
      }).catch(() => {})
    }, 150)
  }

  return {
    doc: info.doc,
    node: info.node,
    clientID,
    extensions: () => extensions(info.version),
    // Called with the view once it exists, and with a function that builds a
    // fresh state for a doc and version, for a rejoin.
    attach(v, make) {
      view = v
      makeState = (doc, version) => make(doc, extensions(version))
      applyState(info.state)
      restoreUnsent()
      pullLoop()
    },
    // For the update listener of the view.
    changed(update) {
      if (update.docChanged && sendableUpdates(update.state).length) push()
      if (update.selectionSet || update.docChanged) sendPresence()
      report({})
    },
    get pending() {
      return view ? sendableUpdates(view.state).length > 0 : false
    },
    async flush() {
      await push()
    },
    leave() {
      if (closed) return
      closed = true
      clearTimeout(presenceTimer)
      call(`/${id}/leave`, { method: 'POST', body: { epoch, clientID }, keepalive: true }).catch(() => {})
    },
    // Replaces the shared text with this client's version (after a conflict).
    replaceWith(text) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } })
    },
  }
}
