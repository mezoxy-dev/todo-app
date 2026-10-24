import { onTestFinished, vi } from 'vitest'
import type { Mock } from 'vitest'
import type { Category, Todo } from '../api'

export type MockReply = { status?: number; body?: unknown }

type Handler = (body: unknown) => MockReply | Promise<MockReply>

/** The three categories the database is seeded with, in column order. */
export const seedCategories: Category[] = [
  { id: 1, name: 'Henüz yapılmadı', position: 1 },
  { id: 2, name: 'Yapılıyor', position: 2 },
  { id: 3, name: 'Tamamlandı', position: 3 },
]

export function makeTodo(overrides: Partial<Todo> = {}): Todo {
  return {
    id: 1,
    title: 'Todo',
    category_id: 1,
    metadata: {},
    created_at: '2026-10-24T12:00:00.000Z',
    updated_at: '2026-10-24T12:00:00.000Z',
    ...overrides,
  }
}

/**
 * Replaces the global fetch with a router. Routes are keyed "METHOD /path"
 * (for example "PATCH /api/todos/1"); each handler receives the parsed JSON
 * request body (undefined when there is none) and returns the reply.
 *
 * A request without a matching route fails the test. The app swallows network
 * errors (it shows an error message instead), so merely throwing from fetch
 * would not be enough: the miss is recorded and reported once the test ends.
 */
export function mockApi(routes: Record<string, Handler>): Mock {
  const unmatched: string[] = []
  onTestFinished(() => {
    if (unmatched.length > 0) {
      throw new Error(`mockApi: no route for ${unmatched.join(', ')}`)
    }
  })

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const key = `${init?.method ?? 'GET'} ${String(input)}`
    const handler = routes[key]
    if (handler === undefined) {
      unmatched.push(key)
      throw new Error(`mockApi: no route for ${key}`)
    }
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined
    const reply = await handler(body)
    return new Response(reply.body === undefined ? null : JSON.stringify(reply.body), {
      status: reply.status ?? 200,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}
