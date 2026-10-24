// The themes the menu offers, in menu order. An id is the value of <html data-theme> and
// names a stylesheet, themes/<id>.css; the first theme is the default one.
// To drop a theme: delete its stylesheet, its @import in index.css and its entry here.
export const themes = [
  { id: 'neubrutalism', label: 'Neubrutalizm' },
  { id: 'glass', label: 'Glass' },
  { id: 'swiss', label: 'Swiss' },
  { id: 'skeuo', label: 'Pano' },
  { id: 'zen', label: 'Zen' },
] as const

export type ThemeId = (typeof themes)[number]['id']

export const DEFAULT_THEME: ThemeId = themes[0].id
export const THEME_STORAGE_KEY = 'todo-theme'

// Anything that is not a listed theme (missing, renamed, tampered with) is the default.
function toThemeId(value: string | null | undefined): ThemeId {
  return themes.find((theme) => theme.id === value)?.id ?? DEFAULT_THEME
}

export function readStoredTheme(): ThemeId {
  try {
    return toThemeId(localStorage.getItem(THEME_STORAGE_KEY))
  } catch {
    // Storage can be blocked (privacy settings); the default theme will do then.
    return DEFAULT_THEME
  }
}

// The theme in force, as <html data-theme> says.
export function readAppliedTheme(): ThemeId {
  return toThemeId(document.documentElement.dataset.theme)
}

export function applyTheme(id: ThemeId): void {
  document.documentElement.dataset.theme = id
  try {
    localStorage.setItem(THEME_STORAGE_KEY, id)
  } catch {
    // The choice is not remembered, but the theme still applies for this visit.
  }
}
