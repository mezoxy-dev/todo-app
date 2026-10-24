import { describe, expect, it, vi } from 'vitest'
import { createTodo, getCategories, updateTodo } from './api'

const categories = [
  { id: 1, name: 'Henüz yapılmadı', position: 1 },
  { id: 2, name: 'Yapılıyor', position: 2 },
  { id: 3, name: 'Tamamlandı', position: 3 },
]

const todo = {
  id: 7,
  title: 'Süt al',
  category_id: 3,
  metadata: {},
  created_at: '2026-10-24T12:00:00.123Z',
  updated_at: '2026-10-24T12:00:00.123Z',
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

// Replaces the global fetch for the current test; setup.ts restores it afterwards.
function stubFetch(response: Response) {
  const fetchMock = vi.fn().mockResolvedValue(response)
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('api client', () => {
  it('getCategories GETs /api/categories and returns the parsed body', async () => {
    const fetchMock = stubFetch(jsonResponse(categories))

    await expect(getCategories()).resolves.toEqual(categories)

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/categories')
    expect(init?.method ?? 'GET').toBe('GET')
  })

  it('createTodo POSTs JSON and returns the created todo', async () => {
    const fetchMock = stubFetch(jsonResponse(todo, 201))

    await expect(createTodo({ title: 'Süt al', category_id: 3 })).resolves.toEqual(todo)

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/todos', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: 'Süt al', category_id: 3 }),
    })
  })

  it('updateTodo PATCHes /api/todos/7', async () => {
    const fetchMock = stubFetch(jsonResponse({ ...todo, category_id: 2 }))

    await expect(updateTodo(7, { category_id: 2 })).resolves.toEqual({ ...todo, category_id: 2 })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/todos/7', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ category_id: 2 }),
    })
  })

  it('rejects with the API error message on non-2xx', async () => {
    stubFetch(jsonResponse({ error: 'title is required' }, 400))

    await expect(createTodo({ title: '' })).rejects.toThrow(new Error('title is required'))
  })

  it('rejects with "sunucu hatası (HTTP 500)" when the error body is not JSON', async () => {
    stubFetch(new Response('Internal Server Error', { status: 500 }))

    await expect(getCategories()).rejects.toThrow(new Error('sunucu hatası (HTTP 500)'))
  })

  it('rejects with "sunucu hatası (HTTP 500)" when the error body is empty', async () => {
    // What the Vite dev proxy answers while the Go server is down.
    stubFetch(new Response(null, { status: 500 }))

    await expect(getCategories()).rejects.toThrow(new Error('sunucu hatası (HTTP 500)'))
  })

  it('rejects with "sunucu hatası (HTTP 400)" when the error body is JSON without an error message', async () => {
    stubFetch(jsonResponse({ detail: 'no error field' }, 400))

    await expect(createTodo({ title: 'x' })).rejects.toThrow(new Error('sunucu hatası (HTTP 400)'))
  })

  it('rejects with "sunucuya ulaşılamadı" when fetch itself fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    await expect(getCategories()).rejects.toThrow(new Error('sunucuya ulaşılamadı'))
  })

  it('resolves with undefined on 204 No Content instead of parsing an empty body', async () => {
    stubFetch(new Response(null, { status: 204 }))

    await expect(updateTodo(7, { title: 'x' })).resolves.toBeUndefined()
  })
})
