import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import App from './App'
import { cardTitles, column, deferred, mockBoard, movesSettled } from './test/appHelpers'
import type { Handler } from './test/appHelpers'
import { makeTodo } from './test/mockApi'
import type { MockReply } from './test/mockApi'
import { applyTheme, readStoredTheme } from './themes'

// The single element matching `selector` under `root`; fails the test when there is none or
// several of them.
function one(selector: string, root: ParentNode = document): HTMLElement {
  const matches = root.querySelectorAll<HTMLElement>(selector)
  expect(matches, selector).toHaveLength(1)
  return matches[0]
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
    // The API answers by column, then position; the board must not re-sort.
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

  it('moves a card with its select to the top of the target column', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: makeTodo({ id: 1, title: 'Fatura öde', category_id: 2, position: 0 }),
    }))
    mockBoard(
      [
        makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 }),
        makeTodo({ id: 2, title: 'Yeni', category_id: 2 }),
      ],
      { 'PATCH /api/todos/1': patch },
    )
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için sütun'), 'Yapılıyor')
    await movesSettled()

    expect(patch).toHaveBeenCalledTimes(1)
    // A <select> value is a string; the API wants numbers. Position 0 is the top of the column.
    expect(patch.mock.calls[0][0]).toEqual({ category_id: 2, position: 0 })
    expect(cardTitles('Yapılıyor')).toEqual(['Fatura öde', 'Yeni'])
    expect(cardTitles('Henüz yapılmadı')).toEqual([])
    expect(screen.getByRole('heading', { name: 'Yapılıyor (2)' })).toBeInTheDocument()
    expect(screen.getByLabelText('Fatura öde için sütun')).toHaveValue('2')
  })

  it('shows the move before the server answers', async () => {
    const reply = deferred<MockReply>()
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 })], {
      'PATCH /api/todos/1': () => reply.promise,
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için sütun'), 'Yapılıyor')

    // The PATCH is still open, yet the card is already in its new column.
    expect(cardTitles('Yapılıyor')).toEqual(['Fatura öde'])
    expect(cardTitles('Henüz yapılmadı')).toEqual([])
    expect(screen.getByRole('heading', { name: 'Yapılıyor (1)' })).toBeInTheDocument()
    expect(screen.getByLabelText('Fatura öde için sütun')).toHaveValue('2')

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', category_id: 2, position: 0 }) })
    await movesSettled()

    expect(cardTitles('Yapılıyor')).toEqual(['Fatura öde'])
  })

  it('disables every card select while a move is saving', async () => {
    const reply = deferred<MockReply>()
    mockBoard(
      [
        makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 }),
        makeTodo({ id: 2, title: 'Süt al', category_id: 3 }),
        makeTodo({ id: 3, title: 'Yoğurt al', category_id: 3 }),
      ],
      { 'PATCH /api/todos/1': () => reply.promise },
    )
    const user = userEvent.setup()
    render(<App />)

    const cardSelects = await screen.findAllByLabelText(/ için sütun$/)
    expect(cardSelects).toHaveLength(3)
    for (const select of cardSelects) expect(select).toBeEnabled()
    // Cards can be picked up by their handle (and reached with the keyboard).
    for (const card of document.querySelectorAll('.card')) expect(card).toHaveAttribute('tabindex', '0')

    await user.selectOptions(screen.getByLabelText('Fatura öde için sütun'), 'Yapılıyor')

    // The select that was used, and the ones on the other cards in any column.
    const whileSaving = screen.getAllByLabelText(/ için sütun$/)
    expect(whileSaving).toHaveLength(3)
    for (const select of whileSaving) expect(select).toBeDisabled()
    // Dragging stays on: a drag handle that goes away would take the keyboard focus with it.
    // A drop that arrives meanwhile is turned away by App (see App.dnd.test.tsx).
    for (const card of document.querySelectorAll('.card')) expect(card).toHaveAttribute('tabindex', '0')

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', category_id: 2, position: 0 }) })
    await movesSettled()

    for (const select of screen.getAllByLabelText(/ için sütun$/)) expect(select).toBeEnabled()
  })

  it('turns away a second move while one is being saved', async () => {
    const reply = deferred<MockReply>()
    const patch = vi.fn<Handler>(() => reply.promise)
    // Only the first todo has a PATCH route: a request for the second one fails the test.
    mockBoard(
      [
        makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 }),
        makeTodo({ id: 2, title: 'Süt al', category_id: 3 }),
      ],
      { 'PATCH /api/todos/1': patch },
    )
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için sütun'), 'Yapılıyor')

    // A disabled select takes no input from a user. This change comes from outside, like a
    // drop that ends while the first move is still being saved would.
    const other = screen.getByLabelText('Süt al için sütun')
    expect(other).toBeDisabled()
    fireEvent.change(other, { target: { value: '2' } })

    expect(patch).toHaveBeenCalledTimes(1)
    expect(cardTitles('Tamamlandı')).toEqual(['Süt al'])
    expect(cardTitles('Yapılıyor')).toEqual(['Fatura öde'])

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', category_id: 2, position: 0 }) })
    await movesSettled()

    expect(cardTitles('Tamamlandı')).toEqual(['Süt al'])
    expect(cardTitles('Yapılıyor')).toEqual(['Fatura öde'])
  })

  it('reverts the move and shows the error above the board on failure', async () => {
    mockBoard(
      [
        makeTodo({ id: 1, title: 'A', category_id: 1 }),
        makeTodo({ id: 2, title: 'B', category_id: 1 }),
      ],
      { 'PATCH /api/todos/2': () => ({ status: 500, body: { error: 'boom' } }) },
    )
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('B için sütun'), 'Tamamlandı')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/^Todo taşınamadı: boom$/)
    expect(alert).toHaveClass('alert')
    // Above the board, not inside a column or a card.
    const board = one('.board')
    expect(board).not.toContainElement(alert)
    expect(alert.compareDocumentPosition(board) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // Everything is back exactly where it was.
    expect(cardTitles('Henüz yapılmadı')).toEqual(['A', 'B'])
    expect(cardTitles('Tamamlandı')).toEqual([])
    expect(screen.getByRole('heading', { name: 'Henüz yapılmadı (2)' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Tamamlandı (0)' })).toBeInTheDocument()
    expect(screen.getByLabelText('B için sütun')).toHaveValue('1')
    // And nothing stays locked.
    for (const select of screen.getAllByLabelText(/ için sütun$/)) expect(select).toBeEnabled()
  })

  it('clears the move error once the next move starts', async () => {
    let patches = 0
    mockBoard([makeTodo({ id: 1, title: 'A', category_id: 1 })], {
      'PATCH /api/todos/1': () => {
        patches += 1
        if (patches === 1) {
          return { status: 500, body: { error: 'boom' } }
        }
        return { body: makeTodo({ id: 1, title: 'A', category_id: 3, position: 0 }) }
      },
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('A için sütun'), 'Tamamlandı')
    expect(await screen.findByRole('alert')).toHaveTextContent('Todo taşınamadı: boom')

    await user.selectOptions(screen.getByLabelText('A için sütun'), 'Tamamlandı')
    await movesSettled()

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(cardTitles('Tamamlandı')).toEqual(['A'])
  })

  it('keeps a todo that was added while a move was saving when that move fails', async () => {
    const reply = deferred<MockReply>()
    mockBoard([makeTodo({ id: 1, title: 'A', category_id: 1 })], {
      'PATCH /api/todos/1': () => reply.promise,
      'POST /api/todos': () => ({ status: 201, body: makeTodo({ id: 2, title: 'Yeni', category_id: 1 }) }),
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('A için sütun'), 'Tamamlandı')
    // The add form is not locked by a saving move.
    await user.type(screen.getByLabelText('Todo başlığı'), 'Yeni{Enter}')
    expect(await screen.findByRole('heading', { name: 'Henüz yapılmadı (1)' })).toBeInTheDocument()
    expect(cardTitles('Henüz yapılmadı')).toEqual(['Yeni'])
    expect(cardTitles('Tamamlandı')).toEqual(['A'])

    reply.resolve({ status: 500, body: { error: 'boom' } })

    expect(await screen.findByRole('alert')).toHaveTextContent('Todo taşınamadı: boom')
    // The move is undone, the new todo is still on top of its column, as on the server.
    expect(cardTitles('Henüz yapılmadı')).toEqual(['Yeni', 'A'])
    expect(cardTitles('Tamamlandı')).toEqual([])
  })

  it('keeps an empty column as a drop target', async () => {
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 })])
    render(<App />)
    await screen.findByRole('region', { name: 'Yapılıyor' })

    // "Boş" lives inside the drop area, so the area exists while the column is empty.
    const list = one('.card-list', column('Yapılıyor'))
    expect(within(list).getByText('Boş')).toBeInTheDocument()
    // It is the drag library's drop area for category 2, like the one of the full column.
    expect(list).toHaveAttribute('data-rfd-droppable-id', '2')
    expect(one('.card-list', column('Henüz yapılmadı'))).toHaveAttribute('data-rfd-droppable-id', '1')
    // A card is draggable under its todo id.
    expect(one('.card', column('Henüz yapılmadı'))).toHaveAttribute('data-rfd-draggable-id', '1')
  })

  it('renders and moves a card under StrictMode without console warnings', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    const warnings = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mockBoard(
      [
        makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 }),
        makeTodo({ id: 2, title: 'Yeni', category_id: 2 }),
      ],
      { 'PATCH /api/todos/1': () => ({ body: makeTodo({ id: 1, title: 'Fatura öde', category_id: 2, position: 0 }) }) },
    )
    const user = userEvent.setup()
    render(
      <StrictMode>
        <App />
      </StrictMode>,
    )

    await user.selectOptions(await screen.findByLabelText('Fatura öde için sütun'), 'Yapılıyor')
    await movesSettled()

    expect(cardTitles('Yapılıyor')).toEqual(['Fatura öde', 'Yeni'])
    expect(errors).not.toHaveBeenCalled()
    expect(warnings).not.toHaveBeenCalled()
  })
})

describe('theme menu', () => {
  // Only the Pano theme is left, so there is nothing to choose and the menu is not rendered.
  it('is not shown while a single theme is left: loading, load error or ready', async () => {
    mockBoard([], { 'GET /api/todos': () => ({ status: 500, body: { error: 'internal server error' } }) })
    render(<App />)

    expect(screen.getByText('Yükleniyor...')).toBeInTheDocument()
    expect(screen.queryByRole('group', { name: 'Tasarım' })).not.toBeInTheDocument()

    await screen.findByRole('alert')
    expect(screen.queryByRole('group', { name: 'Tasarım' })).not.toBeInTheDocument()
  })

  it('applies the Pano theme on <html> even when a removed theme was stored', async () => {
    // What main.tsx does before the first render; 'zen' was picked before it was removed.
    localStorage.setItem('todo-theme', 'zen')
    applyTheme(readStoredTheme())
    mockBoard()
    render(<App />)
    await screen.findByRole('region', { name: 'Henüz yapılmadı' })

    expect(document.documentElement.dataset.theme).toBe('skeuo')
    expect(screen.queryByRole('group', { name: 'Tasarım' })).not.toBeInTheDocument()
  })
})

// The classes the theme stylesheets are written against (spec section 5.3).
describe('markup contract', () => {
  it('renders the markup contract', async () => {
    mockBoard([
      makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 }),
      makeTodo({ id: 2, title: 'Süt al', category_id: 3 }),
    ])
    render(<App />)
    await screen.findByRole('region', { name: 'Henüz yapılmadı' })

    // Page and header: the title; the theme menu is not rendered while a single theme is left.
    const app = one('main.app')
    const header = one(':scope > .app-header', app)
    expect(one('h1.app-title', header)).toHaveTextContent("Todo'lar")
    expect(header.querySelector('.theme-switcher')).toBeNull()

    // Add form.
    const form = one('form.add-form', app)
    expect(one('input.field.add-input', form)).toBe(screen.getByLabelText('Todo başlığı'))
    expect(one('select.field.add-select', form)).toBe(screen.getByLabelText('Sütun'))
    expect(one('button.button.add-button', form)).toBe(screen.getByRole('button', { name: 'Ekle' }))

    // Board: one column per category in order, each with a title and a drop area.
    const board = one('.board', app)
    const columns = Array.from(board.querySelectorAll<HTMLElement>(':scope > .column'))
    expect(columns.map((element) => element.dataset.column)).toEqual(['1', '2', '3'])
    for (const element of columns) {
      expect(one('.column-title', element)).toHaveTextContent(/^.+ \(\d\)$/)
      one('.card-list', element)
    }
    const [first, second, third] = columns

    // A card: .card > .card-body > .card-title, and the select lives in the body too.
    const card = one('.card-list .card', first)
    expect(one(':scope > .card-body > .card-title', card)).toHaveTextContent('Fatura öde')
    expect(one(':scope > .card-body > .card-select', card)).toBe(screen.getByLabelText('Fatura öde için sütun'))
    expect(first.querySelector('.empty')).toBeNull()
    expect(one('.card-list .card .card-title', third)).toHaveTextContent('Süt al')

    // The empty column keeps its drop area, and "Boş" sits inside it.
    expect(one('.card-list .empty', second)).toHaveTextContent('Boş')
    expect(second.querySelector('.card')).toBeNull()

    // Nothing left over from the other states.
    expect(document.querySelector('.status, .alert')).toBeNull()
  })

  it('marks the loading text with the status class', async () => {
    mockBoard()
    render(<App />)

    expect(screen.getByText('Yükleniyor...')).toHaveClass('status')

    await screen.findByRole('region', { name: 'Henüz yapılmadı' })
  })

  it('marks the load error as an alert and the retry button as a button', async () => {
    mockBoard([], { 'GET /api/todos': () => ({ status: 500, body: { error: 'internal server error' } }) })
    render(<App />)

    expect(await screen.findByRole('alert')).toHaveClass('alert')
    expect(screen.getByRole('button', { name: 'Tekrar dene' })).toHaveClass('button')
  })

  it('marks the add error as an alert inside the form', async () => {
    mockBoard([], { 'POST /api/todos': () => ({ status: 400, body: { error: 'title is required' } }) })
    const user = userEvent.setup()
    render(<App />)

    await user.type(await screen.findByLabelText('Todo başlığı'), 'Fatura öde{Enter}')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveClass('alert')
    expect(alert.closest('.add-form')).not.toBeNull()
  })
})
