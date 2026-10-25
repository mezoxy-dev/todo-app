export type Status = 'todo' | 'in_progress' | 'done'

// Who put a todo in its column: `pending` is waiting for the AI, `done` was placed by the AI,
// `failed` could not be classified (it stays in Gelen kutusu), `manual` was moved by the user.
export type Classification = 'pending' | 'done' | 'failed' | 'manual'

// `key` is the stable name of a category (`inbox`, `purchase`, ...); `name` is what the board shows.
export type Category = { id: number; key: string; name: string; position: number }

export type Todo = {
  id: number
  title: string
  description: string
  category_id: number
  // Place inside its column, 0 = top.
  position: number
  status: Status
  classification: Classification
  metadata: Record<string, unknown>
  created_at: string
  updated_at: string
}

// A new todo has no category: the server puts it on top of Gelen kutusu and the AI picks its column.
export type CreateTodoInput = {
  title: string
  description?: string
  status?: Status
  metadata?: Record<string, unknown>
}

export type UpdateTodoInput = {
  title?: string
  description?: string
  status?: Status
  category_id?: number
  // Index in the target column once the todo has left its old place.
  position?: number
  metadata?: Record<string, unknown>
}

export type LogEntry = {
  seq: number
  time: string
  level: 'info' | 'warn' | 'error'
  source: 'api' | 'ai' | 'system'
  // A single line; `details` is the multi-line part, when there is one.
  message: string
  details?: string
}

export type LogPage = {
  // Oldest first.
  entries: LogEntry[]
  // The newest number the server has handed out; the next request asks for what comes after it.
  last_seq: number
  // True when more than `limit` entries were waiting and only the newest ones are in `entries`.
  truncated: boolean
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

// The text of a failed request for the user: the requests reject with an Error that carries it.
export function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
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

// Sends the todo back to Gelen kutusu to be classified again; the answer is the todo as it is now.
export function classifyTodo(id: number): Promise<Todo> {
  return request<Todo>('POST', `/api/todos/${id}/classify`)
}

// The entries after number `after` (all of them when it is left out), at most `limit` of them.
export function getLogs(after?: number, limit?: number): Promise<LogPage> {
  const query = new URLSearchParams()
  if (after !== undefined) query.set('after', String(after))
  if (limit !== undefined) query.set('limit', String(limit))
  const text = query.toString()
  return request<LogPage>('GET', text === '' ? '/api/logs' : `/api/logs?${text}`)
}
