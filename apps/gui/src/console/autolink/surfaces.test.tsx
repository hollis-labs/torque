/** @vitest-environment jsdom */
import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render } from '@testing-library/react'
import Markdown from 'react-markdown'
import { MemoryRouter } from 'react-router-dom'
import { createDefaultRefRegistry } from '../refs'
import { AutolinkProvider, LinkText, linkifiedMarkdownComponents, setAutolinkSurface } from '.'

afterEach(() => {
  cleanup()
  setAutolinkSurface('messaging', true)
})

const wrap = (ui: React.ReactNode) =>
  render(
    <MemoryRouter>
      <AutolinkProvider registry={createDefaultRefRegistry()}>{ui}</AutolinkProvider>
    </MemoryRouter>,
  )

describe('LinkText', () => {
  it('links references and leaves plain text as a bare text node', () => {
    const { container } = wrap(<p data-testid="p"><LinkText surface="messaging" text="plain words" /></p>)
    expect(container.querySelector('p')!.innerHTML).toBe('plain words')
    cleanup()
    const r = wrap(<LinkText surface="messaging" text="see CW-20261016-0101" />)
    expect(r.container.querySelectorAll('[data-ref-kind]')).toHaveLength(1)
  })

  it('renders plain text when the surface is opted out', () => {
    setAutolinkSurface('messaging', false)
    const { container } = wrap(<LinkText surface="messaging" text="see CW-20261016-0101" />)
    expect(container.querySelectorAll('[data-ref-kind]')).toHaveLength(0)
    expect(container.textContent).toBe('see CW-20261016-0101')
  })
})

describe('linkifiedMarkdownComponents', () => {
  it('links prose but not code', () => {
    const { container } = wrap(
      <Markdown components={linkifiedMarkdownComponents('forums')}>{'Work on CW-20261016-0101 and `EP-20261016-0002`.\n\n```\nCW-20261016-0102\n```'}</Markdown>,
    )
    expect(container.querySelectorAll('[data-ref-kind]')).toHaveLength(1)
    expect(container.querySelector('code')!.textContent).toContain('EP-20261016-0002')
  })
})
