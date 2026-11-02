// The theme is a choice of this browser, so it lives in localStorage.
const KEY = 'telex-theme'

export function getTheme() {
  try {
    const t = localStorage.getItem(KEY)
    return t === 'light' || t === 'dark' ? t : 'auto'
  } catch {
    return 'auto'
  }
}

export function applyTheme(theme = getTheme()) {
  if (theme === 'auto') document.documentElement.removeAttribute('data-theme')
  else document.documentElement.dataset.theme = theme
}

export function setTheme(theme) {
  try {
    if (theme === 'auto') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, theme)
  } catch {}
  applyTheme(theme)
}
