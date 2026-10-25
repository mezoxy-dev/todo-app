import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { createTodo, errorText } from '../api'
import type { Status, Todo } from '../api'
import { statuses } from '../statuses'

type Props = {
  onCreated: (todo: Todo) => void
}

// There is no category to choose: a new todo lands in Gelen kutusu and the AI picks its column.
export function AddTodoForm({ onCreated }: Props) {
  const [title, setTitle] = useState('')
  // The description field is closed until "Açıklama ekle" asks for it.
  const [describing, setDescribing] = useState(false)
  const [description, setDescription] = useState('')
  const [status, setStatus] = useState<Status>('todo')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const descriptionField = useRef<HTMLTextAreaElement>(null)
  // Unlike `submitting`, the ref changes synchronously, so a second submit that
  // arrives before React has re-rendered the disabled button is still ignored.
  const inFlight = useRef(false)

  // The button that opened the field is gone; the keyboard user goes on typing in the field.
  useEffect(() => {
    if (describing) descriptionField.current?.focus()
  }, [describing])

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = title.trim()
    if (trimmed === '' || inFlight.current) return

    inFlight.current = true
    setSubmitting(true)
    setError(null)
    try {
      const text = description.trim()
      const todo = await createTodo({ title: trimmed, ...(text === '' ? {} : { description: text }), status })
      onCreated(todo)
      // The next todo starts from scratch; the chosen status is kept, as the chosen column was.
      setTitle('')
      setDescription('')
      setDescribing(false)
    } catch (err) {
      setError(errorText(err))
    } finally {
      inFlight.current = false
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="add-form mx-auto w-full max-w-5xl space-y-2">
      <div className="flex flex-col gap-2 sm:flex-row">
        <input
          type="text"
          aria-label="Todo başlığı"
          placeholder="Yeni todo yaz..."
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          className="field add-input min-w-0 flex-1 px-3 py-2"
        />
        <select
          aria-label="Durum"
          value={status}
          // A <select> value is a string; it is one of the values offered below.
          onChange={(event) => setStatus(event.target.value as Status)}
          className="field add-select add-status px-3 py-2"
        >
          {statuses.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <button
          type="submit"
          disabled={submitting || title.trim() === ''}
          className="button add-button px-4 py-2"
        >
          Ekle
        </button>
      </div>
      {describing ? (
        <textarea
          ref={descriptionField}
          aria-label="Açıklama"
          placeholder="Açıklama (isteğe bağlı)"
          rows={2}
          value={description}
          onChange={(event) => setDescription(event.target.value)}
          className="field add-description block w-full px-3 py-2"
        />
      ) : (
        <button type="button" onClick={() => setDescribing(true)} className="add-description-toggle px-2 py-1">
          Açıklama ekle
        </button>
      )}
      {error !== null && (
        <p role="alert" className="alert">
          {`Todo eklenemedi: ${error}`}
        </p>
      )}
    </form>
  )
}
