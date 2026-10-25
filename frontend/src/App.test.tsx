import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import App from './App'
import { card, cardTitles, column, deferred, mockBoard, movesSettled } from './test/appHelpers'
import type { Handler } from './test/appHelpers'
import { cat, makeTodo } from './test/mockApi'
import type { MockReply } from './test/mockApi'
import { applyTheme, readStoredTheme } from './themes'

// The single element matching `selector` under `root`; fails the test when there is none or
// several of them.
function one(selector: string, root: ParentNode = document): HTMLElement {
  const matches = root.querySelectorAll<HTMLElement>(selector)
  expect(matches, selector).toHaveLength(1)
  return matches[0]
}

const columnNames = [
  'Gelen kutusu',
  'Satın alım',
  'Araştırma',
  'İletişim',
  'Ödeme',
  'Randevu & Etkinlik',
  'İş',
  'Ev & Kişisel',
  'Diğer',
]

const columnKeys = [
  'inbox',
  'purchase',
  'research',
  'communication',
  'payment',
  'appointment',
  'work',
  'home',
  'other',
]

describe('board', () => {
  it('shows "Yükleniyor..." and then one column per category with counts', async () => {
    const fetchMock = mockBoard([
      makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox }),
      makeTodo({ id: 2, title: 'Süt al', category_id: cat.purchase }),
    ])

    render(<App />)

    expect(screen.getByRole('heading', { level: 1, name: "Todo'lar" })).toBeInTheDocument()
    expect(screen.getByText('Yükleniyor...')).toBeInTheDocument()
    // Categories and todos are requested in parallel, before either reply arrives.
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(['/api/categories', '/api/todos'])

    const headings = await screen.findAllByRole('heading', { level: 2 })
    expect(headings.map((heading) => heading.textContent)).toEqual([
      'Gelen kutusu (1)',
      'Satın alım (1)',
      'Araştırma (0)',
      'İletişim (0)',
      'Ödeme (0)',
      'Randevu & Etkinlik (0)',
      'İş (0)',
      'Ev & Kişisel (0)',
      'Diğer (0)',
    ])
    expect(screen.queryByText('Yükleniyor...')).not.toBeInTheDocument()
  })

  it('renders 9 columns in order with Gelen kutusu first', async () => {
    mockBoard()

    render(<App />)
    await screen.findByRole('region', { name: 'Gelen kutusu' })

    // One column per category, in the order of their positions.
    expect(screen.getAllByRole('region').map((region) => region.getAttribute('aria-label'))).toEqual(columnNames)
    expect(screen.getAllByRole('heading', { level: 2 }).map((heading) => heading.textContent)).toEqual(
      columnNames.map((name) => `${name} (0)`),
    )
    const columns = Array.from(one('.board').querySelectorAll<HTMLElement>(':scope > .column'))
    expect(columns.map((element) => element.dataset.key)).toEqual(columnKeys)
    expect(columns[0]).toBe(column('Gelen kutusu'))
  })

  it('puts each todo in its column in API order and shows "Boş" in empty columns', async () => {
    // The API answers by column, then position; the board must not re-sort.
    mockBoard([
      makeTodo({ id: 4, title: 'Dördüncü', category_id: cat.inbox }),
      makeTodo({ id: 3, title: 'Üçüncü', category_id: cat.research }),
      makeTodo({ id: 2, title: 'İkinci', category_id: cat.inbox }),
      makeTodo({ id: 1, title: 'Birinci', category_id: cat.inbox }),
    ])

    render(<App />)
    await screen.findByRole('region', { name: 'Gelen kutusu' })

    expect(cardTitles('Gelen kutusu')).toEqual(['Dördüncü', 'İkinci', 'Birinci'])
    expect(cardTitles('Araştırma')).toEqual(['Üçüncü'])

    const empty = column('Satın alım')
    expect(within(empty).getByText('Boş')).toBeInTheDocument()
    expect(within(empty).queryAllByRole('listitem')).toHaveLength(0)
    expect(within(column('Gelen kutusu')).queryByText('Boş')).not.toBeInTheDocument()
  })

  it('shows "Sunucuya ulaşılamadı." and reloads on "Tekrar dene"', async () => {
    let todoRequests = 0
    mockBoard([], {
      'GET /api/todos': () => {
        todoRequests += 1
        if (todoRequests === 1) {
          return { status: 500, body: { error: 'internal server error' } }
        }
        return { body: [makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox })] }
      },
    })
    const user = userEvent.setup()

    render(<App />)

    expect(await screen.findByRole('alert')).toHaveTextContent(/^Sunucuya ulaşılamadı\.$/)
    expect(screen.queryByRole('region')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Tekrar dene' }))

    expect(await screen.findByRole('heading', { name: 'Gelen kutusu (1)' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Tekrar dene' })).not.toBeInTheDocument()
    expect(cardTitles('Gelen kutusu')).toEqual(['Fatura öde'])
    expect(todoRequests).toBe(2)
  })
})

describe('add form', () => {
  it('has no category selector in the add form', async () => {
    mockBoard()
    render(<App />)

    await screen.findByLabelText('Todo başlığı')

    // The AI picks the column. The only select left in the form is the status.
    expect(screen.queryByLabelText('Sütun')).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/kategori/i)).not.toBeInTheDocument()
    const form = one('form.add-form')
    expect(within(form).getAllByRole('combobox')).toHaveLength(1)
    expect(screen.getByLabelText('Todo başlığı')).toHaveAttribute('placeholder', 'Yeni todo yaz...')
  })

  it('offers the three statuses with Yapılacak selected', async () => {
    mockBoard()
    render(<App />)

    const select = await screen.findByLabelText('Durum')

    expect(within(select).getAllByRole('option').map((option) => option.textContent)).toEqual([
      'Yapılacak',
      'Devam ediyor',
      'Tamamlandı',
    ])
    expect(select).toHaveValue('todo')
  })

  it('adds a todo to Gelen kutusu with a trimmed title and the default status', async () => {
    const post = vi.fn<Handler>(() => ({
      status: 201,
      body: makeTodo({ id: 5, title: 'Fatura öde', classification: 'pending' }),
    }))
    mockBoard([makeTodo({ id: 1, title: 'Eski', category_id: cat.inbox })], { 'POST /api/todos': post })
    const user = userEvent.setup()
    render(<App />)

    const input = await screen.findByLabelText('Todo başlığı')
    await user.type(input, '  Fatura öde  ')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))

    expect(await screen.findByRole('heading', { name: 'Gelen kutusu (2)' })).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
    // No category and no description: the server puts the todo in Gelen kutusu itself.
    expect(post.mock.calls[0][0]).toEqual({ title: 'Fatura öde', status: 'todo' })
    // The new todo is put on top of Gelen kutusu.
    expect(cardTitles('Gelen kutusu')).toEqual(['Fatura öde', 'Eski'])
    expect(input).toHaveValue('')
  })

  it('adds a todo with description and status', async () => {
    const post = vi.fn<Handler>(() => ({
      status: 201,
      body: makeTodo({
        id: 5,
        title: 'Süt al',
        description: '2 litre',
        status: 'done',
        classification: 'pending',
      }),
    }))
    mockBoard([makeTodo({ id: 1, title: 'Eski', category_id: cat.inbox })], { 'POST /api/todos': post })
    const user = userEvent.setup()
    render(<App />)

    await user.type(await screen.findByLabelText('Todo başlığı'), 'Süt al')
    // The description field is closed until it is asked for.
    expect(screen.queryByLabelText('Açıklama')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Açıklama ekle' }))
    const description = screen.getByLabelText('Açıklama')
    expect(description).toHaveAttribute('placeholder', 'Açıklama (isteğe bağlı)')
    expect(description).toHaveFocus()
    await user.type(description, '2 litre')
    await user.selectOptions(screen.getByLabelText('Durum'), 'Tamamlandı')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))

    expect(await screen.findByRole('heading', { name: 'Gelen kutusu (2)' })).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post.mock.calls[0][0]).toEqual({ title: 'Süt al', description: '2 litre', status: 'done' })
    // On top of Gelen kutusu, waiting for the AI, with what was typed.
    expect(cardTitles('Gelen kutusu')).toEqual(['Süt al', 'Eski'])
    const added = card('Süt al')
    expect(within(added).getByText('sınıflandırılıyor…')).toBeInTheDocument()
    expect(within(added).getByText('2 litre', { selector: '.card-description' })).toHaveClass('card-description')
    expect(added).toHaveAttribute('data-status', 'done')
  })

  it('clears title and description after adding, closes the description field and keeps the status', async () => {
    mockBoard([], {
      'POST /api/todos': () => ({
        status: 201,
        body: makeTodo({ id: 5, title: 'Süt al', description: '2 litre', status: 'done', classification: 'pending' }),
      }),
    })
    const user = userEvent.setup()
    render(<App />)

    await user.type(await screen.findByLabelText('Todo başlığı'), 'Süt al')
    await user.click(screen.getByRole('button', { name: 'Açıklama ekle' }))
    await user.type(screen.getByLabelText('Açıklama'), '2 litre')
    await user.selectOptions(screen.getByLabelText('Durum'), 'Tamamlandı')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))
    await screen.findByRole('heading', { name: 'Gelen kutusu (1)' })

    expect(screen.getByLabelText('Todo başlığı')).toHaveValue('')
    // Gone with the todo it described; "Açıklama ekle" is back for the next one.
    expect(screen.queryByLabelText('Açıklama')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Açıklama ekle' })).toBeInTheDocument()
    // The chosen status is kept for the next todo, like the chosen column was before.
    expect(screen.getByLabelText('Durum')).toHaveValue('done')
  })

  it('trims the description and leaves it out of the request when it is blank', async () => {
    const post = vi.fn<Handler>(() => ({
      status: 201,
      body: makeTodo({ id: 5, title: 'Fatura öde', classification: 'pending' }),
    }))
    mockBoard([], { 'POST /api/todos': post })
    const user = userEvent.setup()
    render(<App />)

    await user.type(await screen.findByLabelText('Todo başlığı'), 'Fatura öde')
    await user.click(screen.getByRole('button', { name: 'Açıklama ekle' }))
    await user.type(screen.getByLabelText('Açıklama'), '   ')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))
    await screen.findByRole('heading', { name: 'Gelen kutusu (1)' })
    expect(post.mock.calls[0][0]).toEqual({ title: 'Fatura öde', status: 'todo' })

    await user.type(screen.getByLabelText('Todo başlığı'), 'Süt al')
    await user.click(screen.getByRole('button', { name: 'Açıklama ekle' }))
    await user.type(screen.getByLabelText('Açıklama'), '  2 litre  ')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
    expect(post.mock.calls[1][0]).toEqual({ title: 'Süt al', description: '2 litre', status: 'todo' })
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

    reply.resolve({ status: 201, body: makeTodo({ id: 5, title: 'Fatura öde', classification: 'pending' }) })
    expect(await screen.findByRole('heading', { name: 'Gelen kutusu (1)' })).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
    expect(cardTitles('Gelen kutusu')).toEqual(['Fatura öde'])
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

    reply.resolve({ status: 201, body: makeTodo({ id: 5, title: 'Fatura öde', classification: 'pending' }) })
    expect(await screen.findByRole('heading', { name: 'Gelen kutusu (1)' })).toBeInTheDocument()
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('shows "Todo eklenemedi: <detay>" and keeps what was typed on failure', async () => {
    mockBoard([], {
      'POST /api/todos': () => ({ status: 400, body: { error: 'title is required' } }),
    })
    const user = userEvent.setup()
    render(<App />)

    const input = await screen.findByLabelText('Todo başlığı')
    await user.type(input, 'Fatura öde')
    await user.click(screen.getByRole('button', { name: 'Açıklama ekle' }))
    await user.type(screen.getByLabelText('Açıklama'), 'Elektrik')
    await user.selectOptions(screen.getByLabelText('Durum'), 'Devam ediyor')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/^Todo eklenemedi: title is required$/)
    expect(input).toHaveValue('Fatura öde')
    expect(screen.getByLabelText('Açıklama')).toHaveValue('Elektrik')
    expect(screen.getByLabelText('Durum')).toHaveValue('in_progress')
    expect(cardTitles('Gelen kutusu')).toEqual([])
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
        return { status: 201, body: makeTodo({ id: 5, title: 'Fatura öde', classification: 'pending' }) }
      },
    })
    const user = userEvent.setup()
    render(<App />)

    const input = await screen.findByLabelText('Todo başlığı')
    await user.type(input, 'Fatura öde')
    await user.click(screen.getByRole('button', { name: 'Ekle' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Todo eklenemedi: boom')

    await user.click(screen.getByRole('button', { name: 'Ekle' }))

    expect(await screen.findByRole('heading', { name: 'Gelen kutusu (1)' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(input).toHaveValue('')
  })
})

describe('card status', () => {
  it('changes a card status', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: makeTodo({ id: 1, title: 'Fatura öde', status: 'in_progress' }),
    }))
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde' })], { 'PATCH /api/todos/1': patch })
    const user = userEvent.setup()
    render(<App />)

    const select = await screen.findByLabelText('Fatura öde için durum')
    expect(within(select).getAllByRole('option').map((option) => option.textContent)).toEqual([
      'Yapılacak',
      'Devam ediyor',
      'Tamamlandı',
    ])
    expect(select).toHaveValue('todo')
    expect(card('Fatura öde')).toHaveAttribute('data-status', 'todo')

    await user.selectOptions(select, 'Devam ediyor')

    await waitFor(() => expect(card('Fatura öde')).toHaveAttribute('data-status', 'in_progress'))
    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch.mock.calls[0][0]).toEqual({ status: 'in_progress' })
    expect(screen.getByLabelText('Fatura öde için durum')).toHaveValue('in_progress')
    // Only the status changed: the card is where it was.
    expect(cardTitles('Gelen kutusu')).toEqual(['Fatura öde'])
  })

  it('shows the chosen status while it is saved and applies it once the server has answered', async () => {
    const reply = deferred<MockReply>()
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde' })], { 'PATCH /api/todos/1': () => reply.promise })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için durum'), 'Tamamlandı')

    // The request is open: the choice is shown and locked, but the card still says what the
    // server said (a status change is not applied before the answer).
    const select = screen.getByLabelText('Fatura öde için durum')
    expect(select).toHaveValue('done')
    expect(select).toBeDisabled()
    expect(card('Fatura öde')).toHaveAttribute('data-status', 'todo')

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', status: 'done' }) })

    await waitFor(() => expect(card('Fatura öde')).toHaveAttribute('data-status', 'done'))
    expect(select).toBeEnabled()
    expect(select).toHaveValue('done')
  })

  it('puts a card on top of its new column when the answer says the AI has classified it meanwhile', async () => {
    // The card is last in the list (it was added to an empty Gelen kutusu), after the one that
    // is in Satın alım already.
    mockBoard(
      [
        makeTodo({ id: 2, title: 'Kulaklık', category_id: cat.purchase }),
        makeTodo({ id: 1, title: 'Süt al', category_id: cat.inbox, classification: 'pending' }),
      ],
      {
        'PATCH /api/todos/1': () => ({
          body: makeTodo({
            id: 1,
            title: 'Süt al',
            category_id: cat.purchase,
            position: 0,
            status: 'done',
            classification: 'done',
          }),
        }),
      },
    )
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Süt al için durum'), 'Tamamlandı')

    await waitFor(() => expect(cardTitles('Satın alım')).toEqual(['Süt al', 'Kulaklık']))
    expect(cardTitles('Gelen kutusu')).toEqual([])
    expect(card('Süt al')).toHaveAttribute('data-status', 'done')
    expect(card('Süt al').querySelector('.badge')).toBeNull()
  })

  it('shows an error and keeps the old status when the change fails', async () => {
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', status: 'in_progress' })], {
      'PATCH /api/todos/1': () => ({ status: 500, body: { error: 'boom' } }),
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için durum'), 'Tamamlandı')

    const alert = await within(card('Fatura öde')).findByRole('alert')
    expect(alert).toHaveTextContent(/^Durum değiştirilemedi: boom$/)
    expect(alert).toHaveClass('alert')
    const select = screen.getByLabelText('Fatura öde için durum')
    expect(select).toHaveValue('in_progress')
    expect(select).toBeEnabled()
    expect(card('Fatura öde')).toHaveAttribute('data-status', 'in_progress')
  })

  it('clears the status error once the next change succeeds', async () => {
    let patches = 0
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde' })], {
      'PATCH /api/todos/1': () => {
        patches += 1
        if (patches === 1) return { status: 500, body: { error: 'boom' } }
        return { body: makeTodo({ id: 1, title: 'Fatura öde', status: 'done' }) }
      },
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için durum'), 'Tamamlandı')
    expect(await screen.findByRole('alert')).toHaveTextContent('Durum değiştirilemedi: boom')

    await user.selectOptions(screen.getByLabelText('Fatura öde için durum'), 'Tamamlandı')

    await waitFor(() => expect(card('Fatura öde')).toHaveAttribute('data-status', 'done'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('card description', () => {
  it('shows the description under the title, only for a todo that has one', async () => {
    mockBoard([
      makeTodo({ id: 1, title: 'Fatura öde', description: 'Elektrik ve su' }),
      makeTodo({ id: 2, title: 'Süt al' }),
    ])
    render(<App />)
    await screen.findByRole('region', { name: 'Gelen kutusu' })

    const description = within(card('Fatura öde')).getByText('Elektrik ve su')
    expect(description).toHaveClass('card-description')
    expect(description.previousElementSibling).toBe(within(card('Fatura öde')).getByRole('heading'))
    expect(card('Süt al').querySelector('.card-description')).toBeNull()
  })

  it('edits a description', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: makeTodo({ id: 1, title: 'Fatura öde', description: 'yeni açıklama' }),
    }))
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', description: 'eski' })], { 'PATCH /api/todos/1': patch })
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Fatura öde' })
    const target = card('Fatura öde')

    await user.click(within(target).getByRole('button', { name: 'Açıklamayı düzenle' }))

    // The text area takes the place of the text, with what was there and the focus.
    const field = within(target).getByLabelText('Fatura öde açıklaması')
    expect(field).toHaveValue('eski')
    expect(field).toHaveFocus()
    expect(within(target).queryByText('eski', { selector: '.card-description' })).not.toBeInTheDocument()
    await user.clear(field)
    await user.type(field, 'yeni açıklama')
    await user.click(within(target).getByRole('button', { name: 'Kaydet' }))

    await waitFor(() => expect(within(target).queryByLabelText('Fatura öde açıklaması')).not.toBeInTheDocument())
    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch.mock.calls[0][0]).toEqual({ description: 'yeni açıklama' })
    expect(within(target).getByText('yeni açıklama', { selector: '.card-description' })).toHaveClass('card-description')
    // The keyboard user is back where they started.
    expect(within(target).getByRole('button', { name: 'Açıklamayı düzenle' })).toHaveFocus()
  })

  it('adds a description to a todo that has none', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: makeTodo({ id: 1, title: 'Süt al', description: '2 litre' }),
    }))
    mockBoard([makeTodo({ id: 1, title: 'Süt al' })], { 'PATCH /api/todos/1': patch })
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Süt al' })

    await user.click(within(card('Süt al')).getByRole('button', { name: 'Açıklamayı düzenle' }))
    const field = within(card('Süt al')).getByLabelText('Süt al açıklaması')
    expect(field).toHaveValue('')
    await user.type(field, '  2 litre  ')
    await user.click(within(card('Süt al')).getByRole('button', { name: 'Kaydet' }))

    expect(await within(card('Süt al')).findByText('2 litre', { selector: '.card-description' })).toHaveClass('card-description')
    // Trimmed, like the server stores it.
    expect(patch.mock.calls[0][0]).toEqual({ description: '2 litre' })
  })

  it('sends nothing when the edit is cancelled with Vazgeç', async () => {
    // No PATCH route: a request would fail the test.
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', description: 'eski' })])
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Fatura öde' })

    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Açıklamayı düzenle' }))
    await user.type(within(card('Fatura öde')).getByLabelText('Fatura öde açıklaması'), ' ve daha fazlası')
    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Vazgeç' }))

    expect(within(card('Fatura öde')).queryByLabelText('Fatura öde açıklaması')).not.toBeInTheDocument()
    expect(within(card('Fatura öde')).getByText('eski', { selector: '.card-description' })).toHaveClass('card-description')
    expect(within(card('Fatura öde')).getByRole('button', { name: 'Açıklamayı düzenle' })).toHaveFocus()
    // Opening the editor again starts from the saved text, not from the abandoned one.
    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Açıklamayı düzenle' }))
    expect(within(card('Fatura öde')).getByLabelText('Fatura öde açıklaması')).toHaveValue('eski')
  })

  it('closes the editor without a request when the text did not change', async () => {
    // No PATCH route: a request would fail the test.
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', description: 'eski' })])
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Fatura öde' })

    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Açıklamayı düzenle' }))
    await user.type(within(card('Fatura öde')).getByLabelText('Fatura öde açıklaması'), '  ')
    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Kaydet' }))

    expect(within(card('Fatura öde')).queryByLabelText('Fatura öde açıklaması')).not.toBeInTheDocument()
    expect(within(card('Fatura öde')).getByText('eski', { selector: '.card-description' })).toBeInTheDocument()
  })

  it('removes the description when it is saved empty', async () => {
    const patch = vi.fn<Handler>(() => ({ body: makeTodo({ id: 1, title: 'Fatura öde', description: '' }) }))
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', description: 'eski' })], { 'PATCH /api/todos/1': patch })
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Fatura öde' })

    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Açıklamayı düzenle' }))
    await user.clear(within(card('Fatura öde')).getByLabelText('Fatura öde açıklaması'))
    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Kaydet' }))

    await waitFor(() => expect(card('Fatura öde').querySelector('.description-editor')).toBeNull())
    expect(patch.mock.calls[0][0]).toEqual({ description: '' })
    expect(card('Fatura öde').querySelector('.card-description')).toBeNull()
  })

  it('keeps the editor open with the text and shows the error when saving fails', async () => {
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', description: 'eski' })], {
      'PATCH /api/todos/1': () => ({ status: 400, body: { error: 'description must be at most 2000 characters' } }),
    })
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Fatura öde' })

    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Açıklamayı düzenle' }))
    await user.type(within(card('Fatura öde')).getByLabelText('Fatura öde açıklaması'), ' uzun')
    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Kaydet' }))

    const alert = await within(card('Fatura öde')).findByRole('alert')
    expect(alert).toHaveTextContent(/^Açıklama kaydedilemedi: description must be at most 2000 characters$/)
    // Nothing is lost, and the buttons work again.
    expect(within(card('Fatura öde')).getByLabelText('Fatura öde açıklaması')).toHaveValue('eski uzun')
    expect(within(card('Fatura öde')).getByRole('button', { name: 'Kaydet' })).toBeEnabled()
    expect(within(card('Fatura öde')).getByRole('button', { name: 'Vazgeç' })).toBeEnabled()
  })

  it('locks the editor while the description is saved', async () => {
    const reply = deferred<MockReply>()
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', description: 'eski' })], {
      'PATCH /api/todos/1': () => reply.promise,
    })
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Fatura öde' })

    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Açıklamayı düzenle' }))
    await user.type(within(card('Fatura öde')).getByLabelText('Fatura öde açıklaması'), ' yeni')
    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Kaydet' }))

    expect(within(card('Fatura öde')).getByRole('button', { name: 'Kaydet' })).toBeDisabled()
    expect(within(card('Fatura öde')).getByRole('button', { name: 'Vazgeç' })).toBeDisabled()
    expect(within(card('Fatura öde')).getByLabelText('Fatura öde açıklaması')).toBeDisabled()

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', description: 'eski yeni' }) })
    expect(await within(card('Fatura öde')).findByText('eski yeni', { selector: '.card-description' })).toBeInTheDocument()
  })
})

describe('classification badges', () => {
  it('shows badges and retries a failed classification', async () => {
    // What the server holds; a card is pending, so a poll may ask for it, and it must hear the truth.
    let stored = [
      makeTodo({ id: 1, title: 'Süt al', position: 0, classification: 'pending' }),
      makeTodo({ id: 2, title: 'Bilet al', position: 1, classification: 'failed' }),
      makeTodo({ id: 3, title: 'Kulaklık', category_id: cat.purchase, classification: 'done' }),
    ]
    const retried = makeTodo({ id: 2, title: 'Bilet al', position: 0, classification: 'pending' })
    const post = vi.fn<Handler>(() => {
      stored = [retried, { ...stored[0], position: 1 }, stored[2]]
      return { status: 202, body: retried }
    })
    mockBoard([], {
      'GET /api/todos': () => ({ body: stored }),
      'POST /api/todos/2/classify': post,
    })
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('region', { name: 'Gelen kutusu' })

    // Waiting for the AI.
    const waiting = card('Süt al')
    expect(waiting).toHaveAttribute('data-classification', 'pending')
    expect(within(waiting).getByText('sınıflandırılıyor…')).toHaveClass('badge', 'badge-pending')
    expect(within(waiting).queryByRole('button', { name: /tekrar dene/i })).not.toBeInTheDocument()
    // Failed: the badge, and a retry button named after the card.
    const failed = card('Bilet al')
    expect(failed).toHaveAttribute('data-classification', 'failed')
    expect(within(failed).getByText('sınıflandırılamadı')).toHaveClass('badge', 'badge-failed')
    const retry = within(failed).getByRole('button', { name: 'Bilet al için tekrar dene' })
    expect(retry).toHaveTextContent(/^Tekrar dene$/)
    expect(retry).toHaveClass('retry-button')
    // A settled card has no badge at all.
    expect(card('Kulaklık').querySelector('.badge, .retry-button')).toBeNull()
    expect(cardTitles('Gelen kutusu')).toEqual(['Süt al', 'Bilet al'])

    await user.click(retry)

    // The server has put it back on top of Gelen kutusu, waiting again.
    await waitFor(() => expect(card('Bilet al')).toHaveAttribute('data-classification', 'pending'))
    expect(post).toHaveBeenCalledTimes(1)
    expect(cardTitles('Gelen kutusu')).toEqual(['Bilet al', 'Süt al'])
    expect(within(card('Bilet al')).getByText('sınıflandırılıyor…')).toBeInTheDocument()
    expect(within(card('Bilet al')).queryByText('sınıflandırılamadı')).not.toBeInTheDocument()
    expect(within(card('Bilet al')).queryByRole('button', { name: /tekrar dene/i })).not.toBeInTheDocument()
  })

  it('shows "Yeniden sınıflandırma başlatılamadı: <detay>" and keeps the card when the retry fails', async () => {
    mockBoard([makeTodo({ id: 2, title: 'Bilet al', classification: 'failed' })], {
      'POST /api/todos/2/classify': () => ({ status: 404, body: { error: 'todo not found' } }),
    })
    const user = userEvent.setup()
    render(<App />)

    await user.click(await screen.findByRole('button', { name: 'Bilet al için tekrar dene' }))

    const alert = await within(card('Bilet al')).findByRole('alert')
    expect(alert).toHaveTextContent(/^Yeniden sınıflandırma başlatılamadı: todo not found$/)
    expect(card('Bilet al')).toHaveAttribute('data-classification', 'failed')
    expect(screen.getByRole('button', { name: 'Bilet al için tekrar dene' })).toBeEnabled()
  })

  it('turns the retry button off while the request is open', async () => {
    const reply = deferred<MockReply>()
    const post = vi.fn<Handler>(() => reply.promise)
    mockBoard([makeTodo({ id: 2, title: 'Bilet al', classification: 'failed' })], {
      'POST /api/todos/2/classify': post,
    })
    const user = userEvent.setup()
    render(<App />)

    const retry = await screen.findByRole('button', { name: 'Bilet al için tekrar dene' })
    await user.click(retry)
    await user.click(retry)

    expect(retry).toBeDisabled()
    expect(post).toHaveBeenCalledTimes(1)

    reply.resolve({ status: 202, body: makeTodo({ id: 2, title: 'Bilet al', classification: 'pending' }) })
    await waitFor(() => expect(card('Bilet al')).toHaveAttribute('data-classification', 'pending'))
  })
})

describe('moving cards', () => {
  it('shows a category select on every card with the current category selected', async () => {
    mockBoard([
      makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox }),
      makeTodo({ id: 2, title: 'Süt al', category_id: cat.purchase }),
    ])
    render(<App />)

    const first = await screen.findByLabelText('Fatura öde için kategori')
    expect(first).toHaveValue(String(cat.inbox))
    expect(within(first).getAllByRole('option').map((option) => option.textContent)).toEqual(columnNames)
    expect(screen.getByLabelText('Süt al için kategori')).toHaveValue(String(cat.purchase))
    // The old column select is gone.
    expect(screen.queryByLabelText('Fatura öde için sütun')).not.toBeInTheDocument()
  })

  it('moves a card with its category selector to the top of the target', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.purchase, position: 0, classification: 'manual' }),
    }))
    mockBoard(
      [
        makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox }),
        makeTodo({ id: 2, title: 'Yeni', category_id: cat.purchase }),
      ],
      { 'PATCH /api/todos/1': patch },
    )
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için kategori'), 'Satın alım')
    await movesSettled()

    expect(patch).toHaveBeenCalledTimes(1)
    // A <select> value is a string; the API wants numbers. Position 0 is the top of the column.
    expect(patch.mock.calls[0][0]).toEqual({ category_id: cat.purchase, position: 0 })
    expect(cardTitles('Satın alım')).toEqual(['Fatura öde', 'Yeni'])
    expect(cardTitles('Gelen kutusu')).toEqual([])
    expect(screen.getByRole('heading', { name: 'Satın alım (2)' })).toBeInTheDocument()
    expect(screen.getByLabelText('Fatura öde için kategori')).toHaveValue(String(cat.purchase))
    // The server's copy replaced the local one: the user's choice is a manual classification.
    expect(card('Fatura öde')).toHaveAttribute('data-classification', 'manual')
  })

  it('shows the move before the server answers', async () => {
    const reply = deferred<MockReply>()
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox })], {
      'PATCH /api/todos/1': () => reply.promise,
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için kategori'), 'Satın alım')

    // The PATCH is still open, yet the card is already in its new column.
    expect(cardTitles('Satın alım')).toEqual(['Fatura öde'])
    expect(cardTitles('Gelen kutusu')).toEqual([])
    expect(screen.getByRole('heading', { name: 'Satın alım (1)' })).toBeInTheDocument()
    expect(screen.getByLabelText('Fatura öde için kategori')).toHaveValue(String(cat.purchase))

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.purchase, position: 0 }) })
    await movesSettled()

    expect(cardTitles('Satın alım')).toEqual(['Fatura öde'])
  })

  it('disables every card select while a move is saving', async () => {
    const reply = deferred<MockReply>()
    mockBoard(
      [
        makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox }),
        makeTodo({ id: 2, title: 'Süt al', category_id: cat.research }),
        makeTodo({ id: 3, title: 'Yoğurt al', category_id: cat.research }),
      ],
      { 'PATCH /api/todos/1': () => reply.promise },
    )
    const user = userEvent.setup()
    render(<App />)

    const categorySelects = await screen.findAllByLabelText(/ için kategori$/)
    expect(categorySelects).toHaveLength(3)
    expect(screen.getAllByLabelText(/ için durum$/)).toHaveLength(3)
    for (const select of [...categorySelects, ...screen.getAllByLabelText(/ için durum$/)]) {
      expect(select).toBeEnabled()
    }
    // Cards can be picked up by their handle (and reached with the keyboard).
    for (const element of document.querySelectorAll('.card')) expect(element).toHaveAttribute('tabindex', '0')

    await user.selectOptions(screen.getByLabelText('Fatura öde için kategori'), 'Satın alım')

    // The select that was used, and the ones on the other cards in any column.
    const whileSaving = [...screen.getAllByLabelText(/ için kategori$/), ...screen.getAllByLabelText(/ için durum$/)]
    expect(whileSaving).toHaveLength(6)
    for (const select of whileSaving) expect(select).toBeDisabled()
    // Dragging stays on: a drag handle that goes away would take the keyboard focus with it.
    // A drop that arrives meanwhile is turned away by App (see App.dnd.test.tsx).
    for (const element of document.querySelectorAll('.card')) expect(element).toHaveAttribute('tabindex', '0')

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.purchase, position: 0 }) })
    await movesSettled()

    for (const select of screen.getAllByLabelText(/ için kategori$/)) expect(select).toBeEnabled()
    for (const select of screen.getAllByLabelText(/ için durum$/)) expect(select).toBeEnabled()
  })

  it('turns away a second move while one is being saved', async () => {
    const reply = deferred<MockReply>()
    const patch = vi.fn<Handler>(() => reply.promise)
    // Only the first todo has a PATCH route: a request for the second one fails the test.
    mockBoard(
      [
        makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox }),
        makeTodo({ id: 2, title: 'Süt al', category_id: cat.research }),
      ],
      { 'PATCH /api/todos/1': patch },
    )
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('Fatura öde için kategori'), 'Satın alım')

    // A disabled select takes no input from a user. This change comes from outside, like a
    // drop that ends while the first move is still being saved would.
    const other = screen.getByLabelText('Süt al için kategori')
    expect(other).toBeDisabled()
    fireEvent.change(other, { target: { value: String(cat.purchase) } })

    expect(patch).toHaveBeenCalledTimes(1)
    expect(cardTitles('Araştırma')).toEqual(['Süt al'])
    expect(cardTitles('Satın alım')).toEqual(['Fatura öde'])

    reply.resolve({ body: makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.purchase, position: 0 }) })
    await movesSettled()

    expect(cardTitles('Araştırma')).toEqual(['Süt al'])
    expect(cardTitles('Satın alım')).toEqual(['Fatura öde'])
  })

  it('reverts the move and shows the error above the board on failure', async () => {
    mockBoard(
      [
        makeTodo({ id: 1, title: 'A', category_id: cat.inbox }),
        makeTodo({ id: 2, title: 'B', category_id: cat.inbox }),
      ],
      { 'PATCH /api/todos/2': () => ({ status: 500, body: { error: 'boom' } }) },
    )
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('B için kategori'), 'Diğer')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/^Todo taşınamadı: boom$/)
    expect(alert).toHaveClass('alert')
    // Above the board, not inside a column or a card.
    const board = one('.board')
    expect(board).not.toContainElement(alert)
    expect(alert.compareDocumentPosition(board) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // Everything is back exactly where it was.
    expect(cardTitles('Gelen kutusu')).toEqual(['A', 'B'])
    expect(cardTitles('Diğer')).toEqual([])
    expect(screen.getByRole('heading', { name: 'Gelen kutusu (2)' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Diğer (0)' })).toBeInTheDocument()
    expect(screen.getByLabelText('B için kategori')).toHaveValue(String(cat.inbox))
    // And nothing stays locked.
    for (const select of screen.getAllByLabelText(/ için kategori$/)) expect(select).toBeEnabled()
  })

  it('clears the move error once the next move starts', async () => {
    let patches = 0
    mockBoard([makeTodo({ id: 1, title: 'A', category_id: cat.inbox })], {
      'PATCH /api/todos/1': () => {
        patches += 1
        if (patches === 1) {
          return { status: 500, body: { error: 'boom' } }
        }
        return { body: makeTodo({ id: 1, title: 'A', category_id: cat.other, position: 0 }) }
      },
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('A için kategori'), 'Diğer')
    expect(await screen.findByRole('alert')).toHaveTextContent('Todo taşınamadı: boom')

    await user.selectOptions(screen.getByLabelText('A için kategori'), 'Diğer')
    await movesSettled()

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(cardTitles('Diğer')).toEqual(['A'])
  })

  it('keeps a todo that was added while a move was saving when that move fails', async () => {
    const reply = deferred<MockReply>()
    mockBoard([makeTodo({ id: 1, title: 'A', category_id: cat.inbox })], {
      'PATCH /api/todos/1': () => reply.promise,
      'POST /api/todos': () => ({
        status: 201,
        body: makeTodo({ id: 2, title: 'Yeni', category_id: cat.inbox, classification: 'pending' }),
      }),
    })
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(await screen.findByLabelText('A için kategori'), 'Diğer')
    // The add form is not locked by a saving move.
    await user.type(screen.getByLabelText('Todo başlığı'), 'Yeni{Enter}')
    expect(await screen.findByRole('heading', { name: 'Gelen kutusu (1)' })).toBeInTheDocument()
    expect(cardTitles('Gelen kutusu')).toEqual(['Yeni'])
    expect(cardTitles('Diğer')).toEqual(['A'])

    reply.resolve({ status: 500, body: { error: 'boom' } })

    expect(await screen.findByRole('alert')).toHaveTextContent('Todo taşınamadı: boom')
    // The move is undone, the new todo is still on top of its column, as on the server.
    expect(cardTitles('Gelen kutusu')).toEqual(['Yeni', 'A'])
    expect(cardTitles('Diğer')).toEqual([])
  })

  it('keeps an empty column as a drop target', async () => {
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox })])
    render(<App />)
    await screen.findByRole('region', { name: 'Araştırma' })

    // "Boş" lives inside the drop area, so the area exists while the column is empty.
    const list = one('.card-list', column('Araştırma'))
    expect(within(list).getByText('Boş')).toBeInTheDocument()
    // It is the drag library's drop area for the category, like the one of the full column.
    expect(list).toHaveAttribute('data-rfd-droppable-id', String(cat.research))
    expect(one('.card-list', column('Gelen kutusu'))).toHaveAttribute('data-rfd-droppable-id', String(cat.inbox))
    // A card is draggable under its todo id.
    expect(one('.card', column('Gelen kutusu'))).toHaveAttribute('data-rfd-draggable-id', '1')
  })

  it('renders and moves a card under StrictMode without console warnings', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    const warnings = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mockBoard(
      [
        makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.inbox }),
        makeTodo({ id: 2, title: 'Yeni', category_id: cat.purchase }),
      ],
      {
        'PATCH /api/todos/1': () => ({
          body: makeTodo({ id: 1, title: 'Fatura öde', category_id: cat.purchase, position: 0 }),
        }),
      },
    )
    const user = userEvent.setup()
    render(
      <StrictMode>
        <App />
      </StrictMode>,
    )

    await user.selectOptions(await screen.findByLabelText('Fatura öde için kategori'), 'Satın alım')
    await movesSettled()

    expect(cardTitles('Satın alım')).toEqual(['Fatura öde', 'Yeni'])
    expect(errors).not.toHaveBeenCalled()
    expect(warnings).not.toHaveBeenCalled()
  })
})

describe('log panel', () => {
  it('is offered closed in every state, and asks for no logs', async () => {
    // No logs route: a request for logs would fail the test.
    mockBoard([], { 'GET /api/todos': () => ({ status: 500, body: { error: 'internal server error' } }) })
    render(<App />)

    // While loading, and after the load has failed.
    expect(screen.getByRole('button', { name: 'Loglar' })).toHaveAttribute('aria-expanded', 'false')
    await screen.findByRole('alert')
    expect(screen.getByRole('button', { name: 'Loglar' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('log')).not.toBeInTheDocument()
  })

  it('is closed on the board and opens from the page', async () => {
    mockBoard([makeTodo({ id: 1, title: 'Süt al' })], {
      'GET /api/logs?limit=200': () => ({
        body: {
          entries: [
            { seq: 1, time: '2026-10-25T12:00:00.000Z', level: 'info', source: 'system', message: 'Sunucu başladı (model: x)' },
          ],
          last_seq: 1,
          truncated: false,
        },
      }),
      'GET /api/logs?after=1': () => ({ body: { entries: [], last_seq: 1, truncated: false } }),
    })
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('region', { name: 'Gelen kutusu' })
    expect(screen.queryByRole('log')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Loglar' }))

    const panel = await screen.findByRole('log')
    expect(await within(panel).findByText('Sunucu başladı (model: x)')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Loglar' })).toHaveAttribute('aria-expanded', 'true')

    await user.click(within(panel).getByRole('button', { name: 'Gizle' }))
    expect(screen.queryByRole('log')).not.toBeInTheDocument()
    // The board is still there, untouched.
    expect(cardTitles('Gelen kutusu')).toEqual(['Süt al'])
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
    await screen.findByRole('region', { name: 'Gelen kutusu' })

    expect(document.documentElement.dataset.theme).toBe('skeuo')
    expect(screen.queryByRole('group', { name: 'Tasarım' })).not.toBeInTheDocument()
  })
})

// The classes the theme stylesheets are written against (step 2 spec section 5.3 and step 3 spec
// section 9.6).
describe('markup contract', () => {
  it('renders the markup contract', async () => {
    mockBoard([
      makeTodo({
        id: 1,
        title: 'Fatura öde',
        description: 'Elektrik',
        category_id: cat.inbox,
        status: 'in_progress',
        classification: 'failed',
      }),
      makeTodo({ id: 2, title: 'Süt al', category_id: cat.purchase, status: 'done', classification: 'manual' }),
      makeTodo({ id: 3, title: 'Kitap oku', category_id: cat.inbox, classification: 'pending' }),
    ])
    render(<App />)
    await screen.findByRole('region', { name: 'Gelen kutusu' })

    // Page and header: the title; the theme menu is not rendered while a single theme is left.
    const app = one('main.app')
    const header = one(':scope > .app-header', app)
    expect(one('h1.app-title', header)).toHaveTextContent("Todo'lar")
    expect(header.querySelector('.theme-switcher')).toBeNull()

    // Add form: a title, a status (there is no category), the description toggle, the button.
    const form = one('form.add-form', app)
    expect(one('input.field.add-input', form)).toBe(screen.getByLabelText('Todo başlığı'))
    expect(one('select.field.add-select.add-status', form)).toBe(screen.getByLabelText('Durum'))
    expect(one('button.button.add-button', form)).toBe(screen.getByRole('button', { name: 'Ekle' }))
    expect(one('button.add-description-toggle', form)).toBe(screen.getByRole('button', { name: 'Açıklama ekle' }))
    expect(form.querySelector('.add-description')).toBeNull()

    // Board: the scroll container, and one column per category in order, each with a title and a
    // drop area.
    const board = one('.board', app)
    const columns = Array.from(board.querySelectorAll<HTMLElement>(':scope > .column'))
    expect(columns.map((element) => element.dataset.key)).toEqual(columnKeys)
    expect(columns.map((element) => element.dataset.column)).toEqual(['0', '1', '2', '3', '4', '5', '6', '7', '8'])
    for (const element of columns) {
      expect(one('.column-title', element)).toHaveTextContent(/^.+ \(\d\)$/)
      one('.card-list', element)
    }
    const [inbox, purchase, research] = columns

    // A card: .card > .card-body > .card-title, the description, the badge, the two selects.
    const failed = card('Fatura öde')
    expect(failed.closest('.column')).toBe(inbox)
    expect(failed).toHaveAttribute('data-status', 'in_progress')
    expect(failed).toHaveAttribute('data-classification', 'failed')
    expect(one(':scope > .card-body > .card-title', failed)).toHaveTextContent('Fatura öde')
    expect(one(':scope > .card-body > .card-description', failed)).toHaveTextContent('Elektrik')
    expect(one(':scope > .card-body .badge.badge-failed', failed)).toHaveTextContent('sınıflandırılamadı')
    expect(one(':scope > .card-body button.retry-button', failed)).toBe(
      screen.getByRole('button', { name: 'Fatura öde için tekrar dene' }),
    )
    expect(one(':scope > .card-body > select.card-select.card-status', failed)).toBe(
      screen.getByLabelText('Fatura öde için durum'),
    )
    expect(one(':scope > .card-body > select.card-select.card-category', failed)).toBe(
      screen.getByLabelText('Fatura öde için kategori'),
    )
    expect(failed.querySelector('.badge-pending')).toBeNull()

    const waiting = card('Kitap oku')
    expect(waiting).toHaveAttribute('data-status', 'todo')
    expect(waiting).toHaveAttribute('data-classification', 'pending')
    expect(one(':scope > .card-body .badge.badge-pending', waiting)).toHaveTextContent('sınıflandırılıyor…')
    expect(waiting.querySelector('.badge-failed, .retry-button, .card-description')).toBeNull()

    const settled = card('Süt al')
    expect(settled.closest('.column')).toBe(purchase)
    expect(settled).toHaveAttribute('data-status', 'done')
    expect(settled).toHaveAttribute('data-classification', 'manual')
    expect(settled.querySelector('.badge, .retry-button')).toBeNull()
    expect(one('.card-list .card .card-title', purchase)).toHaveTextContent('Süt al')

    // The empty column keeps its drop area, and "Boş" sits inside it.
    expect(one('.card-list .empty', research)).toHaveTextContent('Boş')
    expect(research.querySelector('.card')).toBeNull()

    // Nothing left over from the other states.
    expect(document.querySelector('.status, .alert')).toBeNull()
  })

  it('renders the description field of the add form', async () => {
    mockBoard()
    const user = userEvent.setup()
    render(<App />)

    await user.click(await screen.findByRole('button', { name: 'Açıklama ekle' }))

    const form = one('form.add-form')
    expect(one('textarea.field.add-description', form)).toBe(screen.getByLabelText('Açıklama'))
    // The toggle has done its job.
    expect(form.querySelector('.add-description-toggle')).toBeNull()
  })

  it('renders the description editor of a card', async () => {
    mockBoard([makeTodo({ id: 1, title: 'Fatura öde', description: 'eski' })])
    const user = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Fatura öde' })

    await user.click(within(card('Fatura öde')).getByRole('button', { name: 'Açıklamayı düzenle' }))

    const editor = one(':scope > .card-body > .description-editor', card('Fatura öde'))
    expect(one('textarea', editor)).toBe(screen.getByLabelText('Fatura öde açıklaması'))
    expect(within(editor).getByRole('button', { name: 'Kaydet' })).toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Vazgeç' })).toBeInTheDocument()
    // The text is in the editor now, not under the title as well.
    expect(card('Fatura öde').querySelector('.card-description')).toBeNull()
  })

  it('marks the loading text with the status class', async () => {
    mockBoard()
    render(<App />)

    expect(screen.getByText('Yükleniyor...')).toHaveClass('status')

    await screen.findByRole('region', { name: 'Gelen kutusu' })
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
