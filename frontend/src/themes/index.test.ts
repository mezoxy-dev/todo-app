import { describe, expect, it, vi } from 'vitest'
import { applyTheme, readStoredTheme, themes } from './index'

// The spies on localStorage are restored by the shared afterEach in test/setup.ts.
describe('themes', () => {
  it('lists only the Pano theme', () => {
    expect(themes.map((theme) => [theme.id, theme.label])).toEqual([['skeuo', 'Pano']])
  })

  it('falls back to skeuo for a missing, unknown or removed stored theme', () => {
    expect(readStoredTheme()).toBe('skeuo')

    localStorage.setItem('todo-theme', 'bogus')
    expect(readStoredTheme()).toBe('skeuo')

    localStorage.setItem('todo-theme', '')
    expect(readStoredTheme()).toBe('skeuo')

    // A theme the user picked before it was removed.
    localStorage.setItem('todo-theme', 'zen')
    expect(readStoredTheme()).toBe('skeuo')
  })

  it('reads back every listed theme', () => {
    for (const theme of themes) {
      localStorage.setItem('todo-theme', theme.id)
      expect(readStoredTheme()).toBe(theme.id)
    }
  })

  it('falls back to skeuo when localStorage cannot be read', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError')
    })

    expect(readStoredTheme()).toBe('skeuo')
  })

  it('applyTheme sets data-theme on <html> and remembers it', () => {
    applyTheme('skeuo')

    expect(document.documentElement.dataset.theme).toBe('skeuo')
    expect(localStorage.getItem('todo-theme')).toBe('skeuo')
  })

  it('applyTheme still sets the theme when localStorage cannot be written', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })

    expect(() => applyTheme('skeuo')).not.toThrow()
    expect(document.documentElement.dataset.theme).toBe('skeuo')
  })
})
