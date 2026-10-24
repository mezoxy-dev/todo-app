import { useState } from 'react'
import type { ChangeEvent } from 'react'
import { updateTodo } from '../api'
import type { Category, Todo } from '../api'

type Props = {
  todo: Todo
  categories: Category[]
  onUpdated: (todo: Todo) => void
}

export function TodoCard({ todo, categories, onUpdated }: Props) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleMove(event: ChangeEvent<HTMLSelectElement>) {
    // A <select> value is a string, but the API wants a number.
    const categoryId = Number(event.target.value)
    setPending(true)
    setError(null)
    try {
      onUpdated(await updateTodo(todo.id, { category_id: categoryId }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setPending(false)
    }
  }

  return (
    <div className="space-y-2">
      <h3 className="break-words font-medium">{todo.title}</h3>
      {/* Controlled by the todo itself: the card only changes column once the
          server has answered, and a failed move leaves the select untouched. */}
      <select
        aria-label={`${todo.title} için sütun`}
        value={todo.category_id}
        disabled={pending}
        onChange={handleMove}
        className="w-full rounded-md border px-2 py-1 text-sm"
      >
        {categories.map((category) => (
          <option key={category.id} value={category.id}>
            {category.name}
          </option>
        ))}
      </select>
      {error !== null && (
        <p role="alert" className="text-sm text-red-700">
          {`Todo taşınamadı: ${error}`}
        </p>
      )}
    </div>
  )
}
