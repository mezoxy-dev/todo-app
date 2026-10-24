import { describe, expect, it, vi } from 'vitest'
import { applyTheme, readStoredTheme, themes } from './index'

// The spies on localStorage are restored by the shared afterEach in test/setup.ts.
describe('themes', () => {
  it('lists the five themes in order', () => {
    expect(themes.map((theme) => [theme.id, theme.label])).toEqual([
      ['neubrutalism', 'Neubrutalizm'],
      ['glass', 'Glass'],
      ['swiss', 'Swiss'],
      ['skeuo', 'Pano'],
      ['zen', 'Zen'],
    ])
  })

  it('falls back to neubrutalism for a missing or unknown stored theme', () => {
    expect(readStoredTheme()).toBe('neubrutalism')

    localStorage.setItem('todo-theme', 'bogus')
    expect(readStoredTheme()).toBe('neubrutalism')

    localStorage.setItem('todo-theme', '')
    expect(readStoredTheme()).toBe('neubrutalism')

    localStorage.setItem('todo-theme', 'zen')
    expect(readStoredTheme()).toBe('zen')
  })

  it('reads back every listed theme', () => {
    for (const theme of themes) {
      localStorage.setItem('todo-theme', theme.id)
      expect(readStoredTheme()).toBe(theme.id)
    }
  })

  it('falls back to neubrutalism when localStorage cannot be read', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError')
    })

    expect(readStoredTheme()).toBe('neubrutalism')
  })

  it('applyTheme sets data-theme on <html> and remembers it', () => {
    applyTheme('swiss')

    expect(document.documentElement.dataset.theme).toBe('swiss')
    expect(localStorage.getItem('todo-theme')).toBe('swiss')
  })

  it('applyTheme still switches the theme when localStorage cannot be written', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })

    expect(() => applyTheme('glass')).not.toThrow()
    expect(document.documentElement.dataset.theme).toBe('glass')
  })
})
