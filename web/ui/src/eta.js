// Tracks transfer speed from progress samples and turns it into a remaining
// time. A short smoothing window keeps the number from jumping with every chunk.
const SMOOTHING = 0.3
const tracks = new Map()

export function track(key, done, total) {
  const now = performance.now()
  let t = tracks.get(key)
  if (!t || done < t.done) {
    t = { at: now, done, speed: 0 }
    tracks.set(key, t)
  }
  const dt = (now - t.at) / 1000
  if (dt >= 1 && done !== t.done) {
    const current = (done - t.done) / dt
    t.speed = t.speed ? t.speed * (1 - SMOOTHING) + current * SMOOTHING : current
    t.at = now
    t.done = done
  } else if (dt >= 15) {
    // No progress for a while: a stale speed would promise too much.
    t.speed = 0
    t.at = now
  }
  return { speed: t.speed, seconds: t.speed > 0 ? (total - done) / t.speed : null }
}

export function forget(key) {
  tracks.delete(key)
}

export function formatDuration(seconds) {
  if (seconds === null || !isFinite(seconds)) return 'Restzeit wird berechnet'
  if (seconds < 60) return 'noch unter 1 Min.'
  const minutes = Math.ceil(seconds / 60)
  if (minutes < 60) return `noch ca. ${minutes} Min.`
  const hours = Math.floor(minutes / 60)
  return `noch ca. ${hours} Std. ${minutes % 60} Min.`
}
