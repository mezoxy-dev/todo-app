import { describe, expect, it } from 'vitest'
import type { Todo } from './api'
import { applyClassifications, moveTodo, placeOnTop } from './board'
import { cat, makeTodo } from './test/mockApi'

// Column 1 holds A, B, C and column 2 holds D, E (top to bottom), in this array order.
const ids = { A: 1, B: 2, C: 3, D: 4, E: 5 }

function fixture(): Todo[] {
  return [
    makeTodo({ id: ids.A, title: 'A', category_id: 1, position: 0 }),
    makeTodo({ id: ids.B, title: 'B', category_id: 1, position: 1 }),
    makeTodo({ id: ids.C, title: 'C', category_id: 1, position: 2 }),
    makeTodo({ id: ids.D, title: 'D', category_id: 2, position: 0 }),
    makeTodo({ id: ids.E, title: 'E', category_id: 2, position: 1 }),
  ]
}

// Titles of one column, top to bottom: inside a column the array order is the order.
function col(todos: Todo[], categoryId: number): string[] {
  return todos.filter((todo) => todo.category_id === categoryId).map((todo) => todo.title)
}

// The same vectors as the store's UpdateTodo tests, so that the UI and the server agree on
// what "position" means: the index in the target column once the todo has been taken out.
describe('moveTodo', () => {
  it('moves down within a column', () => {
    // The top card dropped on index 2 of a 3-card column ends up last (no off-by-one).
    const moved = moveTodo(fixture(), ids.A, 1, 2)

    expect(col(moved, 1)).toEqual(['B', 'C', 'A'])
    expect(col(moved, 2)).toEqual(['D', 'E'])
  })

  it('moves up within a column', () => {
    const moved = moveTodo(fixture(), ids.C, 1, 0)

    expect(col(moved, 1)).toEqual(['C', 'A', 'B'])
    expect(col(moved, 2)).toEqual(['D', 'E'])
  })

  it('moves across columns to an index', () => {
    const moved = moveTodo(fixture(), ids.B, 2, 1)

    expect(col(moved, 1)).toEqual(['A', 'C'])
    expect(col(moved, 2)).toEqual(['D', 'B', 'E'])
    expect(moved.find((todo) => todo.id === ids.B)?.category_id).toBe(2)
  })

  it('clamps an index past the end', () => {
    const moved = moveTodo(fixture(), ids.A, 2, 99)

    expect(col(moved, 1)).toEqual(['B', 'C'])
    expect(col(moved, 2)).toEqual(['D', 'E', 'A'])
  })

  it('clamps an index past the end within the same column too', () => {
    expect(col(moveTodo(fixture(), ids.A, 1, 99), 1)).toEqual(['B', 'C', 'A'])
  })

  it('clamps a negative index to the top', () => {
    expect(col(moveTodo(fixture(), ids.C, 2, -1), 2)).toEqual(['C', 'D', 'E'])
  })

  it('puts a todo at the top of another column for index 0', () => {
    // What the card select does.
    const moved = moveTodo(fixture(), ids.C, 2, 0)

    expect(col(moved, 1)).toEqual(['A', 'B'])
    expect(col(moved, 2)).toEqual(['C', 'D', 'E'])
  })

  it('moves into an empty column', () => {
    const moved = moveTodo(fixture(), ids.B, 3, 0)

    expect(col(moved, 1)).toEqual(['A', 'C'])
    expect(col(moved, 2)).toEqual(['D', 'E'])
    expect(col(moved, 3)).toEqual(['B'])
  })

  it('keeps the order when a todo is moved to the index it already has', () => {
    // B is taken out of [A, B, C] and put back at index 1.
    expect(col(moveTodo(fixture(), ids.B, 1, 1), 1)).toEqual(['A', 'B', 'C'])
  })

  it('renumbers positions to match the new order', () => {
    // Stale positions on purpose: only the array order says where a todo is.
    const stale = fixture().map((todo) => ({ ...todo, position: 9 }))

    const moved = moveTodo(stale, ids.B, 2, 1)

    const next = new Map<number, number>()
    for (const todo of moved) {
      const index = next.get(todo.category_id) ?? 0
      expect(todo.position, `${todo.title} in column ${todo.category_id}`).toBe(index)
      next.set(todo.category_id, index + 1)
    }
    // Both columns were visited: 2 todos left in column 1, 3 in column 2.
    expect([...next]).toEqual([
      [1, 2],
      [2, 3],
    ])
  })

  it('does not mutate its input', () => {
    const todos = fixture()
    const snapshot = structuredClone(todos)
    // Frozen, so that an in-place change throws instead of slipping through.
    todos.forEach((todo) => Object.freeze(todo))
    Object.freeze(todos)

    const moved = moveTodo(todos, ids.A, 2, 1)

    expect(moved).not.toBe(todos)
    expect(todos).toEqual(snapshot)
  })

  it('returns the todos as they are for an unknown id', () => {
    const todos = fixture()

    expect(moveTodo(todos, 99, 2, 0)).toBe(todos)
  })
})

// P and Q wait in Gelen kutusu; Satın alım holds A and B; Araştırma holds C (top to bottom).
function classifying(): Todo[] {
  return [
    makeTodo({ id: 1, title: 'P', category_id: cat.inbox, position: 0, classification: 'pending' }),
    makeTodo({ id: 2, title: 'Q', category_id: cat.inbox, position: 1, classification: 'pending' }),
    makeTodo({ id: 3, title: 'A', category_id: cat.purchase, position: 0 }),
    makeTodo({ id: 4, title: 'B', category_id: cat.purchase, position: 1 }),
    makeTodo({ id: 5, title: 'C', category_id: cat.research, position: 0 }),
  ]
}

// What the server says once the AI has put P into Satın alım: on top of it.
const classifiedP = makeTodo({ id: 1, title: 'P', category_id: cat.purchase, position: 0, classification: 'done' })

// The index of every todo among the ones of its own column, as the board shows them.
function positionsAreIndexes(todos: Todo[]): void {
  const next = new Map<number, number>()
  for (const todo of todos) {
    const index = next.get(todo.category_id) ?? 0
    expect(todo.position, `${todo.title} in column ${todo.category_id}`).toBe(index)
    next.set(todo.category_id, index + 1)
  }
}

describe('placeOnTop', () => {
  it('takes a todo out of Gelen kutusu and puts it on top of Satın alım', () => {
    const placed = placeOnTop(classifying(), classifiedP)

    expect(col(placed, cat.inbox)).toEqual(['Q'])
    expect(col(placed, cat.purchase)).toEqual(['P', 'A', 'B'])
  })

  it('puts the todo it is given in place of the old one', () => {
    const placed = placeOnTop(classifying(), classifiedP)

    // The server's copy, not the pending one it replaces.
    expect(placed.filter((todo) => todo.id === 1)).toEqual([{ ...classifiedP, position: 0 }])
  })

  it('leaves the other columns and the other todos in their order', () => {
    const before = classifying()

    const placed = placeOnTop(before, classifiedP)

    // Araştırma is neither where P came from nor where it went.
    expect(placed.find((todo) => todo.id === 5)).toEqual(before[4])
    expect(placed.filter((todo) => todo.id !== 1).map((todo) => todo.id)).toEqual([2, 3, 4, 5])
  })

  it('numbers the positions of every column by place', () => {
    // Stale positions on purpose: only the array order says where a todo is.
    const stale = classifying().map((todo) => ({ ...todo, position: 9 }))

    positionsAreIndexes(placeOnTop(stale, classifiedP))
  })

  it('puts a todo that stays in its column on top of it', () => {
    // A retried todo: the server has put it back on top of Gelen kutusu.
    const retried = makeTodo({ id: 2, title: 'Q', category_id: cat.inbox, position: 0, classification: 'pending' })

    const placed = placeOnTop(classifying(), retried)

    expect(col(placed, cat.inbox)).toEqual(['Q', 'P'])
    positionsAreIndexes(placed)
  })

  it('puts a todo into an empty column', () => {
    const toWork = { ...classifiedP, category_id: cat.work }

    const placed = placeOnTop(classifying(), toWork)

    expect(col(placed, cat.work)).toEqual(['P'])
    expect(col(placed, cat.inbox)).toEqual(['Q'])
    positionsAreIndexes(placed)
  })

  it('adds a todo that the list does not have yet on top of its column', () => {
    const created = makeTodo({ id: 6, title: 'New', category_id: cat.inbox, position: 0, classification: 'pending' })

    const placed = placeOnTop(classifying(), created)

    expect(col(placed, cat.inbox)).toEqual(['New', 'P', 'Q'])
    expect(placed).toHaveLength(6)
    positionsAreIndexes(placed)
  })

  it('does not mutate its input', () => {
    const todos = classifying()
    const snapshot = structuredClone(todos)
    // Frozen, so that an in-place change throws instead of slipping through.
    todos.forEach((todo) => Object.freeze(todo))
    Object.freeze(todos)

    const placed = placeOnTop(todos, classifiedP)

    expect(placed).not.toBe(todos)
    expect(todos).toEqual(snapshot)
  })
})

// The AI answers while the board is open: the polling hands the server's list over, and only the
// todos that are pending here are looked at.
describe('applyClassifications', () => {
  it('puts a todo that has been classified on top of its new column', () => {
    const fresh = [classifiedP, makeTodo({ id: 3, title: 'A', category_id: cat.purchase, position: 1 })]

    const settled = applyClassifications(classifying(), fresh)

    expect(col(settled, cat.purchase)).toEqual(['P', 'A', 'B'])
    expect(col(settled, cat.inbox)).toEqual(['Q'])
    expect(settled.find((todo) => todo.id === 1)?.classification).toBe('done')
    positionsAreIndexes(settled)
  })

  it('keeps a todo that failed where it is, with the server copy', () => {
    const failed = makeTodo({ id: 2, title: 'Q', category_id: cat.inbox, position: 1, classification: 'failed' })

    const settled = applyClassifications(classifying(), [failed])

    expect(col(settled, cat.inbox)).toEqual(['P', 'Q'])
    expect(settled.find((todo) => todo.id === 2)?.classification).toBe('failed')
    // P is still pending on the server too, but the server did not list it: nothing happens to it.
    expect(settled.find((todo) => todo.id === 1)?.classification).toBe('pending')
    positionsAreIndexes(settled)
  })

  it('leaves a todo alone while the server still has it pending', () => {
    const todos = classifying()
    const stillPending = makeTodo({ id: 1, title: 'P', category_id: cat.inbox, position: 0, classification: 'pending' })

    expect(applyClassifications(todos, [stillPending])).toBe(todos)
  })

  it('leaves a pending todo the server does not list alone', () => {
    const todos = classifying()

    expect(applyClassifications(todos, [])).toBe(todos)
  })

  it('does not touch the todos that were not pending', () => {
    // The server sees A and B in another order, and B changed; this board has its own order and
    // the state it saved itself. Only P was waiting for an answer.
    const fresh = [
      classifiedP,
      makeTodo({ id: 4, title: 'B changed', category_id: cat.purchase, position: 1, status: 'done' }),
      makeTodo({ id: 3, title: 'A', category_id: cat.purchase, position: 2 }),
      makeTodo({ id: 5, title: 'C moved', category_id: cat.work, position: 0 }),
    ]
    const before = classifying()

    const settled = applyClassifications(before, fresh)

    expect(col(settled, cat.purchase)).toEqual(['P', 'A', 'B'])
    expect(col(settled, cat.research)).toEqual(['C'])
    expect(settled.find((todo) => todo.id === 4)).toEqual({ ...before[3], position: 2 })
    expect(settled.find((todo) => todo.id === 5)).toEqual(before[4])
  })

  it('keeps the order of the server for todos that land in the same column', () => {
    // The AI put P first and Q second, each on top: the server lists Q above P.
    const fresh = [
      makeTodo({ id: 2, title: 'Q', category_id: cat.purchase, position: 0, classification: 'done' }),
      { ...classifiedP, position: 1 },
    ]

    const settled = applyClassifications(classifying(), fresh)

    expect(col(settled, cat.purchase)).toEqual(['Q', 'P', 'A', 'B'])
    expect(col(settled, cat.inbox)).toEqual([])
    positionsAreIndexes(settled)
  })

  it('does not mutate its input', () => {
    const todos = classifying()
    const snapshot = structuredClone(todos)
    todos.forEach((todo) => Object.freeze(todo))
    Object.freeze(todos)

    const settled = applyClassifications(todos, [classifiedP])

    expect(settled).not.toBe(todos)
    expect(todos).toEqual(snapshot)
  })
})
