import { describe, expect, it } from 'vitest'
import type { Todo } from './api'
import { moveTodo } from './board'
import { makeTodo } from './test/mockApi'

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
