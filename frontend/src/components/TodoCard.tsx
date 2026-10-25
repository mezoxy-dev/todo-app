import { Draggable } from '@hello-pangea/dnd'
import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { errorText } from '../api'
import type { Category, Status, Todo } from '../api'
import { statuses } from '../statuses'

// What a card can ask for. The status, the description and the retry are saved before the card
// shows them; their promises reject with the server's error, which the card then shows itself.
export type CardActions = {
  onMove: (todoId: number, toCategoryId: number, toIndex: number) => void
  onStatusChange: (todoId: number, status: Status) => Promise<void>
  // Opens (a text), changes (a text) or closes (undefined) the description editor of a todo.
  onDraftChange: (todoId: number, draft: string | undefined) => void
  onSaveDescription: (todoId: number, description: string) => Promise<void>
  onRetry: (todoId: number) => Promise<void>
}

type Props = CardActions & {
  todo: Todo
  // Place of the card in its column; the drag library needs it.
  index: number
  categories: Category[]
  // True while a move is being saved: the selects are off then. The card itself stays draggable (a
  // drag handle that goes away would take the keyboard focus with it); App turns a second move away.
  disabled: boolean
  // The text in the description editor, or undefined while there is no editor. It is App that holds
  // it: a card whose column changes (the AI has classified it) is built anew by the board, and an
  // editor that is open must not be lost with the card.
  draft: string | undefined
}

export function TodoCard({
  todo,
  index,
  categories,
  disabled,
  draft,
  onMove,
  onStatusChange,
  onDraftChange,
  onSaveDescription,
  onRetry,
}: Props) {
  // What a request of this card is doing right now. It is gone with the card, which is fine for
  // something that lasts a moment.
  const [pendingStatus, setPendingStatus] = useState<Status | null>(null)
  const [savingDescription, setSavingDescription] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const editor = useRef<HTMLTextAreaElement>(null)
  const pencil = useRef<HTMLButtonElement>(null)
  // Set by the click that opens the editor. Only that click moves the focus into the editor, not
  // an editor that is open again because the card has been rebuilt.
  const focusEditor = useRef(false)

  const editing = draft !== undefined

  useEffect(() => {
    if (editing && focusEditor.current) {
      focusEditor.current = false
      editor.current?.focus()
    }
  }, [editing])

  function openEditor() {
    if (editing) {
      editor.current?.focus()
      return
    }
    focusEditor.current = true
    setError(null)
    onDraftChange(todo.id, todo.description)
  }

  // The keyboard user is put back on the button that opened the editor.
  function closeEditor() {
    onDraftChange(todo.id, undefined)
    pencil.current?.focus()
  }

  async function saveDescription(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (draft === undefined || savingDescription) return
    // The server keeps the trimmed text; there is nothing to save when that is what it has.
    const next = draft.trim()
    if (next === todo.description) {
      closeEditor()
      return
    }

    setError(null)
    setSavingDescription(true)
    try {
      // App closes the editor once the description is saved.
      await onSaveDescription(todo.id, next)
      pencil.current?.focus()
    } catch (err) {
      setError(`Açıklama kaydedilemedi: ${errorText(err)}`)
    } finally {
      setSavingDescription(false)
    }
  }

  async function changeStatus(next: Status) {
    setError(null)
    setPendingStatus(next)
    try {
      await onStatusChange(todo.id, next)
    } catch (err) {
      setError(`Durum değiştirilemedi: ${errorText(err)}`)
    } finally {
      setPendingStatus(null)
    }
  }

  async function retry() {
    setError(null)
    setRetrying(true)
    try {
      await onRetry(todo.id)
    } catch (err) {
      setError(`Yeniden sınıflandırma başlatılamadı: ${errorText(err)}`)
    } finally {
      setRetrying(false)
    }
  }

  return (
    <Draggable draggableId={String(todo.id)} index={index}>
      {(provided, snapshot) => (
        // The whole card is the handle. The library marks a handle as a button, which would hide
        // the selects inside from assistive technology, so it stays a list item. The space between
        // cards is padding on this element: the library shifts cards by their own size, which a
        // flex or grid gap would not be part of.
        <li
          ref={provided.innerRef}
          {...provided.draggableProps}
          {...provided.dragHandleProps}
          role="listitem"
          data-status={todo.status}
          data-classification={todo.classification}
          // Each branch is a whole class list: Tailwind only finds a utility that stands alone in
          // a string, not one glued to a `${...}`.
          className={snapshot.isDragging ? 'card pb-2 is-dragging' : 'card pb-2'}
        >
          <div className="card-body space-y-2 p-3">
            <h3 className="card-title break-words">{todo.title}</h3>
            {todo.classification === 'pending' && <span className="badge badge-pending">sınıflandırılıyor…</span>}
            {todo.classification === 'failed' && (
              <div className="flex flex-wrap items-center gap-2">
                <span className="badge badge-failed">sınıflandırılamadı</span>
                {/* Other cards (and the load error) have a "Tekrar dene" too, so the name says whose. */}
                <button
                  type="button"
                  aria-label={`${todo.title} için tekrar dene`}
                  disabled={retrying}
                  onClick={retry}
                  className="retry-button px-2 py-1"
                >
                  Tekrar dene
                </button>
              </div>
            )}
            {/* Two lines of it are enough on the card; the editor shows all of it. */}
            {!editing && todo.description !== '' && (
              <p className="card-description line-clamp-2 break-words">{todo.description}</p>
            )}
            <button
              ref={pencil}
              type="button"
              aria-label="Açıklamayı düzenle"
              aria-expanded={editing}
              onClick={openEditor}
              className="description-edit px-2 py-1"
            >
              ✎
            </button>
            {editing && (
              <form onSubmit={saveDescription} className="description-editor space-y-2">
                <textarea
                  ref={editor}
                  aria-label={`${todo.title} açıklaması`}
                  rows={3}
                  value={draft}
                  disabled={savingDescription}
                  onChange={(event) => onDraftChange(todo.id, event.target.value)}
                  className="w-full px-2 py-1"
                />
                <div className="flex gap-2">
                  <button type="submit" disabled={savingDescription} className="px-3 py-1">
                    Kaydet
                  </button>
                  <button type="button" disabled={savingDescription} onClick={closeEditor} className="px-3 py-1">
                    Vazgeç
                  </button>
                </div>
              </form>
            )}
            {error !== null && (
              <p role="alert" className="alert break-words">
                {error}
              </p>
            )}
            {/* Not saved before the server has answered, so it shows the choice that is on its way
                and turns itself off meanwhile; a failure puts the old status back. */}
            <select
              aria-label={`${todo.title} için durum`}
              value={pendingStatus ?? todo.status}
              disabled={disabled || pendingStatus !== null}
              // A <select> value is a string; it is one of the values offered below.
              onChange={(event) => changeStatus(event.target.value as Status)}
              className="card-select card-status w-full px-2 py-1"
            >
              {statuses.map((status) => (
                <option key={status.value} value={status.value}>
                  {status.label}
                </option>
              ))}
            </select>
            {/* Controlled by the todo, so it follows the card wherever it is moved to or put
                back to. Picking a category puts the card on top of it. */}
            <select
              aria-label={`${todo.title} için kategori`}
              value={todo.category_id}
              disabled={disabled}
              // A <select> value is a string, but the API wants a number.
              onChange={(event) => onMove(todo.id, Number(event.target.value), 0)}
              className="card-select card-category w-full px-2 py-1"
            >
              {categories.map((category) => (
                <option key={category.id} value={category.id}>
                  {category.name}
                </option>
              ))}
            </select>
          </div>
        </li>
      )}
    </Draggable>
  )
}
