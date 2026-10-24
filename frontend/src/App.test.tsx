import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Todo } from './api'
import App from './App'
import { makeTodo, mockApi, seedCategories } from './test/mockApi'
import type { MockReply } from './test/mockApi'

type Routes = Parameters<typeof mockApi>[0]
type Handler = Routes[string]

// The seeded categories plus the given todos; `routes` adds or overrides routes.
function mockBoard(todos: Todo[] = [], routes: Routes = {}) {
  return mockApi({
    'GET /api/categories': () => ({ body: seedCategories }),
    'GET /api/todos': () => ({ body: todos }),
    ...routes,
  })
}

// A reply the test releases by hand, to observe the UI while a request is pending.
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

function column(name: string): HTMLElement {
  return screen.getByRole('region', { name })
}

// Card titles of one column, top to bottom.
function cardTitles(name: string): string[] {
  return within(column(name))
    .queryAllByRole('listitem')
    .map((item) => within(item).getByRole('heading').textContent ?? '')
}

describe('board', () => {
  it('shows "Yükleniyor..." and then one column per category with counts', async () => {
    const fetchMock = mockBoard([
      makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 }),
      makeTodo({ id: 2, title: 'Süt al', category_id: 3 }),
    ])

    render(<App />)

    expect(screen.getByRole('heading', { level: 1, name: "Todo'lar" })).toBeInTheDocument()
    expect(screen.getByText('Yükleniyor...')).toBeInTheDocument()
    // Categories and todos are requested in parallel, before either reply arrives.
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(['/api/categories', '/api/todos'])

    const headings = await screen.findAllByRole('heading', { level: 2 })
    expect(headings.map((heading) => heading.textContent)).toEqual([
      'Henüz yapılmadı (1)',
      'Yapılıyor (0)',
      'Tamamlandı (1)',
    ])
    expect(screen.queryByText('Yükleniyor...')).not.toBeInTheDocument()
  })

  it('puts each todo in its column in API order and shows "Boş" in empty columns', async () => {
    // The API answers newest first; the board must not re-sort.
    mockBoard([
      makeTodo({ id: 4, title: 'Dördüncü', category_id: 1 }),
      makeTodo({ id: 3, title: 'Üçüncü', category_id: 3 }),
      makeTodo({ id: 2, title: 'İkinci', category_id: 1 }),
      makeTodo({ id: 1, title: 'Birinci', category_id: 1 }),
    ])

    render(<App />)
    await screen.findByRole('region', { name: 'Henüz yapılmadı' })

    expect(cardTitles('Henüz yapılmadı')).toEqual(['Dördüncü', 'İkinci', 'Birinci'])
    expect(cardTitles('Tamamlandı')).toEqual(['Üçüncü'])

    const empty = column('Yapılıyor')
    expect(within(empty).getByText('Boş')).toBeInTheDocument()
    expect(within(empty).queryAllByRole('listitem')).toHaveLength(0)
    expect(within(column('Henüz yapılmadı')).queryByText('Boş')).not.toBeInTheDocument()
  })

  it('shows "Sunucuya ulaşılamadı." and reloads on "Tekrar dene"', async () => {
    let todoRequests = 0
    mockBoard([], {
      'GET /api/todos': () => {
        todoRequests += 1
        if (todoRequests === 1) {
          return { status: 500, body: { error: 'internal server error' } }
        }
        return { body: [makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 })] }
      },
    })
    const user = userEvent.setup()

    render(<App />)

    expect(await screen.findByRole('alert')).toHaveTextContent(/^Sunucuya ulaşılamadı\.$/)
    expect(screen.queryByRole('region')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Tekrar dene' }))

    expect(await screen.findByRole('heading', { name: 'Henüz yapılmadı (1)' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Tekrar dene' })).not.toBeInTheDocument()
    expect(cardTitles('Henüz yapılmadı')).toEqual(['Fatura öde'])
    expect(todoRequests).toBe(2)
  })
})

describe('add form', () => {
  it('offers the categories as options, the first one selected', async () => {
    mockBoard()
    render(<App />)

    const select = await screen.findByLabelText('Sütun')
    expect(within(select).getAllByRole('option').map((option) => option.textContent)).toEqual([
      'Henüz yapılmadı',
      'Yapılıyor',
      'Tamamlandı',
    ])
    expect(select).toHaveValue('1')
    expect(screen.getByLabelText('Todo başlığı')).toHaveAttribute('placeholder', 'Yeni todo yaz...')
  })

  it('adds to the default column with a trimmed title', async () => {
    const post = vi.fn<Handler>(() => ({
      status: 201,
      body: makeTodo({ id: 5, title: 'Fatura öde', category_id: 1 }),
    }))
    mockBoard([makeTodo({ id: 1, title: 'Eski', category_id: 1 })], { 'POST /api/todos': post })
    const user = userEvent.setup()
    render(<App />)

    const input = await screen.findByLabelText('Todo başlığı')
    await user.type(input, '  Fatura öde  ')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))

    expect(await screen.findByRole('heading', { name: 'Henüz yapılmadı (2)' })).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][0]).toEqual({ title: 'Fatura öde', category_id: 1 })
    // The new todo is put on top of its column.
    expect(cardTitles('Henüz yapılmadı')).toEqual(['Fatura öde', 'Eski'])
    expect(input).toHaveValue('')
  })

  it('adds directly to Tamamlandı when selected', async () => {
    const post = vi.fn<Handler>(() => ({
      status: 201,
      body: makeTodo({ id: 5, title: 'Süt al', category_id: 3 }),
    }))
    mockBoard([], { 'POST /api/todos': post })
    const user = userEvent.setup()
    render(<App />)

    const select = await screen.findByLabelText('Sütun')
    await user.selectOptions(select, 'Tamamlandı')
    await user.type(screen.getByLabelText('Todo başlığı'), 'Süt al{Enter}')

    expect(await screen.findByRole('heading', { name: 'Tamamlandı (1)' })).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][0]).toEqual({ title: 'Süt al', category_id: 3 })
    expect(cardTitles('Tamamlandı')).toEqual(['Süt al'])
    // The chosen column is kept for the next todo.
    expect(select).toHaveValue('3')
  })

  it('disables "Ekle" while the trimmed title is empty', async () => {
    const post = vi.fn<Handler>(() => ({ status: 201, body: makeTodo() }))
    mockBoard([], { 'POST /api/todos': post })
    const user = userEvent.setup()
    render(<App />)

    const button = await screen.findByRole('button', { name: 'Ekle' })
    const input = screen.getByLabelText('Todo başlığı')
    expect(button).toBeDisabled()

    await user.type(input, '   ')
    expect(button).toBeDisabled()
    await user.keyboard('{Enter}')
    expect(post).not.toHaveBeenCalled()

    await user.type(input, 'a')
    expect(button).toBeEnabled()
  })

  it('sends only one POST on double submit', async () => {
    const reply = deferred<MockReply>()
    const post = vi.fn<Handler>(() => reply.promise)
    mockBoard([], { 'POST /api/todos': post })
    const user = userEvent.setup()
    render(<App />)

    await user.type(await screen.findByLabelText('Todo başlığı'), 'Fatura öde')
    await user.keyboard('{Enter}{Enter}')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))

    expect(post).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: 'Ekle' })).toBeDisabled()

    reply.resolve({ status: 201, body: makeTodo({ id: 5, title: 'Fatura öde', category_id: 1 }) })
    expect(await screen.findByRole('heading', { name: 'Henüz yapılmadı (1)' })).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
    expect(cardTitles('Henüz yapılmadı')).toEqual(['Fatura öde'])
  })

  it('ignores a second submit that arrives before the form has re-rendered', async () => {
    const reply = deferred<MockReply>()
    const post = vi.fn<Handler>(() => reply.promise)
    mockBoard([], { 'POST /api/todos': post })
    const user = userEvent.setup()
    render(<App />)

    await user.type(await screen.findByLabelText('Todo başlığı'), 'Fatura öde')
    const button = screen.getByRole('button', { name: 'Ekle' })

    // Both clicks land inside one act() scope, so React has not yet re-rendered
    // the button as disabled when the second one arrives.
    act(() => {
      fireEvent.click(button)
      fireEvent.click(button)
    })

    expect(post).toHaveBeenCalledTimes(1)

    reply.resolve({ status: 201, body: makeTodo({ id: 5, title: 'Fatura öde', category_id: 1 }) })
    expect(await screen.findByRole('heading', { name: 'Henüz yapılmadı (1)' })).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('shows "Todo eklenemedi: <detay>" and keeps the input on failure', async () => {
    mockBoard([], {
      'POST /api/todos': () => ({ status: 400, body: { error: 'title is required' } }),
    })
    const user = userEvent.setup()
    render(<App />)

    const input = await screen.findByLabelText('Todo başlığı')
    await user.type(input, 'Fatura öde')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/^Todo eklenemedi: title is required$/)
    expect(input).toHaveValue('Fatura öde')
    expect(cardTitles('Henüz yapılmadı')).toEqual([])
    // The form is usable again after the failure.
    expect(screen.getByRole('button', { name: 'Ekle' })).toBeEnabled()
  })

  it('clears the error message once a retry succeeds', async () => {
    let posts = 0
    mockBoard([], {
      'POST /api/todos': () => {
        posts += 1
        if (posts === 1) {
          return { status: 500, body: { error: 'boom' } }
        }
        return { status: 201, body: makeTodo({ id: 5, title: 'Fatura öde', category_id: 1 }) }
      },
    })
    const user = userEvent.setup()
    render(<App />)

    const input = await screen.findByLabelText('Todo başlığı')
    await user.type(input, 'Fatura öde')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Todo eklenemedi: boom')

    await user.click(screen.getByRole('button', { name: 'Ekle' }))

    expect(await screen.findByRole('heading', { name: 'Henüz yapılmadı (1)' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(input).toHaveValue('')
  })
})

describe('moving cards', () => {
  it('shows a column select on every card with the current column selected', async () => {
    mockBoard([
      makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 }),
      makeTodo({ id: 2, title: 'Süt al', category_id: 3 }),
    ])
    render(<App />)

    const first = await screen.findByLabelText('Fatura öde için sütun')
    expect(first).toHaveValue('1')
    expect(within(first).getAllByRole('option').map((option) => option.textContent)).toEqual([
      'Henüz yapılmadı',
      'Yapılıyor',
      'Tamamlandı',
    ])
    expect(screen.getByLabelText('Süt al için sütun')).toHaveValue('3')
  })

  it('moves a card and PATCHes a numeric category_id', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: makeTodo({ id: 1, title: 'Fatura öde', category_id: 2 }),
    }))
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 })], {
      'PATCH /api/todos/1': patch,
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için sütun'), 'Yapılıyor')

    expect(await screen.findByRole('heading', { name: 'Henüz yapılmadı (0)' })).toBeInTheDocument()
    expect(patch).toHaveBeenCalledTimes(1)
    // A <select> value is a string; the API wants a number.
    expect(patch.mock.calls[0][0]).toEqual({ category_id: 2 })
    expect(cardTitles('Yapılıyor')).toEqual(['Fatura öde'])
    expect(cardTitles('Henüz yapılmadı')).toEqual([])
    expect(screen.getByLabelText('Fatura öde için sütun')).toHaveValue('2')
  })

  it('keeps API order (newest first) in the target column after a move', async () => {
    mockBoard(
      [
        makeTodo({ id: 2, title: 'Yeni', category_id: 2 }),
        makeTodo({ id: 1, title: 'Eski', category_id: 1 }),
      ],
      { 'PATCH /api/todos/1': () => ({ body: makeTodo({ id: 1, title: 'Eski', category_id: 2 }) }) },
    )
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Eski için sütun'), 'Yapılıyor')

    expect(await screen.findByRole('heading', { name: 'Yapılıyor (2)' })).toBeInTheDocument()
    expect(cardTitles('Yapılıyor')).toEqual(['Yeni', 'Eski'])
  })

  it('disables the card select while the PATCH is pending', async () => {
    const reply = deferred<MockReply>()
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 })], {
      'PATCH /api/todos/1': () => reply.promise,
    })
    const user = userEvent.setup()
    render(<App />)

    const select = await screen.findByLabelText('Fatura öde için sütun')
    expect(select).toBeEnabled()
    await user.selectOptions(select, 'Yapılıyor')

    expect(select).toBeDisabled()
    // The board only changes once the server has answered.
    expect(select).toHaveValue('1')
    expect(cardTitles('Henüz yapılmadı')).toEqual(['Fatura öde'])

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', category_id: 2 }) })

    expect(await screen.findByRole('heading', { name: 'Yapılıyor (1)' })).toBeInTheDocument()
    expect(screen.getByLabelText('Fatura öde için sütun')).toBeEnabled()
  })

  it('shows "Todo taşınamadı: <detay>" and leaves card and select on the old column on failure', async () => {
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 })], {
      'PATCH /api/todos/1': () => ({ status: 500, body: { error: 'boom' } }),
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için sütun'), 'Yapılıyor')

    expect(await screen.findByRole('alert')).toHaveTextContent(/^Todo taşınamadı: boom$/)
    expect(cardTitles('Henüz yapılmadı')).toEqual(['Fatura öde'])
    expect(cardTitles('Yapılıyor')).toEqual([])
    expect(screen.getByRole('heading', { name: 'Henüz yapılmadı (1)' })).toBeInTheDocument()
    const select = screen.getByLabelText('Fatura öde için sütun')
    expect(select).toHaveValue('1')
    expect(select).toBeEnabled()
  })
})
