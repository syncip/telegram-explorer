// The Android app injects window.TelexBridge on the server's origin. In a normal
// browser it is missing and everything here does nothing.
const bridge = typeof window !== 'undefined' ? window.TelexBridge : undefined

export const isApp = !!bridge

export function send(msg) {
  try {
    bridge?.postMessage(JSON.stringify(msg))
  } catch {
    // The app is gone or older, the web interface works without it.
  }
}

export function onMessage(fn) {
  if (!bridge) return () => {}
  const handler = (e) => {
    try {
      fn(JSON.parse(e.data))
    } catch {
      // Not ours.
    }
  }
  bridge.addEventListener('message', handler)
  return () => bridge.removeEventListener('message', handler)
}

// The app's back button asks this first; true means it was handled here.
export function onBack(fn) {
  window.__telexBack = fn
  return () => {
    if (window.__telexBack === fn) window.__telexBack = null
  }
}

// Android's WebView hands over the files of a picked folder without their paths, the
// app sends them separately in the same order. The name check guards against a
// mismatch, in which case the file lands in the target folder itself.
export function folderPath(file, index) {
  const p = window.__telexRelativePaths?.[index]
  return p && p.split('/').pop() === file.name ? p : ''
}

export function clearFolderPaths() {
  window.__telexRelativePaths = null
}
