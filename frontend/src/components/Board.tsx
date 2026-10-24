import { DragDropContext, Droppable } from '@hello-pangea/dnd'
import type { DropResult } from '@hello-pangea/dnd'
import type { Category, Todo } from '../api'
import { TodoCard } from './TodoCard'

type Props = {
  categories: Category[]
  todos: Todo[]
  // True while a move is being saved: the card selects are off then. The cards stay draggable
  // (see TodoCard); App turns a second move away.
  moving: boolean
  onMove: (todoId: number, toCategoryId: number, toIndex: number) => void
}

export function Board({ categories, todos, moving, onMove }: Props) {
  // The drag library knows its ids as strings: a drop area is a category, a draggable a todo.
  function handleDragEnd({ draggableId, source, destination }: DropResult) {
    // Dropped nowhere, or back on the very place it was picked up from.
    if (!destination) return
    if (destination.droppableId === source.droppableId && destination.index === source.index) return
    onMove(Number(draggableId), Number(destination.droppableId), destination.index)
  }

  return (
    <DragDropContext onDragEnd={handleDragEnd}>
      <div className="board grid gap-4 md:grid-cols-3">
        {categories.map((category) => {
          // Filtering keeps the order of the list inside every column.
          const cards = todos.filter((todo) => todo.category_id === category.id)
          return (
            <section
              key={category.id}
              aria-label={category.name}
              data-column={category.position}
              className="column flex min-w-0 flex-col p-3"
            >
              <h2 className="column-title mb-3">{`${category.name} (${cards.length})`}</h2>
              {/* The drop area is there (and tall enough to aim at) in an empty column too. */}
              <Droppable droppableId={String(category.id)}>
                {(provided, snapshot) => (
                  <ul
                    ref={provided.innerRef}
                    {...provided.droppableProps}
                    // Whole class lists again, so that Tailwind finds each utility standing alone.
                    className={
                      snapshot.isDraggingOver
                        ? 'card-list min-h-16 flex-1 is-drag-over'
                        : 'card-list min-h-16 flex-1'
                    }
                  >
                    {cards.length === 0 && <p className="empty">Boş</p>}
                    {cards.map((todo, index) => (
                      <TodoCard
                        key={todo.id}
                        todo={todo}
                        index={index}
                        categories={categories}
                        disabled={moving}
                        onMove={onMove}
                      />
                    ))}
                    {provided.placeholder}
                  </ul>
                )}
              </Droppable>
            </section>
          )
        })}
      </div>
    </DragDropContext>
  )
}
