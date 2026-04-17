import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { BlockedReasonAlert } from './blocked-reason-alert'

describe('BlockedReasonAlert', () => {
  it('renders blocked variant with the reason text', () => {
    const html = renderToStaticMarkup(
      <BlockedReasonAlert
        status="blocked"
        reason="Permission denied: editing /etc/hosts"
      />,
    )
    expect(html).toMatchSnapshot()
    expect(html).toContain('Blocked')
    expect(html).toContain('Permission denied: editing /etc/hosts')
    expect(html).toContain('role="alert"')
    expect(html).toContain('border-red-500/40')
  })

  it('renders paused variant with yellow accent', () => {
    const html = renderToStaticMarkup(
      <BlockedReasonAlert
        status="paused"
        reason="Awaiting human input on schema decision"
      />,
    )
    expect(html).toMatchSnapshot()
    expect(html).toContain('Paused')
    expect(html).toContain('Awaiting human input on schema decision')
    expect(html).toContain('border-yellow-500/40')
  })

  it('preserves multi-line reasons via whitespace-pre-wrap', () => {
    const reason = 'line one\nline two\nline three'
    const html = renderToStaticMarkup(
      <BlockedReasonAlert status="blocked" reason={reason} />,
    )
    expect(html).toContain('whitespace-pre-wrap')
    expect(html).toContain('line one\nline two\nline three')
  })

  it('exposes a stable test id for smoke wiring', () => {
    const html = renderToStaticMarkup(
      <BlockedReasonAlert status="blocked" reason="x" />,
    )
    expect(html).toContain('data-testid="blocked-reason-alert"')
  })
})
