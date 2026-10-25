import { describe, expect, it, vi } from 'vitest'
import { classifyTodo, createTodo, getCategories, getLogs, updateTodo } from './api'

const categories = [
  { id: 4, key: 'inbox', name: 'Gelen kutusu', position: 0 },
  { id: 5, key: 'purchase', name: 'Satın alım', position: 1 },
  { id: 6, key: 'research', name: 'Araştırma', position: 2 },
]

const todo = {
  id: 7,
  title: 'Süt al',
  description: '2 litre',
  category_id: 4,
  position: 0,
  status: 'done',
  classification: 'pending',
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

    await expect(createTodo({ title: 'Süt al', description: '2 litre', status: 'done' })).resolves.toEqual(todo)

    expect(fetchMock).toHaveBeenCalledTimes(1)
    // No category: the server puts a new todo in Gelen kutusu and the AI picks the column.
    expect(fetchMock).toHaveBeenCalledWith('/api/todos', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: 'Süt al', description: '2 litre', status: 'done' }),
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

  it('updateTodo PATCHes the status and the description of a todo', async () => {
    const fetchMock = stubFetch(jsonResponse({ ...todo, status: 'in_progress', description: 'yeni' }))

    await updateTodo(7, { status: 'in_progress', description: 'yeni' })

    expect(fetchMock).toHaveBeenCalledWith('/api/todos/7', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status: 'in_progress', description: 'yeni' }),
    })
  })

  it('classifyTodo POSTs /api/todos/7/classify without a body and returns the todo', async () => {
    // 202 Accepted: the todo is back in Gelen kutusu and queued again.
    const fetchMock = stubFetch(jsonResponse(todo, 202))

    await expect(classifyTodo(7)).resolves.toEqual(todo)

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/todos/7/classify', { method: 'POST' })
  })

  it('classifyTodo rejects with the API error message when the todo does not exist', async () => {
    stubFetch(jsonResponse({ error: 'todo not found' }, 404))

    await expect(classifyTodo(99)).rejects.toThrow(new Error('todo not found'))
  })

  it('getLogs GETs /api/logs without a query by default', async () => {
    const reply = { entries: [], last_seq: 0, truncated: false }
    const fetchMock = stubFetch(jsonResponse(reply))

    await expect(getLogs()).resolves.toEqual(reply)

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/logs')
    expect(init?.method ?? 'GET').toBe('GET')
  })

  it('getLogs passes after and limit as the query', async () => {
    const fetchMock = stubFetch(jsonResponse({ entries: [], last_seq: 5, truncated: false }))

    await getLogs(5, 200)

    expect(fetchMock.mock.calls[0][0]).toBe('/api/logs?after=5&limit=200')
  })

  it('getLogs sends a single parameter alone, and after=0 as well', async () => {
    const fetchMock = vi.fn().mockImplementation(async () => jsonResponse({ entries: [], last_seq: 0, truncated: false }))
    vi.stubGlobal('fetch', fetchMock)

    await getLogs(undefined, 200)
    await getLogs(7)
    // 0 is a real value (the whole buffer), not "no parameter".
    await getLogs(0)

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/logs?limit=200',
      '/api/logs?after=7',
      '/api/logs?after=0',
    ])
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
