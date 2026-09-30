import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { ActivityPanel } from './activity-panel'
import {
  ActiveRunsContext,
  type ActiveRun,
  type TokenTotals,
} from '@/hooks/active-runs-context'

function renderRun(tokenTotals: TokenTotals) {
  const run: ActiveRun = {
    taskId: 'CW-1',
    runId: 1,
    startedAt: new Date().toISOString(),
    tokenTotals,
    feed: [],
  }
  return renderToStaticMarkup(
    <ActiveRunsContext.Provider
      value={{ activeRuns: new Map([['CW-1', run]]), connected: true }}
    >
      <ActivityPanel taskId="CW-1" />
    </ActiveRunsContext.Provider>,
  )
}

describe('ActivityPanel run totals', () => {
  it('shows the summed prompt/completion totals', () => {
    const html = renderRun({ prompt: 11470, completion: 113, cache_read: 0, cache_write: 0, cost: 0.226 })
    expect(html).toContain('11,470')
    expect(html).toContain('113')
    expect(html).toContain('$0.23')
  })

  it('hides cost while the runtime reports none', () => {
    const html = renderRun({ prompt: 1200, completion: 340, cache_read: 0, cache_write: 0, cost: 0 })
    expect(html).toContain('1,200')
    expect(html).not.toContain('$')
  })
})
