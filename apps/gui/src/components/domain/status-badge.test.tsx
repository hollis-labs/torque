import { describe, it, expect } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { StatusBadge } from './status-badge'
import { TooltipProvider } from '@/components/ui/tooltip'

describe('StatusBadge', () => {
  it('renders without tooltip when none is provided', () => {
    const html = renderToStaticMarkup(<StatusBadge status="doing" />)
    expect(html).toContain('doing')
    // No portal/tooltip popup markup should appear when tooltip is unset
    expect(html).not.toContain('data-slot="tooltip"')
  })

  it('renders inside a tooltip wrapper when tooltip text is provided', () => {
    const html = renderToStaticMarkup(
      <TooltipProvider>
        <StatusBadge status="blocked" tooltip="Could not write file: permission denied" />
      </TooltipProvider>,
    )
    expect(html).toContain('blocked')
    // base-ui's tooltip-trigger leaves a data-slot attribute on the rendered
    // wrapper even before hover. The popup content portals on demand, so this
    // is a smoke check that the trigger wiring is in place — full hover
    // behavior requires a DOM environment.
    expect(html).toContain('data-slot="tooltip-trigger"')
  })

  it('omits the tooltip wrapper when the tooltip prop is empty', () => {
    const html = renderToStaticMarkup(<StatusBadge status="todo" tooltip="" />)
    expect(html).not.toContain('data-slot="tooltip-trigger"')
  })
})
