import { useEffect, useRef, useState } from 'react'
import { getCategories, getTodos, updateTodo } from './api'
import type { Category, Todo } from './api'
import { moveTodo } from './board'
import { AddTodoForm } from './components/AddTodoForm'
import { Board } from './components/Board'
import { ThemeSwitcher } from './components/ThemeSwitcher'

type Status = 'loading' | 'error' | 'ready'

// The order the todos had before a move that could not be saved. A todo added while the move was
// being saved is not in `before`; it stays, on top, where the server has put it.
function undoMove(current: Todo[], before: Todo[]): Todo[] {
  const known = new Set(before.map((todo) => todo.id))
  return [...current.filter((todo) => !known.has(todo.id)), ...before]
}

// The class names that mean nothing to Tailwind (app, board, card, ...) are what the theme
// stylesheets in themes/ are written against; the Tailwind utilities only lay things out.
export default function App() {
  const [status, setStatus] = useState<Status>('loading')
  const [attempt, setAttempt] = useState(0)
  const [categories, setCategories] = useState<Category[]>([])
  const [todos, setTodos] = useState<Todo[]>([])
  const [moving, setMoving] = useState(false)
  const [moveError, setMoveError] = useState<string | null>(null)
  // While a move is being saved, a second one is turned away. The card selects are off then, but
  // the cards stay draggable (a drag handle that goes away would take the keyboard focus with it),
  // so a drop can still arrive: it is ignored, and the card stays where the list says. Unlike
  // `moving`, the ref changes synchronously, so this holds before React has rendered the
  // disabled selects as well.
  const moveInFlight = useRef(false)

  // Loads on mount and again whenever "Tekrar dene" bumps `attempt`.
  useEffect(() => {
    let stale = false
    Promise.all([getCategories(), getTodos()]).then(
      ([loadedCategories, loadedTodos]) => {
        if (stale) return
        setCategories(loadedCategories)
        setTodos(loadedTodos)
        setStatus('ready')
      },
      () => {
        if (!stale) setStatus('error')
      },
    )
    return () => {
      stale = true
    }
  }, [attempt])

  function retry() {
    setStatus('loading')
    setAttempt((current) => current + 1)
  }

  // A new todo is on top of its column (position 0), and a column is shown in the order of the
  // list, so it goes to the front.
  function handleCreated(created: Todo) {
    setTodos((current) => [created, ...current])
  }

  // The board shows a move at once and the server is told afterwards; if that fails, the move
  // is taken back and the error is shown above the board.
  async function handleMove(todoId: number, toCategoryId: number, toIndex: number) {
    if (moveInFlight.current) return
    moveInFlight.current = true
    const before = todos
    setTodos(moveTodo(before, todoId, toCategoryId, toIndex))
    setMoving(true)
    setMoveError(null)
    try {
      const saved = await updateTodo(todoId, { category_id: toCategoryId, position: toIndex })
      // The server's todo replaces the local one; the order stays as the board shows it.
      setTodos((current) => current.map((todo) => (todo.id === saved.id ? saved : todo)))
    } catch (err) {
      setTodos((current) => undoMove(current, before))
      setMoveError(err instanceof Error ? err.message : String(err))
    } finally {
      moveInFlight.current = false
      setMoving(false)
    }
  }

  return (
    <main className="app mx-auto max-w-5xl space-y-6 p-4">
      <header className="app-header flex flex-wrap items-center justify-between gap-3">
        <h1 className="app-title">Todo'lar</h1>
        <ThemeSwitcher />
      </header>
      {status === 'loading' && <p className="status">Yükleniyor...</p>}
      {status === 'error' && (
        <div className="space-y-2">
          <p role="alert" className="alert">
            Sunucuya ulaşılamadı.
          </p>
          <button type="button" onClick={retry} className="button px-3 py-1">
            Tekrar dene
          </button>
        </div>
      )}
      {status === 'ready' && (
        <>
          <AddTodoForm categories={categories} onCreated={handleCreated} />
          {moveError !== null && (
            <p role="alert" className="alert">
              {`Todo taşınamadı: ${moveError}`}
            </p>
          )}
          <Board categories={categories} todos={todos} moving={moving} onMove={handleMove} />
        </>
      )}
    </main>
  )
}
