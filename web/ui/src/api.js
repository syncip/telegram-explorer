export const ROOT = 1

export class ApiError extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

export async function api(path, { method = 'GET', body, headers, raw } = {}) {
  const res = await fetch('/api' + path, {
    method,
    headers: {
      'X-Telex': '1',
      ...(body !== undefined && !raw ? { 'Content-Type': 'application/json' } : {}),
      ...headers,
    },
    body: raw ? body : body !== undefined ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  let data = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = null
  }
  if (!res.ok) {
    const err = new ApiError(data?.error ?? `Fehler ${res.status}`, res.status)
    err.headers = res.headers
    throw err
  }
  return data
}

export const post = (path, body = {}) => api(path, { method: 'POST', body })

export function formatSize(n) {
  if (n === undefined || n === null) return ''
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${i === 0 ? v : v.toLocaleString('de-DE', { maximumFractionDigits: 1 })} ${units[i]}`
}

export function formatDate(unix) {
  if (!unix) return ''
  return new Date(unix * 1000).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' })
}

const KINDS = [
  ['image', /\.(png|jpe?g|gif|webp|bmp|svg|heic|avif)$/i],
  ['video', /\.(mp4|mkv|avi|mov|webm|m4v|wmv)$/i],
  ['audio', /\.(mp3|flac|wav|ogg|m4a|aac|opus)$/i],
  ['archive', /\.(zip|rar|7z|tar|gz|bz2|xz|iso)$/i],
  ['pdf', /\.pdf$/i],
  ['doc', /\.(docx?|odt|rtf|txt|md|xlsx?|ods|csv|pptx?|odp)$/i],
  ['code', /\.(js|ts|go|py|c|cpp|h|cs|java|rs|html?|css|json|ya?ml|sh|ps1|xml|sql)$/i],
]

export function kindOf(node) {
  if (node.isDir) return 'folder'
  return KINDS.find(([, re]) => re.test(node.name))?.[0] ?? 'file'
}

export function typeLabel(node) {
  if (node.isDir) return 'Dateiordner'
  const labels = { image: 'Bild', video: 'Video', audio: 'Audio', archive: 'Archiv', pdf: 'PDF-Dokument', doc: 'Dokument', code: 'Quelltext' }
  const label = labels[kindOf(node)]
  if (label) return label
  const ext = node.name.includes('.') ? node.name.split('.').pop().toUpperCase() : ''
  return ext ? `${ext}-Datei` : 'Datei'
}
