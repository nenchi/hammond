// Applies the user's theme ("auto" | "light" | "dark") as the theme-dark class on <html>.
// Remembered in localStorage so the login page (before /api/me) already has the right colours.
const KEY = 'hammond.theme'
const media = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null
let current = 'auto'

function apply() {
  const dark = current === 'dark' || (current !== 'light' && !!media && media.matches)
  document.documentElement.classList.toggle('theme-dark', dark)
  // tells the browser the page handles dark itself, so phone browsers don't force their own dark mode
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
}

export function setTheme(theme) {
  current = theme || 'auto'
  try {
    window.localStorage.setItem(KEY, current)
  } catch (e) {}
  apply()
}

export function isDark() {
  return document.documentElement.classList.contains('theme-dark')
}

try {
  current = window.localStorage.getItem(KEY) || 'auto'
} catch (e) {}
if (media) {
  media.addEventListener ? media.addEventListener('change', apply) : media.addListener(apply)
}
apply()
