export type Category = { id: number; name: string; position: number }

export type Todo = {
  id: number
  title: string
  category_id: number
  metadata: Record<string, unknown>
  created_at: string
  updated_at: string
}

export type CreateTodoInput = {
  title: string
  category_id?: number
  metadata?: Record<string, unknown>
}

export type UpdateTodoInput = {
  title?: string
  category_id?: number
  metadata?: Record<string, unknown>
}

// Error text shown to the user: the API's own {"error": "..."} message, or a
// generic one when the body is not the JSON the contract promises.
async function errorMessage(response: Response): Promise<string> {
  try {
    const data: unknown = await response.json()
    if (typeof data === 'object' && data !== null && 'error' in data && typeof data.error === 'string') {
      return data.error
    }
  } catch {
    // The body is not JSON; fall through to the generic message.
  }
  return `sunucu hatası (HTTP ${response.status})`
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method }
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' }
    init.body = JSON.stringify(body)
  }

  let response: Response
  try {
    response = await fetch(path, init)
  } catch {
    throw new Error('sunucuya ulaşılamadı')
  }

  if (!response.ok) {
    throw new Error(await errorMessage(response))
  }
  if (response.status === 204) {
    return undefined as T
  }
  return (await response.json()) as T
}

export function getCategories(): Promise<Category[]> {
  return request<Category[]>('GET', '/api/categories')
}

export function getTodos(): Promise<Todo[]> {
  return request<Todo[]>('GET', '/api/todos')
}

export function createTodo(input: CreateTodoInput): Promise<Todo> {
  return request<Todo>('POST', '/api/todos', input)
}

export function updateTodo(id: number, patch: UpdateTodoInput): Promise<Todo> {
  return request<Todo>('PATCH', `/api/todos/${id}`, patch)
}
