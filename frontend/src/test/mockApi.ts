import { onTestFinished, vi } from 'vitest'
import type { Mock } from 'vitest'
import type { Category, Todo } from '../api'

export type MockReply = { status?: number; body?: unknown }

type Handler = (body: unknown) => MockReply | Promise<MockReply>

/**
 * The id of every category, by key. In a real database they start at 4: migration 0003 adds the
 * nine categories next to the three of step 2 and drops those afterwards. Ids that differ from the
 * column positions (0..8) keep a test from taking the one for the other.
 */
export const cat = {
  inbox: 4,
  purchase: 5,
  research: 6,
  communication: 7,
  payment: 8,
  appointment: 9,
  work: 10,
  home: 11,
  other: 12,
} as const

/** The nine categories the database is seeded with, in column order. */
export const seedCategories: Category[] = [
  { id: cat.inbox, key: 'inbox', name: 'Gelen kutusu', position: 0 },
  { id: cat.purchase, key: 'purchase', name: 'Satın alım', position: 1 },
  { id: cat.research, key: 'research', name: 'Araştırma', position: 2 },
  { id: cat.communication, key: 'communication', name: 'İletişim', position: 3 },
  { id: cat.payment, key: 'payment', name: 'Ödeme', position: 4 },
  { id: cat.appointment, key: 'appointment', name: 'Randevu & Etkinlik', position: 5 },
  { id: cat.work, key: 'work', name: 'İş', position: 6 },
  { id: cat.home, key: 'home', name: 'Ev & Kişisel', position: 7 },
  { id: cat.other, key: 'other', name: 'Diğer', position: 8 },
]

/** A classified, settled todo in Gelen kutusu; a test overrides what it is about. */
export function makeTodo(overrides: Partial<Todo> = {}): Todo {
  return {
    id: 1,
    title: 'Todo',
    description: '',
    category_id: cat.inbox,
    position: 0,
    status: 'todo',
    classification: 'done',
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
