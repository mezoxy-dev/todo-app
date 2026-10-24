import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { drop, resetStandIn, standIn } from '../test/dndStandIn'
import { makeTodo, seedCategories } from '../test/mockApi'
import { Board } from './Board'

// jsdom cannot drag, so the drag library is replaced by the stand-in of test/dndStandIn.ts
// (App.test.tsx renders the real library, a browser does the dragging).
vi.mock('@hello-pangea/dnd', () => import('../test/dndStandIn'))

const onMove = vi.fn()

// Column 1: A (id 1), B (id 2). Column 2: empty. Column 3: C (id 3).
function renderBoard(moving = false) {
  return render(
    <Board
      categories={seedCategories}
      todos={[
        makeTodo({ id: 1, title: 'A', category_id: 1 }),
        makeTodo({ id: 2, title: 'B', category_id: 1 }),
        makeTodo({ id: 3, title: 'C', category_id: 3 }),
      ]}
      moving={moving}
      onMove={onMove}
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

    drop('1', { droppableId: '1', index: 0 }, { droppableId: '3', index: 1 })

    // Numbers, not the string ids the library works with.
    expect(onMove).toHaveBeenCalledTimes(1)
    expect(onMove).toHaveBeenCalledWith(1, 3, 1)
  })

  it('asks for a move when a card is dropped on another index of its own column', () => {
    renderBoard()

    drop('1', { droppableId: '1', index: 0 }, { droppableId: '1', index: 1 })

    expect(onMove).toHaveBeenCalledTimes(1)
    expect(onMove).toHaveBeenCalledWith(1, 1, 1)
  })

  it('asks for a move into an empty column', () => {
    renderBoard()

    drop('2', { droppableId: '1', index: 1 }, { droppableId: '2', index: 0 })

    expect(onMove).toHaveBeenCalledWith(2, 2, 0)
  })

  it('does nothing when a card is dropped outside every column', () => {
    renderBoard()

    drop('1', { droppableId: '1', index: 0 }, null)

    expect(onMove).not.toHaveBeenCalled()
  })

  it('does nothing when a card is dropped back where it was', () => {
    renderBoard()

    drop('2', { droppableId: '1', index: 1 }, { droppableId: '1', index: 1 })

    expect(onMove).not.toHaveBeenCalled()
  })

  it('marks the card being dragged and the column it is over', () => {
    standIn.dragging.add('1')
    standIn.hovered.add('3')

    renderBoard()

    const card = (title: string) => screen.getByRole('heading', { name: title }).closest('.card')
    expect(card('A')).toHaveClass('is-dragging')
    expect(card('B')).not.toHaveClass('is-dragging')
    expect(card('C')).not.toHaveClass('is-dragging')

    const list = (column: string) => screen.getByRole('region', { name: column }).querySelector('.card-list')
    expect(list('Tamamlandı')).toHaveClass('is-drag-over')
    expect(list('Henüz yapılmadı')).not.toHaveClass('is-drag-over')
    expect(list('Yapılıyor')).not.toHaveClass('is-drag-over')
  })

  it('can be dragged and used while idle', () => {
    renderBoard(false)

    expect(Object.fromEntries(standIn.dragDisabled)).toEqual({ 1: false, 2: false, 3: false })
    for (const select of screen.getAllByLabelText(/ için sütun$/)) expect(select).toBeEnabled()
  })

  it('turns every card select off while a move is saving, but leaves dragging on', () => {
    renderBoard(true)

    const selects = screen.getAllByLabelText(/ için sütun$/)
    expect(selects).toHaveLength(3)
    for (const select of selects) expect(select).toBeDisabled()
    // A drag handle that goes away takes the keyboard focus with it, so the cards stay
    // draggable; App turns a second move away.
    expect(Object.fromEntries(standIn.dragDisabled)).toEqual({ 1: false, 2: false, 3: false })
  })
})
