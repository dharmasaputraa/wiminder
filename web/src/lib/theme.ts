import { useSyncExternalStore } from 'react'

export type Theme = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'wiminder.theme'

function isTheme(value: string | null): value is Theme {
  return value === 'light' || value === 'dark' || value === 'system'
}

// Mirrors the inline snippet in index.html so the first React render matches
// the class already applied to <html> (no flash between the two).
function getStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    return isTheme(stored) ? stored : 'system'
  } catch {
    return 'system'
  }
}

function applyTheme(theme: Theme) {
  const dark =
    theme === 'dark' ||
    (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
  // The favicon is rendered by the browser, outside the page's CSS, so it needs
  // an explicit white/black variant swap instead of following text-foreground.
  const favicon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (favicon) favicon.href = dark ? '/favicon-dark.svg' : '/favicon.svg'
}

let currentTheme: Theme = getStoredTheme()
const listeners = new Set<() => void>()

function emit() {
  for (const listener of listeners) listener()
}

// Stay live while following the OS setting.
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
  if (currentTheme === 'system') applyTheme(currentTheme)
})

export function getTheme(): Theme {
  return currentTheme
}

export function setTheme(theme: Theme) {
  currentTheme = theme
  try {
    localStorage.setItem(STORAGE_KEY, theme)
  } catch {
    // Storage unavailable (private mode etc.) — theme still applies for the session.
  }
  applyTheme(theme)
  emit()
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useTheme(): [Theme, (theme: Theme) => void] {
  const theme = useSyncExternalStore(subscribe, getTheme, () => 'system' as Theme)
  return [theme, setTheme]
}
