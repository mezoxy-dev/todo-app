import type { Category, Todo } from '../api'
import { TodoCard } from './TodoCard'

type Props = {
  categories: Category[]
  todos: Todo[]
  onUpdated: (todo: Todo) => void
}

export function Board({ categories, todos, onUpdated }: Props) {
  return (
    <div className="grid gap-4 md:grid-cols-3">
      {categories.map((category) => {
        // Filtering keeps the API order (newest first) inside every column.
        const cards = todos.filter((todo) => todo.category_id === category.id)
        return (
          <section
            key={category.id}
            aria-label={category.name}
            className="min-w-0 rounded-lg bg-slate-100 p-3"
          >
            <h2 className="mb-3 font-semibold">{`${category.name} (${cards.length})`}</h2>
            {cards.length === 0 ? (
              <p className="text-sm text-slate-500">Boş</p>
            ) : (
              <ul className="space-y-2">
                {cards.map((todo) => (
                  <li key={todo.id} className="rounded-md bg-white p-3 shadow-sm">
                    <TodoCard todo={todo} categories={categories} onUpdated={onUpdated} />
                  </li>
                ))}
              </ul>
            )}
          </section>
        )
      })}
    </div>
  )
}
