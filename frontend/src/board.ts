import type { Todo } from './api'

// Sets every position to the place of the todo among the ones of its own column, in array order.
function renumber(todos: Todo[]): Todo[] {
  const placed = new Map<number, number>()
  return todos.map((todo) => {
    const position = placed.get(todo.category_id) ?? 0
    placed.set(todo.category_id, position + 1)
    return { ...todo, position }
  })
}

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
  return renumber([...rest.slice(0, at), { ...moving, category_id: toCategoryId }, ...rest.slice(at)])
}

// Puts `todo` (the copy the server sent) on top of its own column, taking out the one with the
// same id first; a todo the list does not have yet is simply added. Returns a new array with
// every position matching its place in its column; todos in other columns keep their order.
export function placeOnTop(todos: Todo[], todo: Todo): Todo[] {
  const rest = todos.filter((other) => other.id !== todo.id)
  const top = rest.find((other) => other.category_id === todo.category_id)
  const at = top === undefined ? rest.length : rest.indexOf(top)
  return renumber([...rest.slice(0, at), todo, ...rest.slice(at)])
}

// What the board does with the server's list while cards wait for the AI. Only a todo that is
// pending here is looked at, and only once the server no longer has it pending: it is replaced by
// the server's copy and put on top of its own column, or, when the classification failed, replaced
// where it is (it stays in Gelen kutusu). Every other todo keeps the state and the place this board
// has given it. Returns `todos` itself when there is nothing to change.
export function applyClassifications(todos: Todo[], fresh: Todo[]): Todo[] {
  const waiting = new Set(todos.filter((todo) => todo.classification === 'pending').map((todo) => todo.id))
  // The server lists a column from its top, and every answer goes on top of its column: taking
  // the answers last to first leaves the ones that land in the same column in the server's order.
  const answered = fresh.filter((todo) => waiting.has(todo.id) && todo.classification !== 'pending').reverse()
  if (answered.length === 0) return todos

  let settled = todos
  for (const todo of answered) {
    settled =
      todo.classification === 'failed'
        ? settled.map((other) => (other.id === todo.id ? todo : other))
        : placeOnTop(settled, todo)
  }
  return renumber(settled)
}
