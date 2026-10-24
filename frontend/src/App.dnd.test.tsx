import { act, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { cardTitles, deferred, mockBoard, movesSettled } from './test/appHelpers'
import type { Handler } from './test/appHelpers'
import { drop, resetStandIn, standIn } from './test/dndStandIn'
import type { Place } from './test/dndStandIn'
import { makeTodo } from './test/mockApi'
import type { MockReply } from './test/mockApi'

// jsdom cannot drag, so the drag library is replaced by the stand-in of test/dndStandIn.ts and
// the drops are reported by hand. App.test.tsx renders the real library.
vi.mock('@hello-pangea/dnd', () => import('./test/dndStandIn'))

beforeEach(resetStandIn)

// A drop reported to the board; act() because it changes the state of App.
function dropCard(draggableId: string, source: Place, destination: Place | null) {
  act(() => drop(draggableId, source, destination))
}

describe('dropping cards', () => {
  it('moves a dropped card to the dropped place and saves that index', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: makeTodo({ id: 1, title: 'A', category_id: 3, position: 1 }),
    }))
    mockBoard(
      [
        makeTodo({ id: 1, title: 'A', category_id: 1 }),
        makeTodo({ id: 2, title: 'B', category_id: 1 }),
        makeTodo({ id: 3, title: 'C', category_id: 3 }),
        makeTodo({ id: 4, title: 'D', category_id: 3 }),
      ],
      { 'PATCH /api/todos/1': patch },
    )
    render(<App />)
    await screen.findByRole('region', { name: 'Tamamlandı' })

    // A, on top of column 1, dropped between C and D in column 3.
    dropCard('1', { droppableId: '1', index: 0 }, { droppableId: '3', index: 1 })

    // Shown at once, and saved with the index it was dropped on.
    expect(cardTitles('Tamamlandı')).toEqual(['C', 'A', 'D'])
    expect(cardTitles('Henüz yapılmadı')).toEqual(['B'])
    await movesSettled()
    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch.mock.calls[0][0]).toEqual({ category_id: 3, position: 1 })
    expect(cardTitles('Tamamlandı')).toEqual(['C', 'A', 'D'])
  })

  it('saves the index after removal for a drop further down its own column', async () => {
    const patch = vi.fn<Handler>(() => ({
      body: makeTodo({ id: 1, title: 'A', category_id: 1, position: 2 }),
    }))
    mockBoard(
      [
        makeTodo({ id: 1, title: 'A', category_id: 1 }),
        makeTodo({ id: 2, title: 'B', category_id: 1 }),
        makeTodo({ id: 3, title: 'C', category_id: 1 }),
      ],
      { 'PATCH /api/todos/1': patch },
    )
    render(<App />)
    await screen.findByRole('region', { name: 'Henüz yapılmadı' })

    // The top card dropped on the last place of a 3-card column ends up last, and the server is
    // told index 2: the index in the column once A has left it.
    dropCard('1', { droppableId: '1', index: 0 }, { droppableId: '1', index: 2 })

    expect(cardTitles('Henüz yapılmadı')).toEqual(['B', 'C', 'A'])
    await movesSettled()
    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch.mock.calls[0][0]).toEqual({ category_id: 1, position: 2 })
  })

  it('ignores a drop that ends while a move is being saved', async () => {
    const reply = deferred<MockReply>()
    const patch = vi.fn<Handler>(() => reply.promise)
    // Only the first todo has a PATCH route: a request for the second one fails the test.
    mockBoard(
      [
        makeTodo({ id: 1, title: 'A', category_id: 1 }),
        makeTodo({ id: 2, title: 'B', category_id: 1 }),
        makeTodo({ id: 3, title: 'C', category_id: 3 }),
      ],
      { 'PATCH /api/todos/1': patch },
    )
    render(<App />)
    await screen.findByRole('region', { name: 'Tamamlandı' })

    dropCard('1', { droppableId: '1', index: 0 }, { droppableId: '2', index: 0 })
    expect(cardTitles('Yapılıyor')).toEqual(['A'])

    // The cards stay draggable while A is saved (a drag handle that goes away would take the
    // keyboard focus with it), so this drop can still come. App turns it away.
    expect(Object.fromEntries(standIn.dragDisabled)).toEqual({ 1: false, 2: false, 3: false })
    dropCard('2', { droppableId: '1', index: 0 }, { droppableId: '3', index: 0 })

    expect(patch).toHaveBeenCalledTimes(1)
    expect(cardTitles('Henüz yapılmadı')).toEqual(['B'])
    expect(cardTitles('Yapılıyor')).toEqual(['A'])
    expect(cardTitles('Tamamlandı')).toEqual(['C'])
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    reply.resolve({ body: makeTodo({ id: 1, title: 'A', category_id: 2, position: 0 }) })
    await movesSettled()

    // The first move stands; the refused drop left no trace, and nothing else was requested.
    expect(cardTitles('Henüz yapılmadı')).toEqual(['B'])
    expect(cardTitles('Yapılıyor')).toEqual(['A'])
    expect(cardTitles('Tamamlandı')).toEqual(['C'])
    expect(patch).toHaveBeenCalledTimes(1)
  })

  it('ignores a drop that comes before the board has re-rendered after the first one', async () => {
    const reply = deferred<MockReply>()
    const patch = vi.fn<Handler>(() => reply.promise)
    mockBoard(
      [
        makeTodo({ id: 1, title: 'A', category_id: 1 }),
        makeTodo({ id: 2, title: 'B', category_id: 1 }),
        makeTodo({ id: 3, title: 'C', category_id: 3 }),
      ],
      { 'PATCH /api/todos/1': patch },
    )
    render(<App />)
    await screen.findByRole('region', { name: 'Tamamlandı' })

    // Both drops land inside one act() scope: React has not shown the first move yet (and
    // `moving` is not true yet) when the second arrives, so only the synchronous guard helps.
    act(() => {
      drop('1', { droppableId: '1', index: 0 }, { droppableId: '2', index: 0 })
      drop('2', { droppableId: '1', index: 1 }, { droppableId: '3', index: 0 })
    })

    expect(patch).toHaveBeenCalledTimes(1)
    expect(cardTitles('Henüz yapılmadı')).toEqual(['B'])
    expect(cardTitles('Yapılıyor')).toEqual(['A'])
    expect(cardTitles('Tamamlandı')).toEqual(['C'])

    reply.resolve({ body: makeTodo({ id: 1, title: 'A', category_id: 2, position: 0 }) })
    await movesSettled()
    expect(patch).toHaveBeenCalledTimes(1)
  })
})
