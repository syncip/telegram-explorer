import { api, ApiError, post } from './api.js'

const CHUNK = 8 << 20

let nextKey = 1
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

// fetch cannot report upload progress, and a bar that only moves once per chunk
// looks stuck on a slow line.
function putChunk(id, body, offset, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PATCH', `/api/uploads/${id}`)
    xhr.setRequestHeader('X-Telex', '1')
    xhr.setRequestHeader('X-Upload-Offset', String(offset))
    xhr.upload.onprogress = (e) => onProgress(e.loaded)
    xhr.onerror = () => reject(new Error('Netzwerkfehler'))
    xhr.onload = () => {
      let data = null
      try {
        data = JSON.parse(xhr.responseText)
      } catch {}
      if (xhr.status >= 200 && xhr.status < 300) resolve(data)
      else reject(new ApiError(data?.error ?? `Fehler ${xhr.status}`, xhr.status))
    }
    xhr.send(body)
  })
}

async function sendChunks(item, id, start) {
  let offset = start
  let failures = 0
  while (true) {
    if (item.state === 'canceled') return
    const end = Math.min(offset + CHUNK, item.file.size)
    try {
      const from = offset
      const res = await putChunk(id, item.file.slice(from, end), from, (loaded) => (item.sent = Math.min(from + loaded, end)))
      failures = 0
      offset = res.received
      item.sent = offset
      item.error = ''
      if (res.node) return res.node
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
    const node = await sendChunks(item, id, start)
    storage((s) => s.removeItem(key))
    if (item.state === 'canceled') return
    item.nodeId = node.id
    item.state = 'server'
    uploads.onDone?.()
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
    key: nextKey++,
    file,
    parentId,
    name,
    size: file.size,
    encrypted,
    overwrite: false,
    sent: 0,
    state: 'queued',
    error: '',
    id: null,
    nodeId: null,
    job: null,
  })
  pump()
}

// syncJobs follows files on their way from the server to Telegram. Files from
// SFTP or an earlier session show up here without having been uploaded on this page.
export async function syncJobs(jobs) {
  for (const job of jobs) {
    const item = uploads.items.find((i) => i.nodeId === job.id)
    if (item) {
      item.job = job
    } else if (!uploads.items.some((i) => i.state === 'running' && i.parentId === job.parentId && i.name === job.name)) {
      uploads.items.push({
        key: nextKey++,
        file: null,
        parentId: job.parentId,
        name: job.name,
        size: job.total,
        sent: job.total,
        state: 'server',
        error: '',
        id: null,
        nodeId: job.id,
        job,
      })
    }
  }
  const ids = new Set(jobs.map((j) => j.id))
  for (const item of uploads.items.filter((i) => i.state === 'server' && !ids.has(i.nodeId))) {
    // Not listed can also mean this list is older than the upload, so ask.
    try {
      const { node } = await api(`/nodes/${item.nodeId}`)
      if (node.state !== 'ready') continue
      item.state = 'finished'
      item.job = null
      uploads.onDone?.()
    } catch (err) {
      if (err.status === 404) item.state = 'canceled'
    }
  }
}

// resume continues an upload the server still holds. The browser cannot keep a
// file across sessions, so the user has to pick it again.
export function resume(file, upload) {
  if (file.size !== upload.size || Math.floor(file.lastModified / 1000) !== upload.mtime) {
    throw new Error(`„${file.name}“ ist nicht die Datei, deren Upload unterbrochen wurde`)
  }
  storage((s) => s.setItem(resumeKey({ file, parentId: upload.parentId, name: upload.name }), upload.id))
  enqueue(file, upload.parentId, upload.encrypted, upload.name)
}

export async function cancel(item) {
  item.state = 'canceled'
  storage((s) => s.removeItem(resumeKey(item)))
  if (item.id) await api(`/uploads/${item.id}`, { method: 'DELETE' }).catch(() => {})
}

export function retry(item) {
  item.state = 'queued'
  item.error = ''
  pump()
}

export function clearFinished() {
  uploads.items = uploads.items.filter((i) => ['queued', 'running', 'error', 'server'].includes(i.state))
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
