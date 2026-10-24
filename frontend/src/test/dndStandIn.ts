import type {
  DragDropContextProps,
  DraggableProps,
  DraggableProvided,
  DraggableStateSnapshot,
  DroppableProps,
  DroppableProvided,
  DroppableStateSnapshot,
  OnDragEndResponder,
} from '@hello-pangea/dnd'

// jsdom has no layout, so no card can really be dragged in a unit test (a browser run does
// that). This stand-in takes the place of @hello-pangea/dnd: it renders the library's children
// as they are, hands out the snapshots a test chose, and keeps the onDragEnd it was given so a
// test can report a finished drag itself with `drop`. A test file switches it on with
//   vi.mock('@hello-pangea/dnd', () => import('<path to>/test/dndStandIn'))
// App.test.tsx does not: it renders the real library.
export const standIn = {
  onDragEnd: undefined as OnDragEndResponder | undefined,
  dragging: new Set<string>(),
  hovered: new Set<string>(),
  // The isDragDisabled each Draggable was rendered with, by draggable id.
  dragDisabled: new Map<string, boolean>(),
}

export function resetStandIn() {
  standIn.onDragEnd = undefined
  standIn.dragging.clear()
  standIn.hovered.clear()
  standIn.dragDisabled.clear()
}

export type Place = { droppableId: string; index: number }

// A finished drag, as the library reports it.
export function drop(draggableId: string, source: Place, destination: Place | null) {
  if (!standIn.onDragEnd) {
    throw new Error('the stand-in drag context has no onDragEnd: is a board rendered?')
  }
  standIn.onDragEnd(
    { draggableId, type: 'DEFAULT', source, destination, reason: 'DROP', mode: 'FLUID', combine: null },
    { announce: () => {} },
  )
}

export function DragDropContext({ onDragEnd, children }: DragDropContextProps) {
  standIn.onDragEnd = onDragEnd
  return children
}

export function Droppable({ droppableId, children }: DroppableProps) {
  return children(
    { innerRef: () => {}, droppableProps: {}, placeholder: null } as unknown as DroppableProvided,
    { isDraggingOver: standIn.hovered.has(droppableId) } as DroppableStateSnapshot,
  )
}

export function Draggable({ draggableId, isDragDisabled = false, children }: DraggableProps) {
  standIn.dragDisabled.set(draggableId, isDragDisabled)
  return children(
    { innerRef: () => {}, draggableProps: {}, dragHandleProps: null } as unknown as DraggableProvided,
    { isDragging: standIn.dragging.has(draggableId) } as DraggableStateSnapshot,
    { draggableId, type: 'DEFAULT', source: { droppableId: '1', index: 0 } },
  )
}
