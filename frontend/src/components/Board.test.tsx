import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { drop, resetStandIn, standIn } from '../test/dndStandIn'
import { cat, makeTodo, seedCategories } from '../test/mockApi'
import { Board } from './Board'

// jsdom cannot drag, so the drag library is replaced by the stand-in of test/dndStandIn.ts
// (App.test.tsx renders the real library, a browser does the dragging).
vi.mock('@hello-pangea/dnd', () => import('../test/dndStandIn'))

const onMove = vi.fn()

// Gelen kutusu: A (id 1), B (id 2). Satın alım: empty. Araştırma: C (id 3).
function renderBoard(moving = false) {
  return render(
    <Board
      categories={seedCategories}
      todos={[
        makeTodo({ id: 1, title: 'A', category_id: cat.inbox }),
        makeTodo({ id: 2, title: 'B', category_id: cat.inbox }),
        makeTodo({ id: 3, title: 'C', category_id: cat.research }),
      ]}
      moving={moving}
      drafts={new Map()}
      onMove={onMove}
      onStatusChange={vi.fn()}
      onDraftChange={vi.fn()}
      onSaveDescription={vi.fn()}
      onRetry={vi.fn()}
    />,
  )
}

beforeEach(() => {
  resetStandIn()
  onMove.mockReset()
})

describe('Board drag and drop', () => {
  it('asks for a move to the dropped column and index', () => {
    renderBoard()

    drop('1', { droppableId: String(cat.inbox), index: 0 }, { droppableId: String(cat.research), index: 1 })

    // Numbers, not the string ids the library works with.
    expect(onMove).toHaveBeenCalledTimes(1)
    expect(onMove).toHaveBeenCalledWith(1, cat.research, 1)
  })

  it('asks for a move when a card is dropped on another index of its own column', () => {
    renderBoard()

    drop('1', { droppableId: String(cat.inbox), index: 0 }, { droppableId: String(cat.inbox), index: 1 })

    expect(onMove).toHaveBeenCalledTimes(1)
    expect(onMove).toHaveBeenCalledWith(1, cat.inbox, 1)
  })

  it('asks for a move into an empty column', () => {
    renderBoard()

    drop('2', { droppableId: String(cat.inbox), index: 1 }, { droppableId: String(cat.purchase), index: 0 })

    expect(onMove).toHaveBeenCalledWith(2, cat.purchase, 0)
  })

  it('does nothing when a card is dropped outside every column', () => {
    renderBoard()

    drop('1', { droppableId: String(cat.inbox), index: 0 }, null)

    expect(onMove).not.toHaveBeenCalled()
  })

  it('does nothing when a card is dropped back where it was', () => {
    renderBoard()

    drop('2', { droppableId: String(cat.inbox), index: 1 }, { droppableId: String(cat.inbox), index: 1 })

    expect(onMove).not.toHaveBeenCalled()
  })

  it('marks the card being dragged and the column it is over', () => {
    standIn.dragging.add('1')
    standIn.hovered.add(String(cat.research))

    renderBoard()

    const card = (title: string) => screen.getByRole('heading', { name: title }).closest('.card')
    expect(card('A')).toHaveClass('is-dragging')
    expect(card('B')).not.toHaveClass('is-dragging')
    expect(card('C')).not.toHaveClass('is-dragging')

    const list = (column: string) => screen.getByRole('region', { name: column }).querySelector('.card-list')
    expect(list('Araştırma')).toHaveClass('is-drag-over')
    expect(list('Gelen kutusu')).not.toHaveClass('is-drag-over')
    expect(list('Satın alım')).not.toHaveClass('is-drag-over')
  })

  it('can be dragged and used while idle', () => {
    renderBoard(false)

    expect(Object.fromEntries(standIn.dragDisabled)).toEqual({ 1: false, 2: false, 3: false })
    for (const select of screen.getAllByLabelText(/ için kategori$/)) expect(select).toBeEnabled()
    for (const select of screen.getAllByLabelText(/ için durum$/)) expect(select).toBeEnabled()
  })

  it('turns every card select off while a move is saving, but leaves dragging on', () => {
    renderBoard(true)

    const categories = screen.getAllByLabelText(/ için kategori$/)
    expect(categories).toHaveLength(3)
    for (const select of categories) expect(select).toBeDisabled()
    const statuses = screen.getAllByLabelText(/ için durum$/)
    expect(statuses).toHaveLength(3)
    for (const select of statuses) expect(select).toBeDisabled()
    // A drag handle that goes away takes the keyboard focus with it, so the cards stay
    // draggable; App turns a second move away.
    expect(Object.fromEntries(standIn.dragDisabled)).toEqual({ 1: false, 2: false, 3: false })
  })
})
