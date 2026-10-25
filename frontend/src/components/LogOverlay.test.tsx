import { fireEvent, render, screen, within } from '@testing-library/react'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Mock } from 'vitest'
import type { LogEntry, LogPage } from '../api'
import { advance, deferred } from '../test/appHelpers'
import { mockApi } from '../test/mockApi'
import type { MockReply } from '../test/mockApi'
import { LogOverlay } from './LogOverlay'

// The panel polls every second, so time is the test's to move. A test reads the page after
// advance(), which also lets the replies of the mocked fetch settle. user-event is not used: it
// waits for a timer of its own, which the fake time never fires.
beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

// The ISO time of a moment today in the local time zone, so that "12:00:05" is "12:00:05" wherever
// the tests run: the panel shows the time of the day of the person who looks.
function at(hours: number, minutes: number, seconds: number): string {
  return new Date(2026, 9, 25, hours, minutes, seconds).toISOString()
}

function entry(seq: number, overrides: Partial<LogEntry> = {}): LogEntry {
  return {
    seq,
    time: at(12, 0, seq),
    level: 'info',
    source: 'api',
    message: `Kayıt ${seq}`,
    ...overrides,
  }
}

function page(entries: LogEntry[], overrides: Partial<LogPage> = {}): MockReply {
  const last = entries.at(-1)?.seq ?? 0
  return { body: { entries, last_seq: last, truncated: false, ...overrides } }
}

// The paths of the log requests made so far.
function logRequests(fetchMock: Mock): string[] {
  return fetchMock.mock.calls.map(([url]) => String(url)).filter((url) => url.startsWith('/api/logs'))
}

function open() {
  fireEvent.click(screen.getByRole('button', { name: 'Loglar' }))
}

// The entries on the panel, top to bottom.
function rows(): HTMLElement[] {
  return Array.from(screen.getByRole('log').querySelectorAll<HTMLElement>('.log-entry'))
}

describe('log panel', () => {
  it('is hidden by default and makes no request', async () => {
    // No route at all: a request would fail the test.
    const fetchMock = mockApi({})
    render(<LogOverlay />)

    const toggle = screen.getByRole('button', { name: 'Loglar' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('log')).not.toBeInTheDocument()
    expect(screen.queryByText('Henüz log yok.')).not.toBeInTheDocument()

    await advance(5000)

    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.queryByRole('log')).not.toBeInTheDocument()
  })

  it('opens, loads recent logs and polls with after', async () => {
    const fetchMock = mockApi({
      'GET /api/logs?limit=200': () => page([entry(1), entry(2)]),
      'GET /api/logs?after=2': () => page([entry(3)], { last_seq: 3 }),
      'GET /api/logs?after=3': () => page([], { last_seq: 3 }),
    })
    render(<LogOverlay />)

    open()
    await advance()

    expect(screen.getByRole('button', { name: 'Loglar' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('log')).toBeInTheDocument()
    // The recent ones at once, without waiting for a second.
    expect(logRequests(fetchMock)).toEqual(['/api/logs?limit=200'])
    expect(rows()).toHaveLength(2)

    // From then on only what comes after the last number, a second apart.
    await advance(999)
    expect(logRequests(fetchMock)).toHaveLength(1)
    await advance(1)
    expect(logRequests(fetchMock)).toEqual(['/api/logs?limit=200', '/api/logs?after=2'])
    expect(rows().map((row) => row.querySelector('.log-message')?.textContent)).toEqual([
      'Kayıt 1',
      'Kayıt 2',
      'Kayıt 3',
    ])

    // Nothing new: the number stays where it was and is asked again.
    await advance(1000)
    await advance(1000)
    expect(logRequests(fetchMock)).toEqual([
      '/api/logs?limit=200',
      '/api/logs?after=2',
      '/api/logs?after=3',
      '/api/logs?after=3',
    ])
    expect(rows()).toHaveLength(3)
  })

  it('shows each entry on one line: time, source, message', async () => {
    const long = 'Sınıflandırıldı: “' + 'çok uzun bir başlık '.repeat(20) + '” → Satın alım (0,4 sn)'
    mockApi({
      'GET /api/logs?limit=200': () =>
        page([
          entry(1, { time: at(12, 0, 5), source: 'api', level: 'info', message: 'Todo eklendi: “Süt al”' }),
          entry(2, { time: at(9, 4, 3), source: 'ai', level: 'warn', message: long }),
          entry(3, { time: at(23, 59, 59), source: 'system', level: 'error', message: 'Sunucu başladı (model: x)' }),
        ]),
    })
    render(<LogOverlay />)
    open()
    await advance()

    const [first, second, third] = rows()
    // Oldest first: the newest entry is the one at the bottom.
    expect(first.querySelector('.log-message')).toHaveTextContent('Todo eklendi: “Süt al”')
    expect(third.querySelector('.log-message')).toHaveTextContent('Sunucu başladı (model: x)')

    expect(first.querySelector('.log-time')).toHaveTextContent(/^12:00:05$/)
    expect(second.querySelector('.log-time')).toHaveTextContent(/^09:04:03$/)
    expect(third.querySelector('.log-time')).toHaveTextContent(/^23:59:59$/)
    expect(first.querySelector('.log-source')).toHaveTextContent(/^api$/)
    expect(second.querySelector('.log-source')).toHaveTextContent(/^ai$/)
    // The server says "system"; the panel says "sistem".
    expect(third.querySelector('.log-source')).toHaveTextContent(/^sistem$/)

    expect(first).toHaveAttribute('data-level', 'info')
    expect(first).toHaveAttribute('data-source', 'api')
    expect(second).toHaveAttribute('data-level', 'warn')
    expect(second).toHaveAttribute('data-source', 'ai')
    expect(third).toHaveAttribute('data-level', 'error')
    expect(third).toHaveAttribute('data-source', 'system')

    // A line is a line however long the message is: it is cut with an ellipsis, not wrapped.
    const message = second.querySelector('.log-message')
    expect(message).toHaveTextContent(long)
    expect(message).toHaveClass('truncate', 'min-w-0', 'flex-1')
    for (const row of [first, second, third]) {
      expect(row).toHaveClass('flex')
      expect(row.querySelector('.log-time')).toHaveClass('shrink-0', 'whitespace-nowrap')
      expect(row.querySelector('.log-source')).toHaveClass('shrink-0', 'whitespace-nowrap')
    }
  })

  it('expands and collapses only entries that have details', async () => {
    const details = 'Model: x\nGiden metin: Başlık: Süt al\nCevap: {"category":"purchase"}'
    mockApi({
      'GET /api/logs?limit=200': () =>
        page([
          entry(1, { message: 'Sınıflandırıldı: “Süt al” → Satın alım (0,4 sn)', source: 'ai', details }),
          entry(2, { message: 'Todo eklendi: “Süt al”' }),
        ]),
    })
    render(<LogOverlay />)
    open()
    await advance()

    const [withDetails, without] = rows()
    // The entry with details is a button that says whether it is open; the other one is not.
    const button = within(screen.getByRole('log')).getByRole('button', { name: /Sınıflandırıldı/ })
    expect(button).toBe(withDetails)
    expect(button).toHaveAttribute('aria-expanded', 'false')
    expect(button).not.toHaveClass('is-expanded')
    expect(button).toHaveTextContent('▸')
    expect(screen.getByRole('log').querySelector('.log-details')).toBeNull()
    expect(without.tagName).not.toBe('BUTTON')
    expect(without.closest('button')).toBeNull()
    expect(within(without).queryByRole('button')).not.toBeInTheDocument()
    expect(without).not.toHaveAttribute('aria-expanded')
    expect(without.textContent).not.toContain('▸')

    fireEvent.click(button)

    expect(button).toHaveAttribute('aria-expanded', 'true')
    expect(button).toHaveClass('is-expanded')
    expect(button).toHaveTextContent('▾')
    const shown = screen.getByRole('log').querySelector('.log-details')
    expect(shown).not.toBeNull()
    // Below the line, with its line breaks.
    expect(shown?.textContent).toBe(details)
    expect(button.nextElementSibling).toBe(shown)
    // The other entry has nothing to open, and clicking it changes nothing.
    fireEvent.click(without)
    expect(screen.getByRole('log').querySelectorAll('.log-details')).toHaveLength(1)
    expect(rows()).toHaveLength(2)

    fireEvent.click(button)

    expect(button).toHaveAttribute('aria-expanded', 'false')
    expect(button).not.toHaveClass('is-expanded')
    expect(button).toHaveTextContent('▸')
    expect(screen.getByRole('log').querySelector('.log-details')).toBeNull()
  })

  it('keeps an entry open while new entries arrive', async () => {
    mockApi({
      'GET /api/logs?limit=200': () => page([entry(1, { details: 'ayrıntı' })]),
      'GET /api/logs?after=1': () => page([entry(2)]),
    })
    render(<LogOverlay />)
    open()
    await advance()
    fireEvent.click(rows()[0])

    await advance(1000)

    expect(rows()).toHaveLength(2)
    expect(rows()[0]).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('ayrıntı')).toHaveClass('log-details')
  })

  it('hides with Gizle and stops polling', async () => {
    const fetchMock = mockApi({
      'GET /api/logs?limit=200': () => page([entry(1)]),
      'GET /api/logs?after=1': () => page([], { last_seq: 1 }),
    })
    render(<LogOverlay />)
    open()
    await advance()
    await advance(1000)
    expect(logRequests(fetchMock)).toHaveLength(2)

    fireEvent.click(within(screen.getByRole('log')).getByRole('button', { name: 'Gizle' }))

    expect(screen.queryByRole('log')).not.toBeInTheDocument()
    const toggle = screen.getByRole('button', { name: 'Loglar' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    // The keyboard user is back on the button that opened it.
    expect(toggle).toHaveFocus()
    await advance(5000)
    expect(logRequests(fetchMock)).toHaveLength(2)
  })

  it('closes with the Loglar button too, and loads afresh every time it is opened', async () => {
    const fetchMock = mockApi({
      'GET /api/logs?limit=200': () => page([entry(1), entry(2)]),
      'GET /api/logs?after=2': () => page([], { last_seq: 2 }),
    })
    render(<LogOverlay />)
    open()
    await advance()
    await advance(1000)
    open()
    expect(screen.queryByRole('log')).not.toBeInTheDocument()

    open()
    await advance()

    // A new load, not a continuation: the panel did not keep anything while it was away.
    expect(logRequests(fetchMock)).toEqual([
      '/api/logs?limit=200',
      '/api/logs?after=2',
      '/api/logs?limit=200',
    ])
    expect(rows()).toHaveLength(2)
  })

  it('shows "Henüz log yok." when empty', async () => {
    mockApi({ 'GET /api/logs?limit=200': () => page([]) })
    render(<LogOverlay />)

    open()
    // Not before the server has said that there is nothing.
    expect(screen.queryByText('Henüz log yok.')).not.toBeInTheDocument()
    await advance()

    expect(within(screen.getByRole('log')).getByText('Henüz log yok.')).toBeInTheDocument()
    expect(rows()).toHaveLength(0)
    // The title and the way out are there all the same.
    expect(within(screen.getByRole('log')).getByRole('heading', { name: 'Loglar' })).toBeInTheDocument()
    expect(within(screen.getByRole('log')).getByRole('button', { name: 'Gizle' })).toBeInTheDocument()
  })

  it('removes "Henüz log yok." once the first entry arrives', async () => {
    mockApi({
      'GET /api/logs?limit=200': () => page([]),
      'GET /api/logs?after=0': () => page([entry(1)]),
    })
    render(<LogOverlay />)
    open()
    await advance()
    expect(screen.getByText('Henüz log yok.')).toBeInTheDocument()

    await advance(1000)

    expect(screen.queryByText('Henüz log yok.')).not.toBeInTheDocument()
    expect(rows()).toHaveLength(1)
  })

  it('never overlaps requests', async () => {
    const reply = deferred<MockReply>()
    let asked = 0
    const fetchMock = mockApi({
      'GET /api/logs?limit=200': () => page([entry(1)]),
      'GET /api/logs?after=1': () => {
        asked += 1
        return asked === 1 ? reply.promise : page([entry(2)])
      },
    })
    render(<LogOverlay />)
    open()
    await advance()

    // The server holds the second request back: no third while it is open.
    await advance(1000)
    await advance(3000)
    expect(logRequests(fetchMock)).toEqual(['/api/logs?limit=200', '/api/logs?after=1'])

    reply.resolve(page([], { last_seq: 1 }))
    await advance()
    await advance(1000)
    expect(logRequests(fetchMock)).toHaveLength(3)
    expect(rows()).toHaveLength(2)
  })

  it('goes on after a request failed and keeps what it shows', async () => {
    let asked = 0
    mockApi({
      'GET /api/logs?limit=200': () => page([entry(1)]),
      'GET /api/logs?after=1': () => {
        asked += 1
        return asked === 1 ? { status: 500, body: { error: 'boom' } } : page([entry(2)])
      },
    })
    render(<LogOverlay />)
    open()
    await advance()

    await advance(1000)
    expect(rows()).toHaveLength(1)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    await advance(1000)
    expect(rows()).toHaveLength(2)
  })

  it('starts again from the recent entries when the server has started over', async () => {
    // A restart: the numbers begin at 1 again, so "after 5" would never see anything new.
    let loads = 0
    const fetchMock = mockApi({
      'GET /api/logs?limit=200': () => {
        loads += 1
        return loads === 1 ? page([entry(4), entry(5)]) : page([entry(1, { message: 'Sunucu başladı' })])
      },
      'GET /api/logs?after=5': () => page([], { last_seq: 1 }),
      'GET /api/logs?after=1': () => page([], { last_seq: 1 }),
    })
    render(<LogOverlay />)
    open()
    await advance()
    expect(rows()).toHaveLength(2)

    await advance(1000)
    await advance(1000)

    expect(logRequests(fetchMock).slice(0, 3)).toEqual([
      '/api/logs?limit=200',
      '/api/logs?after=5',
      '/api/logs?limit=200',
    ])
    expect(rows().map((row) => row.querySelector('.log-message')?.textContent)).toEqual(['Sunucu başladı'])
  })

  it('asks only once when it opens under StrictMode, and shows the answer', async () => {
    // StrictMode runs the effect of a new panel twice (mount, unmount, mount); the dev server of
    // the app does that. The first request is not repeated, and its answer is not lost.
    const fetchMock = mockApi({
      'GET /api/logs?limit=200': () => page([entry(1), entry(2)]),
      'GET /api/logs?after=2': () => page([], { last_seq: 2 }),
    })
    render(
      <StrictMode>
        <LogOverlay />
      </StrictMode>,
    )

    open()
    await advance()

    expect(logRequests(fetchMock)).toEqual(['/api/logs?limit=200'])
    expect(rows()).toHaveLength(2)
    await advance(1000)
    expect(logRequests(fetchMock)).toEqual(['/api/logs?limit=200', '/api/logs?after=2'])
  })

  it('scrolls down for a new entry only while the user is at the bottom', async () => {
    mockApi({
      'GET /api/logs?limit=200': () => page([entry(1), entry(2)]),
      'GET /api/logs?after=2': () => page([entry(3)]),
      'GET /api/logs?after=3': () => page([entry(4)]),
    })
    render(<LogOverlay />)
    open()
    await advance()
    // jsdom has no layout, so the list is given a size: 1000 high of which 100 is seen.
    const list = within(screen.getByRole('log')).getByRole('list')
    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(list, 'clientHeight', { configurable: true, value: 100 })

    // At the bottom: the new entry is brought into view.
    list.scrollTop = 900
    fireEvent.scroll(list)
    await advance(1000)
    expect(rows()).toHaveLength(3)
    expect(list.scrollTop).toBe(1000)

    // Scrolled up to read: the list stays where the user left it.
    list.scrollTop = 200
    fireEvent.scroll(list)
    await advance(1000)
    expect(rows()).toHaveLength(4)
    expect(list.scrollTop).toBe(200)
  })

  it('does not move the list when an entry is opened', async () => {
    mockApi({
      'GET /api/logs?limit=200': () => page([entry(1, { details: 'ayrıntı' }), entry(2), entry(3)]),
    })
    render(<LogOverlay />)
    open()
    await advance()
    const list = within(screen.getByRole('log')).getByRole('list')
    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(list, 'clientHeight', { configurable: true, value: 100 })
    // The user looks at the first entries of a list that is longer than the panel is high.
    list.scrollTop = 0

    fireEvent.click(rows()[0])

    // The details open below the entry, where the user is looking; nothing scrolls to the end.
    expect(rows()[0]).toHaveAttribute('aria-expanded', 'true')
    expect(list.scrollTop).toBe(0)
  })

  it('renders the markup contract', async () => {
    mockApi({
      'GET /api/logs?limit=200': () => page([entry(1, { details: 'ayrıntı', source: 'ai', level: 'warn' }), entry(2)]),
    })
    const { container } = render(<LogOverlay />)

    expect(container.querySelectorAll('.log-toggle')).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Loglar' })).toHaveClass('log-toggle')

    open()
    await advance()
    fireEvent.click(rows()[0])

    // The panel is the log; it holds the title, the way out and the list.
    const panel = screen.getByRole('log')
    expect(panel).toHaveClass('log-panel')
    expect(panel).toHaveAccessibleName('Loglar')
    expect(panel.querySelectorAll('.log-list')).toHaveLength(1)
    const list = panel.querySelector('.log-list')
    expect(list?.tagName).toBe('UL')
    // Every entry is a line of the list; the open one is marked, and has its details below.
    expect(list?.querySelectorAll(':scope > li > .log-entry')).toHaveLength(2)
    const [open1, line2] = rows()
    expect(open1).toHaveClass('is-expanded')
    expect(line2).not.toHaveClass('is-expanded')
    for (const row of [open1, line2]) {
      expect(row.querySelectorAll(':scope > .log-time')).toHaveLength(1)
      expect(row.querySelectorAll(':scope > .log-source')).toHaveLength(1)
      expect(row.querySelectorAll(':scope > .log-message')).toHaveLength(1)
    }
    expect(panel.querySelectorAll('.log-details')).toHaveLength(1)
    expect(open1.parentElement?.querySelector(':scope > .log-details')).toHaveTextContent('ayrıntı')
  })
})

