import { describe, expect, it } from 'vitest'
import {
  HITL_WORKFLOW_PRESETS,
  listHITLWorkflowDefinitions,
  lookupHITLPayloadSchema,
  lookupHITLResponseSchema,
  lookupHITLWorkflowDefinition,
} from './hitl-workflows'

describe('HITL workflow registry', () => {
  it('lists the canonical presets in contract order', () => {
    expect(HITL_WORKFLOW_PRESETS).toEqual(['pr_review', 'approval', 'message'])
    expect(listHITLWorkflowDefinitions().map((def) => def.type)).toEqual(HITL_WORKFLOW_PRESETS)
  })

  it('looks up typed payload and response schemas for pr_review', () => {
    const payload = lookupHITLPayloadSchema('pr_review')
    const response = lookupHITLResponseSchema('pr_review')

    expect(payload.required).toContain('pr_url')
    expect(response.required).toContain('decision')
    expect(response.properties?.decision.enum).toContain('request_changes')
  })

  it('degrades unknown workflow types to open object schemas', () => {
    const def = lookupHITLWorkflowDefinition('vendor.custom')

    expect(def.known).toBe(false)
    expect(def.type).toBe('vendor.custom')
    expect(def.payload_schema).toMatchObject({ type: 'object', additionalProperties: true })
    expect(def.response_schema).toMatchObject({ type: 'object', additionalProperties: true })
    expect(def.task_metadata.enforcement_mode).toBe('none')
  })

  it('returns cloned schemas so callers cannot mutate the registry', () => {
    const first = lookupHITLWorkflowDefinition('approval')
    first.payload_schema.required?.push('mutated')

    const second = lookupHITLWorkflowDefinition('approval')
    expect(second.payload_schema.required).toEqual(['title', 'prompt'])
  })
})
