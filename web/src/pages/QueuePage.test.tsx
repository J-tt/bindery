import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import QueuePage from './QueuePage'
import { api } from '../api/client'

vi.mock('../api/client', async importOriginal => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      listQueue: vi.fn(),
      listPending: vi.fn(),
      deleteFromQueue: vi.fn(),
      dismissPending: vi.fn(),
      grabPending: vi.fn(),
    },
  }
})

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => {
      if (key === 'queue.remaining' && opts?.time) return `${opts.time} remaining`
      const m: Record<string, string> = {
        'queue.title': 'Queue',
        'queue.empty': 'Queue is empty',
        'queue.remove': 'Remove',
        'common.loading': 'Loading...',
      }
      return m[key] ?? key
    },
  }),
}))

describe('QueuePage — Bug 17b: timestamps and retry count', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.listPending).mockResolvedValue([])
  })

  it('shows addedAt as relative time (e.g. "3h ago") for queue items', async () => {
    const addedAt = new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString()
    vi.mocked(api.listQueue).mockResolvedValue([{
      id: 1,
      guid: 'abc',
      title: 'Test Book',
      status: 'downloading',
      size: 1048576,
      protocol: 'usenet',
      errorMessage: '',
      addedAt,
    }])

    render(<QueuePage />)
    await waitFor(() => expect(screen.getByText('Test Book')).toBeInTheDocument())
    expect(screen.getByText(/3h ago/)).toBeInTheDocument()
  })

  it('shows importRetryCount on failed items so operators know retries remaining', async () => {
    const addedAt = new Date(Date.now() - 60 * 1000).toISOString()
    vi.mocked(api.listQueue).mockResolvedValue([{
      id: 2,
      guid: 'def',
      title: 'Failed Book',
      status: 'importFailed',
      size: 2048000,
      protocol: 'usenet',
      errorMessage: 'path not found',
      addedAt,
      importRetryCount: 2,
    }])

    render(<QueuePage />)
    await waitFor(() => expect(screen.getByText('Failed Book')).toBeInTheDocument())
    expect(screen.getByText(/attempt 2 of 3/i)).toBeInTheDocument()
  })
})
