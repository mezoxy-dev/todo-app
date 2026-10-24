import { useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { createTodo } from '../api'
import type { Category, Todo } from '../api'

type Props = {
  categories: Category[]
  onCreated: (todo: Todo) => void
}

export function AddTodoForm({ categories, onCreated }: Props) {
  const [title, setTitle] = useState('')
  const [categoryId, setCategoryId] = useState<number | undefined>(categories[0]?.id)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Unlike `submitting`, the ref changes synchronously, so a second submit that
  // arrives before React has re-rendered the disabled button is still ignored.
  const inFlight = useRef(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = title.trim()
    if (trimmed === '' || inFlight.current) return

    inFlight.current = true
    setSubmitting(true)
    setError(null)
    try {
      const todo = await createTodo({ title: trimmed, category_id: categoryId })
      onCreated(todo)
      setTitle('')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      inFlight.current = false
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-2">
      <div className="flex flex-col gap-2 sm:flex-row">
        <input
          type="text"
          aria-label="Todo başlığı"
          placeholder="Yeni todo yaz..."
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          className="min-w-0 flex-1 rounded-md border px-3 py-2"
        />
        <select
          aria-label="Sütun"
          value={categoryId}
          onChange={(event) => setCategoryId(Number(event.target.value))}
          className="rounded-md border px-3 py-2"
        >
          {categories.map((category) => (
            <option key={category.id} value={category.id}>
              {category.name}
            </option>
          ))}
        </select>
        <button
          type="submit"
          disabled={submitting || title.trim() === ''}
          className="rounded-md bg-slate-800 px-4 py-2 text-white disabled:opacity-50"
        >
          Ekle
        </button>
      </div>
      {error !== null && (
        <p role="alert" className="text-red-700">
          {`Todo eklenemedi: ${error}`}
        </p>
      )}
    </form>
  )
}
