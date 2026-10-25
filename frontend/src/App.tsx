import { useEffect, useRef, useState } from 'react'
import { classifyTodo, errorText, getCategories, getTodos, updateTodo } from './api'
import type { Category, Status, Todo } from './api'
import { moveTodo, placeOnTop } from './board'
import { AddTodoForm } from './components/AddTodoForm'
import { Board } from './components/Board'
import { LogOverlay } from './components/LogOverlay'
import { ThemeSwitcher } from './components/ThemeSwitcher'
import { usePendingPolling } from './usePendingPolling'

type Load = 'loading' | 'error' | 'ready'

// The order the todos had before a move that could not be saved. A todo added while the move was
// being saved is not in `before`; it stays, on top, where the server has put it.
function undoMove(current: Todo[], before: Todo[]): Todo[] {
  const known = new Set(before.map((todo) => todo.id))
  return [...current.filter((todo) => !known.has(todo.id)), ...before]
}

// The class names that mean nothing to Tailwind (app, board, card, ...) are what the theme
// stylesheets in themes/ are written against; the Tailwind utilities only lay things out. The page
// is as wide as the window, for the board to use it; the header, the form and the messages keep to
// the width of a page.
export default function App() {
  const [load, setLoad] = useState<Load>('loading')
  const [attempt, setAttempt] = useState(0)
  const [categories, setCategories] = useState<Category[]>([])
  const [todos, setTodos] = useState<Todo[]>([])
  const [moving, setMoving] = useState(false)
  const [moveError, setMoveError] = useState<string | null>(null)
  // The description editors that are open, by todo id, with the text in them. They are here and
  // not in the cards because the board builds a card anew when its column changes, which happens
  // by itself when the AI classifies it.
  const [drafts, setDrafts] = useState<ReadonlyMap<number, string>>(new Map())
  // While a move is being saved, a second one is turned away. The card selects are off then, but
  // the cards stay draggable (a drag handle that goes away would take the keyboard focus with it),
  // so a drop can still arrive: it is ignored, and the card stays where the list says. Unlike
  // `moving`, the ref changes synchronously, so this holds before React has rendered the
  // disabled selects as well. The polling reads it too, to keep its answers away from a move.
  const moveInFlight = useRef(false)

  // Loads on mount and again whenever "Tekrar dene" bumps `attempt`.
  useEffect(() => {
    let stale = false
    Promise.all([getCategories(), getTodos()]).then(
      ([loadedCategories, loadedTodos]) => {
        if (stale) return
        setCategories(loadedCategories)
        setTodos(loadedTodos)
        setLoad('ready')
      },
      () => {
        if (!stale) setLoad('error')
      },
    )
    return () => {
      stale = true
    }
  }, [attempt])

  function retry() {
    setLoad('loading')
    setAttempt((current) => current + 1)
  }

  // The cards the AI has not answered for yet are asked after every second.
  const waiting = todos.some((todo) => todo.classification === 'pending')
  usePendingPolling(load === 'ready' && waiting, setTodos, moveInFlight)

  // The server's copy of a todo replaces the local one; the order stays as the board shows it. If
  // the copy says another column than the one the board knows (the AI classified the todo while
  // the request was open), the todo goes on top of that column, as any classified card does.
  function replaceTodo(saved: Todo) {
    setTodos((current) => {
      const local = current.find((todo) => todo.id === saved.id)
      if (local !== undefined && local.category_id !== saved.category_id) return placeOnTop(current, saved)
      return current.map((todo) => (todo.id === saved.id ? saved : todo))
    })
  }

  // A new todo is on top of Gelen kutusu, waiting for the AI.
  function handleCreated(created: Todo) {
    setTodos((current) => placeOnTop(current, created))
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
      replaceTodo(await updateTodo(todoId, { category_id: toCategoryId, position: toIndex }))
    } catch (err) {
      setTodos((current) => undoMove(current, before))
      setMoveError(errorText(err))
    } finally {
      moveInFlight.current = false
      setMoving(false)
    }
  }

  // The next three are not shown before the server has answered. A card that asks for one shows
  // the error of a failed request itself, so they let it reject.
  async function handleStatusChange(todoId: number, status: Status) {
    replaceTodo(await updateTodo(todoId, { status }))
  }

  function handleDraftChange(todoId: number, draft: string | undefined) {
    setDrafts((current) => {
      const next = new Map(current)
      if (draft === undefined) next.delete(todoId)
      else next.set(todoId, draft)
      return next
    })
  }

  async function handleSaveDescription(todoId: number, description: string) {
    replaceTodo(await updateTodo(todoId, { description }))
    handleDraftChange(todoId, undefined)
  }

  // The server has put the todo back on top of Gelen kutusu, waiting for the AI again.
  async function handleRetry(todoId: number) {
    const saved = await classifyTodo(todoId)
    setTodos((current) => placeOnTop(current, saved))
  }

  return (
    <>
      <main className="app space-y-6 p-4">
        <header className="app-header mx-auto flex w-full max-w-5xl flex-wrap items-center justify-between gap-3">
          <h1 className="app-title">Todo'lar</h1>
          <ThemeSwitcher />
        </header>
        {load === 'loading' && <p className="status">Yükleniyor...</p>}
        {load === 'error' && (
          <div className="space-y-2">
            <p role="alert" className="alert">
              Sunucuya ulaşılamadı.
            </p>
            <button type="button" onClick={retry} className="button px-3 py-1">
              Tekrar dene
            </button>
          </div>
        )}
        {load === 'ready' && (
          <>
            <AddTodoForm onCreated={handleCreated} />
            {moveError !== null && (
              <p role="alert" className="alert mx-auto w-full max-w-5xl">
                {`Todo taşınamadı: ${moveError}`}
              </p>
            )}
            <Board
              categories={categories}
              todos={todos}
              moving={moving}
              drafts={drafts}
              onMove={handleMove}
              onStatusChange={handleStatusChange}
              onDraftChange={handleDraftChange}
              onSaveDescription={handleSaveDescription}
              onRetry={handleRetry}
            />
          </>
        )}
      </main>
      {/* Fixed at the bottom right, whatever the board is doing; closed until the user opens it. */}
      <LogOverlay />
    </>
  )
}
