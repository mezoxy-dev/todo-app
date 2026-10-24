import { Draggable } from '@hello-pangea/dnd'
import type { Category, Todo } from '../api'

type Props = {
  todo: Todo
  // Place of the card in its column; the drag library needs it.
  index: number
  categories: Category[]
  // True while a move is being saved: the select is off then. The card itself stays draggable (a
  // drag handle that goes away would take the keyboard focus with it); App turns a second move away.
  disabled: boolean
  onMove: (todoId: number, toCategoryId: number, toIndex: number) => void
}

export function TodoCard({ todo, index, categories, disabled, onMove }: Props) {
  return (
    <Draggable draggableId={String(todo.id)} index={index}>
      {(provided, snapshot) => (
        // The whole card is the handle. The library marks a handle as a button, which would hide
        // the select inside from assistive technology, so it stays a list item. The space between
        // cards is padding on this element: the library shifts cards by their own size, which a
        // flex or grid gap would not be part of.
        <li
          ref={provided.innerRef}
          {...provided.draggableProps}
          {...provided.dragHandleProps}
          role="listitem"
          className={`card pb-2${snapshot.isDragging ? ' is-dragging' : ''}`}
        >
          <div className="card-body space-y-2 p-3">
            <h3 className="card-title break-words">{todo.title}</h3>
            {/* Controlled by the todo, so it follows the card wherever it is moved to or put
                back to. Picking a column puts the card on top of it. */}
            <select
              aria-label={`${todo.title} için sütun`}
              value={todo.category_id}
              disabled={disabled}
              // A <select> value is a string, but the API wants a number.
              onChange={(event) => onMove(todo.id, Number(event.target.value), 0)}
              className="card-select w-full px-2 py-1"
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
