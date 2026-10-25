export type ThemeChoice = 'light' | 'dark' | 'system'

const storageKey = 'registry-theme'

export function readTheme(): ThemeChoice {
  try {
    const stored = localStorage.getItem(storageKey)
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored
  } catch {
    // Private mode can block storage. The system theme still applies.
  }
  return 'system'
}

export function themeIsDark(choice: ThemeChoice): boolean {
  if (choice === 'dark') return true
  if (choice === 'light') return false
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

export function applyTheme(choice: ThemeChoice): void {
  document.documentElement.dataset.theme = themeIsDark(choice) ? 'dark' : 'light'
}

export function storeTheme(choice: ThemeChoice): void {
  try {
    localStorage.setItem(storageKey, choice)
  } catch {
    // The choice still applies for this visit.
  }
}
