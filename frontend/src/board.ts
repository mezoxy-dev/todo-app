import type { Todo } from './api'

// Moves a todo to `toIndex` in column `toCategoryId`. The index counts the target column
// once the todo has left its old place (the same meaning as the API's `position`); past the
// end means last. Returns a new array in which the order inside every column is the array
// order, the moved todo has its new category_id, and every position matches its place in its
// column. Todos in other columns keep their relative order. An unknown id returns `todos`.
export function moveTodo(todos: Todo[], todoId: number, toCategoryId: number, toIndex: number): Todo[] {
  const moving = todos.find((todo) => todo.id === todoId)
  if (moving === undefined) return todos

  const rest = todos.filter((todo) => todo.id !== todoId)
  const target = rest.filter((todo) => todo.category_id === toCategoryId)
  // The todo that ends up right below the moved one; none when it goes to the bottom.
  const below = target[Math.max(toIndex, 0)]
  const at = below === undefined ? rest.length : rest.indexOf(below)
  const moved = [...rest.slice(0, at), { ...moving, category_id: toCategoryId }, ...rest.slice(at)]

  const placed = new Map<number, number>()
  return moved.map((todo) => {
    const position = placed.get(todo.category_id) ?? 0
    placed.set(todo.category_id, position + 1)
    return { ...todo, position }
  })
}
