import { screen, waitFor, within } from '@testing-library/react'
import { expect } from 'vitest'
import type { Todo } from '../api'
import { mockApi, seedCategories } from './mockApi'

export type Routes = Parameters<typeof mockApi>[0]
export type Handler = Routes[string]

// The seeded categories plus the given todos; `routes` adds or overrides routes.
export function mockBoard(todos: Todo[] = [], routes: Routes = {}) {
  return mockApi({
    'GET /api/categories': () => ({ body: seedCategories }),
    'GET /api/todos': () => ({ body: todos }),
    ...routes,
  })
}

// A reply the test releases by hand, to observe the UI while a request is pending.
export function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

export function column(name: string): HTMLElement {
  return screen.getByRole('region', { name })
}

// Card titles of one column, top to bottom.
export function cardTitles(name: string): string[] {
  return within(column(name))
    .queryAllByRole('listitem')
    .map((item) => within(item).getByRole('heading').textContent ?? '')
}

// Waits until no move is being saved any more: every card select is usable again. A move shows
// at once, so without this a test could end while its PATCH is still being answered.
export async function movesSettled() {
  await waitFor(() => {
    for (const select of screen.queryAllByLabelText(/ için sütun$/)) expect(select).toBeEnabled()
  })
}
