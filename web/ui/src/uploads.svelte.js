import { api, ApiError, post } from './api.js'

const CHUNK = 8 << 20
const PARALLEL = 2
const RETRY_LIMIT = 8

export const uploads = $state({ items: [], onDone: null, resolveConflict: null })

function resumeKey(item) {
  const f = item.file
  return `telex-upload:${item.parentId}:${item.name}:${f.size}:${f.lastModified}`
}

function storage(fn) {
  try {
    return fn(localStorage)
  } catch {
    return null
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function sendChunks(item, id, start) {
  let offset = start
  let failures = 0
  while (true) {
    if (item.state === 'canceled') return
    const end = Math.min(offset + CHUNK, item.file.size)
    try {
      const res = await api(`/uploads/${id}`, {
        method: 'PATCH',
        raw: true,
        body: item.file.slice(offset, end),
        headers: { 'X-Upload-Offset': String(offset) },
      })
      failures = 0
      offset = res.received
      item.sent = offset
      item.error = ''
      if (res.node) return
    } catch (err) {
      // Network trouble is expected on large uploads: ask where the server
      // stands and carry on from there.
      if (++failures > RETRY_LIMIT || (err.status && err.status !== 409 && err.status < 500)) throw err
      item.error = `Verbindung unterbrochen, neuer Versuch ${failures}/${RETRY_LIMIT}`
      await sleep(Math.min(1000 * 2 ** failures, 30000))
      const status = await api(`/uploads/${id}`).catch(() => null)
      if (status) offset = status.received
    }
  }
}

async function run(item) {
  item.state = 'running'
  const key = resumeKey(item)
  try {
    let id = storage((s) => s.getItem(key))
    let start = 0
    if (id) {
      const status = await api(`/uploads/${id}`).catch(() => null)
      if (status) start = status.received
      else id = null
    }
    if (!id) {
      const body = {
        parentId: item.parentId,
        name: item.name,
        size: item.file.size,
        encrypted: item.encrypted,
        overwrite: item.overwrite,
        mtime: Math.floor(item.file.lastModified / 1000),
      }
      try {
        id = (await post('/uploads', body)).id
      } catch (err) {
        if (!(err instanceof ApiError) || err.status !== 409) throw err
        const choice = uploads.resolveConflict ? await uploads.resolveConflict(item.name) : 'skip'
        if (choice !== 'replace') {
          item.state = 'skipped'
          return
        }
        id = (await post('/uploads', { ...body, overwrite: true })).id
      }
      storage((s) => s.setItem(key, id))
    }
    item.id = id
    item.sent = start
    await sendChunks(item, id, start)
    storage((s) => s.removeItem(key))
    if (item.state !== 'canceled') item.state = 'done'
    uploads.onDone?.(item.parentId)
  } catch (err) {
    item.state = 'error'
    item.error = err.message
  }
}

function pump() {
  const running = uploads.items.filter((i) => i.state === 'running').length
  const free = PARALLEL - running
  const next = uploads.items.filter((i) => i.state === 'queued').slice(0, free)
  for (const item of next) {
    run(item).finally(pump)
  }
}

export function enqueue(file, parentId, encrypted, name = file.name) {
  uploads.items.push({
    key: crypto.randomUUID(),
    file,
    parentId,
    name,
    encrypted,
    overwrite: false,
    sent: 0,
    state: 'queued',
    error: '',
    id: null,
  })
  pump()
}

export async function cancel(item) {
  const wasRunning = item.state === 'running'
  item.state = 'canceled'
  storage((s) => s.removeItem(resumeKey(item)))
  if (wasRunning && item.id) await api(`/uploads/${item.id}`, { method: 'DELETE' }).catch(() => {})
}

export function retry(item) {
  item.state = 'queued'
  item.error = ''
  pump()
}

export function clearFinished() {
  uploads.items = uploads.items.filter((i) => ['queued', 'running', 'error'].includes(i.state))
}

async function ensureFolder(parentId, name) {
  try {
    return (await post(`/nodes/${parentId}/mkdir`, { name })).id
  } catch (err) {
    if (err.status !== 409) throw err
    const list = await api(`/nodes/${parentId}/children`)
    const existing = list.find((n) => n.name === name && n.isDir)
    if (!existing) throw err
    return existing.id
  }
}

async function readAll(reader) {
  const out = []
  while (true) {
    const batch = await new Promise((res, rej) => reader.readEntries(res, rej))
    if (batch.length === 0) return out
    out.push(...batch)
  }
}

async function addEntry(entry, parentId, encrypted) {
  if (entry.isFile) {
    const file = await new Promise((res, rej) => entry.file(res, rej))
    enqueue(file, parentId, encrypted)
    return
  }
  const id = await ensureFolder(parentId, entry.name)
  for (const child of await readAll(entry.createReader())) {
    await addEntry(child, id, encrypted)
  }
}

// addDropped handles files and whole folders dropped from the desktop.
export async function addDropped(dataTransfer, parentId, encrypted) {
  const entries = [...dataTransfer.items].map((i) => i.webkitGetAsEntry?.()).filter(Boolean)
  if (entries.length === 0) {
    for (const f of dataTransfer.files) enqueue(f, parentId, encrypted)
    return
  }
  for (const entry of entries) await addEntry(entry, parentId, encrypted)
}

// addFolderInput handles the result of <input webkitdirectory>.
export async function addFolderInput(fileList, parentId, encrypted) {
  const folders = new Map()
  for (const file of fileList) {
    const parts = file.webkitRelativePath.split('/')
    const name = parts.pop()
    let id = parentId
    let path = ''
    for (const part of parts) {
      path += '/' + part
      if (!folders.has(path)) folders.set(path, await ensureFolder(id, part))
      id = folders.get(path)
    }
    enqueue(file, id, encrypted, name)
  }
}
