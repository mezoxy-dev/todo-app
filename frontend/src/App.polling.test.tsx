import { fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Mock } from 'vitest'
import App from './App'
import type { Todo } from './api'
import { advance, card, cardTitles, deferred, mockBoard } from './test/appHelpers'
import type { Handler } from './test/appHelpers'
import { cat, makeTodo } from './test/mockApi'
import type { MockReply } from './test/mockApi'

// The polling is a matter of seconds, so time is the test's to move. A test reads the page after
// advance(), which also lets the replies of the mocked fetch settle.
beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

// "Süt al" waits for the AI in Gelen kutusu; the AI puts it into Satın alım, on top of "Kulaklık".
const waiting = makeTodo({ id: 1, title: 'Süt al', category_id: cat.inbox, classification: 'pending' })
const headphones = makeTodo({ id: 2, title: 'Kulaklık', category_id: cat.purchase })
const placed = makeTodo({ id: 1, title: 'Süt al', category_id: cat.purchase, position: 0, classification: 'done' })

// A route that answers with the given lists in turn and with the last one from then on.
function lists(...answers: Todo[][]): Handler {
  let asked = 0
  return () => ({ body: answers[Math.min(asked++, answers.length - 1)] })
}

// How many times the list of todos has been asked for (the POST of a new todo has the same path).
function listRequests(fetchMock: Mock): number {
  return fetchMock.mock.calls.filter(([url, init]) => url === '/api/todos' && init?.method === 'GET').length
}

// Picks an option in a select as the user would. user-event is not used in this file: it waits for a
// timer of its own, which the fake time never fires, so the events are sent by hand.
function choose(select: HTMLElement, label: string) {
  const option = within(select).getByRole<HTMLOptionElement>('option', { name: label })
  fireEvent.change(select, { target: { value: option.value } })
}

describe('polling for pending cards', () => {
  it('polls every second while a card is pending and moves it when classified', async () => {
    const fetchMock = mockBoard([], {
      'GET /api/todos': lists([waiting, headphones], [placed, { ...headphones, position: 1 }]),
    })

    render(<App />)
    await advance()

    expect(cardTitles('Gelen kutusu')).toEqual(['Süt al'])
    expect(within(card('Süt al')).getByText('sınıflandırılıyor…')).toBeInTheDocument()
    expect(listRequests(fetchMock)).toBe(1)

    // Not before a second has passed.
    await advance(999)
    expect(listRequests(fetchMock)).toBe(1)
    expect(cardTitles('Gelen kutusu')).toEqual(['Süt al'])

    await advance(1)
    expect(listRequests(fetchMock)).toBe(2)

    // On top of Satın alım, with the server's copy: no badge any more.
    expect(cardTitles('Satın alım')).toEqual(['Süt al', 'Kulaklık'])
    expect(cardTitles('Gelen kutusu')).toEqual([])
    expect(screen.getByRole('heading', { name: 'Satın alım (2)' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Gelen kutusu (0)' })).toBeInTheDocument()
    expect(card('Süt al')).toHaveAttribute('data-classification', 'done')
    expect(card('Süt al').querySelector('.badge')).toBeNull()
  })

  it('stops polling when nothing is pending', async () => {
    const fetchMock = mockBoard([], { 'GET /api/todos': lists([waiting, headphones], [placed, headphones]) })
    render(<App />)
    await advance()

    await advance(1000)
    expect(cardTitles('Satın alım')).toEqual(['Süt al', 'Kulaklık'])
    const asked = listRequests(fetchMock)

    await advance(3000)

    expect(listRequests(fetchMock)).toBe(asked)
  })

  it('does not poll at all while nothing is pending', async () => {
    const fetchMock = mockBoard([headphones, makeTodo({ id: 3, title: 'Bilet al', classification: 'failed' })])
    render(<App />)
    await advance()

    await advance(5000)

    expect(listRequests(fetchMock)).toBe(1)
  })

  it('keeps a failed card in Gelen kutusu with its badge', async () => {
    const failed = makeTodo({ id: 1, title: 'Süt al', category_id: cat.inbox, classification: 'failed' })
    const fetchMock = mockBoard([], { 'GET /api/todos': lists([waiting, headphones], [failed, headphones]) })
    render(<App />)
    await advance()

    await advance(1000)

    expect(listRequests(fetchMock)).toBe(2)
    expect(cardTitles('Gelen kutusu')).toEqual(['Süt al'])
    expect(cardTitles('Satın alım')).toEqual(['Kulaklık'])
    expect(card('Süt al')).toHaveAttribute('data-classification', 'failed')
    expect(within(card('Süt al')).getByText('sınıflandırılamadı')).toHaveClass('badge-failed')
    expect(within(card('Süt al')).getByRole('button', { name: 'Süt al için tekrar dene' })).toBeInTheDocument()
    expect(within(card('Süt al')).queryByText('sınıflandırılıyor…')).not.toBeInTheDocument()
    // Nothing is pending any more, so the polling is over.
    await advance(3000)
    expect(listRequests(fetchMock)).toBe(2)
  })

  it('does not touch cards that were not pending', async () => {
    // The server sees the other cards differently: another order, another status, another column.
    // This board has its own order, and nothing but the pending card waited for an answer.
    const a = makeTodo({ id: 2, title: 'A', category_id: cat.purchase, position: 0 })
    const b = makeTodo({ id: 3, title: 'B', category_id: cat.purchase, position: 1 })
    const c = makeTodo({ id: 4, title: 'C', category_id: cat.research })
    const toWork = makeTodo({ id: 1, title: 'Süt al', category_id: cat.work, position: 0, classification: 'done' })
    const fetchMock = mockBoard([], {
      'GET /api/todos': lists(
        [waiting, a, b, c],
        [
          { ...b, position: 0, status: 'done' },
          { ...a, position: 1, title: 'A changed' },
          { ...c, category_id: cat.other, position: 0 },
          toWork,
        ],
      ),
    })
    render(<App />)
    await advance()

    await advance(1000)

    expect(listRequests(fetchMock)).toBe(2)
    expect(cardTitles('İş')).toEqual(['Süt al'])
    // A and B: the local order, the local data.
    expect(cardTitles('Satın alım')).toEqual(['A', 'B'])
    expect(card('B')).toHaveAttribute('data-status', 'todo')
    expect(screen.queryByText('A changed')).not.toBeInTheDocument()
    // C is still in Araştırma, not in Diğer.
    expect(cardTitles('Araştırma')).toEqual(['C'])
    expect(cardTitles('Diğer')).toEqual([])
  })

  it('keeps the order of the server for cards that are classified into the same column', async () => {
    const second = makeTodo({ id: 3, title: 'Bilet al', category_id: cat.inbox, classification: 'pending' })
    mockBoard([], {
      'GET /api/todos': lists(
        [second, waiting, headphones],
        // The AI took "Süt al" first and "Bilet al" next, each to the top of Satın alım.
        [
          { ...second, category_id: cat.purchase, position: 0, classification: 'done' },
          { ...placed, position: 1 },
          { ...headphones, position: 2 },
        ],
      ),
    })
    render(<App />)
    await advance()
    expect(cardTitles('Gelen kutusu')).toEqual(['Bilet al', 'Süt al'])

    await advance(1000)

    expect(cardTitles('Satın alım')).toEqual(['Bilet al', 'Süt al', 'Kulaklık'])
    expect(cardTitles('Gelen kutusu')).toEqual([])
  })

  it('never overlaps requests', async () => {
    const reply = deferred<MockReply>()
    let asked = 0
    const fetchMock = mockBoard([], {
      'GET /api/todos': () => {
        asked += 1
        return asked === 1 ? { body: [waiting, headphones] } : reply.promise
      },
    })
    render(<App />)
    await advance()
    expect(listRequests(fetchMock)).toBe(1)

    // The second request is held back by the server: three more seconds, no further request.
    await advance(1000)
    expect(listRequests(fetchMock)).toBe(2)
    await advance(3000)
    expect(listRequests(fetchMock)).toBe(2)

    // The moment it is answered (the card is still pending) polling goes on, one request at a time.
    reply.resolve({ body: [waiting, headphones] })
    await advance(0)
    expect(listRequests(fetchMock)).toBe(2)
    await advance(1000)
    expect(listRequests(fetchMock)).toBe(3)
  })

  it('goes on polling after a request failed', async () => {
    let asked = 0
    const fetchMock = mockBoard([], {
      'GET /api/todos': () => {
        asked += 1
        if (asked === 1) return { body: [waiting, headphones] }
        if (asked === 2) return { status: 500, body: { error: 'boom' } }
        return { body: [placed, headphones] }
      },
    })
    render(<App />)
    await advance()

    await advance(1000)
    // A failed poll is no news to the user: the card just goes on waiting.
    expect(listRequests(fetchMock)).toBe(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(cardTitles('Gelen kutusu')).toEqual(['Süt al'])

    await advance(1000)

    expect(listRequests(fetchMock)).toBe(3)
    expect(cardTitles('Satın alım')).toEqual(['Süt al', 'Kulaklık'])
  })

  it('starts polling when a card is added and stops once it is classified', async () => {
    const created = makeTodo({ id: 1, title: 'Süt al', category_id: cat.inbox, classification: 'pending' })
    const fetchMock = mockBoard([headphones], {
      'POST /api/todos': () => ({ status: 201, body: created }),
      'GET /api/todos': lists([headphones], [placed, headphones]),
    })
    render(<App />)
    await advance()

    await advance(3000)
    expect(listRequests(fetchMock)).toBe(1)

    fireEvent.change(screen.getByLabelText('Todo başlığı'), { target: { value: 'Süt al' } })
    fireEvent.click(screen.getByRole('button', { name: 'Ekle' }))
    await advance()
    expect(cardTitles('Gelen kutusu')).toEqual(['Süt al'])
    expect(listRequests(fetchMock)).toBe(1)

    await advance(1000)

    expect(listRequests(fetchMock)).toBe(2)
    expect(cardTitles('Satın alım')).toEqual(['Süt al', 'Kulaklık'])
    await advance(3000)
    expect(listRequests(fetchMock)).toBe(2)
  })

  it('polls again after a failed card is sent to the AI once more', async () => {
    const failed = makeTodo({ id: 1, title: 'Süt al', category_id: cat.inbox, classification: 'failed' })
    const fetchMock = mockBoard([], {
      'POST /api/todos/1/classify': () => ({ status: 202, body: waiting }),
      'GET /api/todos': lists([failed, headphones], [placed, headphones]),
    })
    render(<App />)
    await advance()
    await advance(3000)
    expect(listRequests(fetchMock)).toBe(1)

    fireEvent.click(screen.getByRole('button', { name: 'Süt al için tekrar dene' }))
    await advance()
    expect(within(card('Süt al')).getByText('sınıflandırılıyor…')).toBeInTheDocument()

    await advance(1000)

    expect(listRequests(fetchMock)).toBe(2)
    expect(cardTitles('Satın alım')).toEqual(['Süt al', 'Kulaklık'])
  })
})

// Review focus: the AI answers while the user moves the very card by hand. The answer must not
// carry the card back.
describe('polling and moves by hand', () => {
  it('does not apply a polling result while a move is being saved', async () => {
    const move = deferred<MockReply>()
    const patch = vi.fn<Handler>(() => move.promise)
    const fetchMock = mockBoard([], {
      'GET /api/todos': lists([waiting, headphones], [placed, { ...headphones, position: 1 }]),
      'PATCH /api/todos/1': patch,
    })
    render(<App />)
    await advance()

    // The user puts the card in Diğer; the request is still open.
    choose(screen.getByLabelText('Süt al için kategori'), 'Diğer')
    expect(cardTitles('Diğer')).toEqual(['Süt al'])

    // Meanwhile the server tells that the AI has put it into Satın alım.
    await advance(1000)
    expect(listRequests(fetchMock)).toBe(2)

    // That answer is not used: the card stays where the user put it.
    expect(cardTitles('Diğer')).toEqual(['Süt al'])
    expect(cardTitles('Satın alım')).toEqual(['Kulaklık'])

    move.resolve({
      body: makeTodo({ id: 1, title: 'Süt al', category_id: cat.other, position: 0, classification: 'manual' }),
    })
    await advance()

    expect(patch.mock.calls[0][0]).toEqual({ category_id: cat.other, position: 0 })
    expect(cardTitles('Diğer')).toEqual(['Süt al'])
    expect(cardTitles('Satın alım')).toEqual(['Kulaklık'])
    expect(card('Süt al')).toHaveAttribute('data-classification', 'manual')
    // The card is the user's now, so there is nothing to wait for.
    await advance(3000)
    expect(listRequests(fetchMock)).toBe(2)
  })

  it('does not carry a moved card back with an answer that was already on its way', async () => {
    const poll = deferred<MockReply>()
    let asked = 0
    const fetchMock = mockBoard([], {
      'GET /api/todos': () => {
        asked += 1
        return asked === 1 ? { body: [waiting, headphones] } : poll.promise
      },
      'PATCH /api/todos/1': () => ({
        body: makeTodo({ id: 1, title: 'Süt al', category_id: cat.other, position: 0, classification: 'manual' }),
      }),
    })
    render(<App />)
    await advance()

    // The second request goes out, and is not answered yet.
    await advance(1000)
    expect(listRequests(fetchMock)).toBe(2)

    // The user moves the card, and the move is saved, all before the answer comes.
    choose(screen.getByLabelText('Süt al için kategori'), 'Diğer')
    await advance()
    expect(cardTitles('Diğer')).toEqual(['Süt al'])
    expect(screen.getByLabelText('Süt al için kategori')).toBeEnabled()

    // The answer was written before the move: it says the AI put the card into Satın alım. Saved, the
    // card is the user's (manual), and a card that is not pending here is not looked at.
    poll.resolve({ body: [placed, headphones] })
    await advance()

    expect(cardTitles('Diğer')).toEqual(['Süt al'])
    expect(cardTitles('Satın alım')).toEqual(['Kulaklık'])
    expect(card('Süt al')).toHaveAttribute('data-classification', 'manual')
  })

  it('keeps the other cards waiting for their answer after a move made one answer unusable', async () => {
    // "Bilet al" is classified in the very answer that is dropped; the next poll brings it.
    const ticket = makeTodo({ id: 3, title: 'Bilet al', category_id: cat.inbox, classification: 'pending' })
    const ticketPlaced = makeTodo({ id: 3, title: 'Bilet al', category_id: cat.appointment, classification: 'done' })
    const move = deferred<MockReply>()
    mockBoard([], {
      'GET /api/todos': lists([waiting, ticket], [placed, ticketPlaced], [placed, ticketPlaced]),
      'PATCH /api/todos/1': () => move.promise,
    })
    render(<App />)
    await advance()

    choose(screen.getByLabelText('Süt al için kategori'), 'Diğer')
    await advance(1000)
    // Dropped while the move was open: "Bilet al" is still in Gelen kutusu.
    expect(cardTitles('Gelen kutusu')).toEqual(['Bilet al'])

    move.resolve({
      body: makeTodo({ id: 1, title: 'Süt al', category_id: cat.other, position: 0, classification: 'manual' }),
    })
    await advance()
    await advance(1000)

    expect(cardTitles('Randevu & Etkinlik')).toEqual(['Bilet al'])
    expect(cardTitles('Diğer')).toEqual(['Süt al'])
    expect(cardTitles('Satın alım')).toEqual([])
  })

  it('keeps an open description editor, with its text, when the AI moves the card', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: { ...placed, description: 'iki litre', position: 0 },
    }))
    mockBoard([], {
      'GET /api/todos': lists([waiting, headphones], [placed, { ...headphones, position: 1 }]),
      'PATCH /api/todos/1': patch,
    })
    render(<App />)
    await advance()

    fireEvent.click(within(card('Süt al')).getByRole('button', { name: 'Açıklamayı düzenle' }))
    fireEvent.change(within(card('Süt al')).getByLabelText('Süt al açıklaması'), { target: { value: 'iki litre' } })

    // The card changes its column, which builds it anew; its editor and the text stay.
    await advance(1000)
    expect(cardTitles('Satın alım')).toEqual(['Süt al', 'Kulaklık'])
    expect(cardTitles('Gelen kutusu')).toEqual([])
    const editor = within(card('Süt al')).getByLabelText('Süt al açıklaması')
    expect(editor).toHaveValue('iki litre')

    fireEvent.click(within(card('Süt al')).getByRole('button', { name: 'Kaydet' }))
    await advance()

    expect(patch.mock.calls[0][0]).toEqual({ description: 'iki litre' })
    expect(within(card('Süt al')).queryByLabelText('Süt al açıklaması')).not.toBeInTheDocument()
    expect(within(card('Süt al')).getByText('iki litre', { selector: '.card-description' })).toBeInTheDocument()
  })
})
