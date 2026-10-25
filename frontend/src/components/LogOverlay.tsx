import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { getLogs } from '../api'
import type { LogEntry } from '../api'
import { POLL_INTERVAL_MS } from '../usePendingPolling'

// How many of the latest entries the panel loads when it opens.
const RECENT = 200

// The server says "system"; the panel is in Turkish.
const sourceLabels: Record<LogEntry['source'], string> = { api: 'api', ai: 'ai', system: 'sistem' }

const two = (value: number) => String(value).padStart(2, '0')

// The time of the day, HH:MM:SS, where the person who looks is.
function formatTime(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return `${two(date.getHours())}:${two(date.getMinutes())}:${two(date.getSeconds())}`
}

// Whether a list is scrolled down to its end; a pixel or two short of it still counts.
function atBottom(list: HTMLElement): boolean {
  return list.scrollHeight - list.scrollTop - list.clientHeight <= 2
}

type RowProps = {
  entry: LogEntry
  open: boolean
  onToggle: () => void
}

// One entry is one line: time, source, message (cut with an ellipsis when it is too long). An
// entry with details is a button that opens them below the line; one without is just a line.
function LogRow({ entry, open, onToggle }: RowProps) {
  const cells = (
    <>
      <time dateTime={entry.time} className="log-time shrink-0 whitespace-nowrap">
        {formatTime(entry.time)}
      </time>
      <span className="log-source shrink-0 whitespace-nowrap">{sourceLabels[entry.source]}</span>
      <span className="log-message min-w-0 flex-1 truncate">{entry.message}</span>
    </>
  )

  if (entry.details === undefined || entry.details === '') {
    return (
      <li>
        <div className="log-entry flex items-baseline gap-2" data-level={entry.level} data-source={entry.source}>
          <span aria-hidden="true" className="log-chevron w-4 shrink-0" />
          {cells}
        </div>
      </li>
    )
  }

  return (
    <li>
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        data-level={entry.level}
        data-source={entry.source}
        // Whole class lists again, so that Tailwind finds each utility standing alone.
        className={
          open
            ? 'log-entry is-expanded flex w-full items-baseline gap-2 text-left'
            : 'log-entry flex w-full items-baseline gap-2 text-left'
        }
      >
        <span aria-hidden="true" className="log-chevron w-4 shrink-0">
          {open ? '▾' : '▸'}
        </span>
        {cells}
      </button>
      {open && <pre className="log-details max-h-48 overflow-auto whitespace-pre-wrap break-words">{entry.details}</pre>}
    </li>
  )
}

// The open panel: it loads the latest entries when it appears and asks for the new ones every
// second for as long as it is there. Closed, it does not exist, so nothing is requested.
function LogPanel({ onHide }: { onHide: () => void }) {
  // Oldest first, so that the newest is at the bottom. Null until the server has answered once, so
  // that "Henüz log yok." is not claimed before it is known.
  const [entries, setEntries] = useState<LogEntry[] | null>(null)
  // The numbers of the entries whose details are open.
  const [opened, setOpened] = useState<ReadonlySet<number>>(new Set())
  const titleId = useId()
  const list = useRef<HTMLUListElement>(null)
  // Whether the user has the list scrolled to its end; only then does a new entry scroll it.
  const followEnd = useRef(true)
  // Where the polling stands. Refs, so that when React runs the effect a second time (StrictMode)
  // it takes up where the first run was and does not ask twice.
  const lastSeq = useRef<number | null>(null)
  const asking = useRef(false)
  const mounted = useRef(false)

  useEffect(() => {
    mounted.current = true

    function load() {
      // Another request is not started before the one before it has ended.
      if (asking.current) return
      asking.current = true
      const after = lastSeq.current
      getLogs(after ?? undefined, after === null ? RECENT : undefined)
        .then(
          (reply) => {
            if (!mounted.current) return
            if (after !== null && reply.last_seq < after) {
              // The server has started over and its numbers with it: ask for the recent ones again,
              // and show them from their end, as when the panel is opened.
              lastSeq.current = null
              followEnd.current = true
              setEntries(null)
              setOpened(new Set())
              return
            }
            lastSeq.current = reply.last_seq
            setEntries((current) => {
              if (after === null || current === null) return reply.entries
              return reply.entries.length === 0 ? current : [...current, ...reply.entries]
            })
          },
          () => {
            // Not worth telling the user: the panel keeps what it shows and the next tick asks again.
          },
        )
        .finally(() => {
          asking.current = false
        })
    }

    load()
    const timer = setInterval(load, POLL_INTERVAL_MS)
    return () => {
      mounted.current = false
      clearInterval(timer)
    }
  }, [])

  // A new entry is brought into view for a user who is at the end of the list; one who has
  // scrolled up to read is left alone. Opening the details of an entry moves nothing: they open
  // below the entry, where the user is looking.
  useLayoutEffect(() => {
    const element = list.current
    if (element !== null && followEnd.current) element.scrollTop = element.scrollHeight
  }, [entries])

  function toggle(seq: number) {
    setOpened((current) => {
      const next = new Set(current)
      if (!next.delete(seq)) next.add(seq)
      return next
    })
  }

  return (
    <section
      role="log"
      aria-labelledby={titleId}
      className="log-panel flex max-h-[60vh] w-[min(36rem,calc(100vw-2rem))] flex-col gap-2 p-3"
    >
      <div className="log-header flex items-center justify-between gap-2">
        <h2 id={titleId} className="log-title">
          Loglar
        </h2>
        <button type="button" onClick={onHide} className="log-hide px-2 py-1">
          Gizle
        </button>
      </div>
      {entries !== null && entries.length === 0 && <p className="log-empty">Henüz log yok.</p>}
      {entries !== null && entries.length > 0 && (
        <ul
          ref={list}
          onScroll={(event) => {
            followEnd.current = atBottom(event.currentTarget)
          }}
          className="log-list min-h-0 flex-1 overflow-y-auto"
        >
          {entries.map((entry) => (
            <LogRow key={entry.seq} entry={entry} open={opened.has(entry.seq)} onToggle={() => toggle(entry.seq)} />
          ))}
        </ul>
      )}
    </section>
  )
}

// The button at the bottom right, and the panel above it when it is open. The panel is closed
// every time the page opens.
export function LogOverlay() {
  const [open, setOpen] = useState(false)
  const toggle = useRef<HTMLButtonElement>(null)

  // The keyboard user is put back on the button that opened the panel.
  function hide() {
    setOpen(false)
    toggle.current?.focus()
  }

  return (
    // The button comes first in the page, so that Tab leads from it into the panel, which is drawn
    // above it.
    <div className="log-overlay fixed right-4 bottom-4 z-50 flex flex-col-reverse items-end gap-2">
      <button
        ref={toggle}
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
        className="log-toggle px-3 py-2"
      >
        Loglar
      </button>
      {open && <LogPanel onHide={hide} />}
    </div>
  )
}
