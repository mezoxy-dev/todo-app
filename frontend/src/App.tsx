import { useEffect, useState } from 'react'
import { getCategories, getTodos } from './api'
import type { Category, Todo } from './api'
import { AddTodoForm } from './components/AddTodoForm'
import { Board } from './components/Board'

type Status = 'loading' | 'error' | 'ready'

export default function App() {
  const [status, setStatus] = useState<Status>('loading')
  const [attempt, setAttempt] = useState(0)
  const [categories, setCategories] = useState<Category[]>([])
  const [todos, setTodos] = useState<Todo[]>([])

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

  // New todos go to the front: the API lists newest first.
  function handleCreated(created: Todo) {
    setTodos((current) => [created, ...current])
  }

  // Swap the updated todo in place so the list keeps the API order.
  function handleUpdated(updated: Todo) {
    setTodos((current) => current.map((todo) => (todo.id === updated.id ? updated : todo)))
  }

  return (
    <main className="mx-auto max-w-5xl space-y-6 p-4">
      <h1 className="text-2xl font-bold">Todo'lar</h1>
      {status === 'loading' && <p>Yükleniyor...</p>}
      {status === 'error' && (
        <div className="space-y-2">
          <p role="alert" className="text-red-700">
            Sunucuya ulaşılamadı.
          </p>
          <button type="button" onClick={retry} className="rounded-md border px-3 py-1">
            Tekrar dene
          </button>
        </div>
      )}
      {status === 'ready' && (
        <>
          <AddTodoForm categories={categories} onCreated={handleCreated} />
          <Board categories={categories} todos={todos} onUpdated={handleUpdated} />
        </>
      )}
    </main>
  )
}
