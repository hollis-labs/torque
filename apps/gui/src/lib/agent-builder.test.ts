import { describe, expect, it } from 'vitest'
import { authority, effectiveServers, buildDefinition, draftFromArchetype, elevations, slugify, validate } from '@/lib/agent-builder'

describe('agent builder', () => {
  it('slugifies names into catalog-safe ids', () => {
    expect(slugify('  Release Captain! ')).toBe('release-captain')
    expect(slugify('Ünï Cöde')).toBe('uni-code')
  })

  it('flags a missing name and a taken slug as errors', () => {
    const d = draftFromArchetype('worker')
    expect(validate(d).some((i) => i.level === 'error' && i.step === 'identity')).toBe(true)
    const named = { ...d, name: 'General', slug: 'general' }
    expect(validate(named, ['general']).some((i) => i.text.includes('already taken'))).toBe(true)
  })

  it('treats bypass, network and high-risk scopes as needing approval and raises authority', () => {
    const base = { ...draftFromArchetype('reviewer'), name: 'R', slug: 'r', network: false }
    expect(elevations(base)).toEqual([])
    const wide = { ...base, permissionMode: 'bypass' as const, scopes: ['catalog.write'] }
    expect(elevations(wide).map((e) => e.field)).toEqual(['permission_mode', 'mcp_scopes'])
    expect(authority(wide)).toBeGreaterThan(authority(base))
  })

  it('lets a non-empty project list replace the torque-only default', () => {
    expect(effectiveServers('nanite')).toEqual(['torque'])
    expect(effectiveServers('design-lab')).not.toContain('torque')
  })

  it('compiles an agent and a launch that point at each other', () => {
    const def = buildDefinition({ ...draftFromArchetype('worker'), name: 'Fixer', slug: 'fixer' })
    expect(def.launch).toMatchObject({ agent: 'fixer', id: 'fixer-worktree', workspace: { mode: 'worktree' } })
    expect(def.agent).toMatchObject({ id: 'fixer', roles: ['worker'] })
  })
})
