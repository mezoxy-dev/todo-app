import { useState } from 'react'
import { applyTheme, readAppliedTheme, themes } from '../themes'
import type { ThemeId } from '../themes'

export function ThemeSwitcher() {
  // The theme in force lives on <html data-theme> (main.tsx applies the stored one before the
  // first render); the state only makes the buttons follow it.
  const [active, setActive] = useState<ThemeId>(readAppliedTheme)

  // With a single theme left there is nothing to choose from.
  if (themes.length < 2) return null

  function choose(id: ThemeId) {
    applyTheme(id)
    setActive(id)
  }

  return (
    <div role="group" aria-label="Tasarım" className="theme-switcher flex flex-wrap gap-2">
      {themes.map((theme) => (
        <button
          key={theme.id}
          type="button"
          aria-pressed={theme.id === active}
          onClick={() => choose(theme.id)}
          className="theme-button px-2 py-1"
        >
          {theme.label}
        </button>
      ))}
    </div>
  )
}
