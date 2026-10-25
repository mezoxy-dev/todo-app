import type { Status } from './api'

// The statuses of a todo in the order the selects offer them, with the label the user reads.
export const statuses: { value: Status; label: string }[] = [
  { value: 'todo', label: 'Yapılacak' },
  { value: 'in_progress', label: 'Devam ediyor' },
  { value: 'done', label: 'Tamamlandı' },
]
