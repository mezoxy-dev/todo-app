import { useEffect, useRef } from 'react'
import type { Dispatch, RefObject, SetStateAction } from 'react'
import { getTodos } from './api'
import type { Todo } from './api'
import { applyClassifications } from './board'

export const POLL_INTERVAL_MS = 1000

// While `enabled` (some card waits for the AI), asks the server for the todos every second and lets
// applyClassifications put the cards that have been classified in their columns. Another request
// is never started before the one before it has ended, and a request that fails is simply asked
// again a second later.
//
// `moveInFlight` is true while a move by hand is being saved. An answer that comes back then is
// not used: the card the user has just moved shows where the user put it, and an answer that says
// the AI put it elsewhere would carry it back. The next answer is a fresh one. (Once the move is
// saved the card is the user's, `manual`, and an older answer cannot touch it: only the cards
// that are pending here are looked at.)
export function usePendingPolling(
  enabled: boolean,
  setTodos: Dispatch<SetStateAction<Todo[]>>,
  moveInFlight: RefObject<boolean>,
) {
  // Shared by every run of the effect, so that a request left open by an earlier run also keeps
  // the next one from starting.
  const asking = useRef(false)

  useEffect(() => {
    if (!enabled) return

    let stopped = false
    const timer = setInterval(() => {
      if (asking.current) return
      asking.current = true
      getTodos()
        .then(
          (fresh) => {
            if (!stopped && !moveInFlight.current) setTodos((current) => applyClassifications(current, fresh))
          },
          () => {
            // Not worth telling the user: the cards go on waiting and the next tick asks again.
          },
        )
        .finally(() => {
          asking.current = false
        })
    }, POLL_INTERVAL_MS)

    return () => {
      stopped = true
      clearInterval(timer)
    }
  }, [enabled, setTodos, moveInFlight])
}
