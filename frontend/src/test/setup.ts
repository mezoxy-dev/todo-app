import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  // The theme lives on <html> and in localStorage, both outside the rendered tree.
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
})
