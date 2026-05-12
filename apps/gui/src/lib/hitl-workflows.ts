import type {
  HITLEnforcementMode,
  HITLWorkflowDefinition,
  HITLWorkflowPreset,
  HITLWorkflowRequirements,
  JSONSchema,
} from './types'

export const HITL_WORKFLOW_PRESETS: HITLWorkflowPreset[] = ['pr_review', 'approval', 'message']

const OPEN_OBJECT_SCHEMA: JSONSchema = {
  type: 'object',
  additionalProperties: true,
}

function taskMetadata(
  enforcementMode: HITLEnforcementMode,
  requirements: HITLWorkflowRequirements,
) {
  return {
    key: 'hitl',
    workflow_type_key: 'workflow_type',
    requirements_key: 'requirements',
    enforcement_key: 'enforcement_mode',
    requirements,
    enforcement_mode: enforcementMode,
    behavior_reserved: true,
  }
}

const DEFINITIONS: Record<HITLWorkflowPreset, HITLWorkflowDefinition> = {
  pr_review: {
    type: 'pr_review',
    known: true,
    title: 'Pull request review',
    description: 'Checkpoint flow for reviewing a pull request and returning an explicit review decision.',
    payload_schema: {
      type: 'object',
      additionalProperties: true,
      required: ['pr_url'],
      properties: {
        pr_url: { type: 'string', description: 'Pull request URL.' },
        title: { type: 'string' },
        summary: { type: 'string' },
        branch: { type: 'string' },
        checklist: { type: 'array', items: { type: 'string' } },
      },
    },
    response_schema: {
      type: 'object',
      additionalProperties: true,
      required: ['decision'],
      properties: {
        decision: { type: 'string', enum: ['approve', 'request_changes', 'comment'] },
        summary: { type: 'string' },
        comments: { type: 'array', items: { type: 'string' } },
        required_changes: { type: 'array', items: { type: 'string' } },
      },
    },
    task_metadata: taskMetadata('advisory', {
      response_required: true,
      allowed_responder_source_types: ['user', 'agent', 'api'],
      min_responders: 1,
    }),
  },
  approval: {
    type: 'approval',
    known: true,
    title: 'Approval',
    description: 'Checkpoint flow for a yes/no style approval gate outside pull request review.',
    payload_schema: {
      type: 'object',
      additionalProperties: true,
      required: ['title', 'prompt'],
      properties: {
        title: { type: 'string' },
        prompt: { type: 'string' },
        context: { type: 'object', additionalProperties: true },
        options: { type: 'array', items: { type: 'string' } },
      },
    },
    response_schema: {
      type: 'object',
      additionalProperties: true,
      required: ['decision'],
      properties: {
        decision: { type: 'string', enum: ['approved', 'rejected', 'needs_info'] },
        comment: { type: 'string' },
      },
    },
    task_metadata: taskMetadata('advisory', {
      response_required: true,
      allowed_responder_source_types: ['user', 'agent', 'api'],
      min_responders: 1,
    }),
  },
  message: {
    type: 'message',
    known: true,
    title: 'Message',
    description: 'Checkpoint flow for sending a message that may optionally be acknowledged or replied to.',
    payload_schema: {
      type: 'object',
      additionalProperties: true,
      required: ['message'],
      properties: {
        subject: { type: 'string' },
        message: { type: 'string' },
        severity: { type: 'string', enum: ['info', 'warning', 'urgent'] },
        context: { type: 'object', additionalProperties: true },
      },
    },
    response_schema: {
      type: 'object',
      additionalProperties: true,
      required: ['acknowledged'],
      properties: {
        acknowledged: { type: 'boolean' },
        reply: { type: 'string' },
      },
    },
    task_metadata: taskMetadata('none', {
      response_required: false,
      allowed_responder_source_types: ['user', 'agent', 'api'],
    }),
  },
}

export function listHITLWorkflowDefinitions(): HITLWorkflowDefinition[] {
  return HITL_WORKFLOW_PRESETS.map((preset) => cloneDefinition(DEFINITIONS[preset]))
}

export function lookupHITLWorkflowDefinition(type: string): HITLWorkflowDefinition {
  const key = type.trim()
  if (isHITLWorkflowPreset(key)) {
    return cloneDefinition(DEFINITIONS[key])
  }
  return {
    type: key || 'unknown',
    known: false,
    title: 'Unknown workflow',
    description: 'Unregistered checkpoint workflow. Treat payload and response bodies as opaque JSON objects.',
    payload_schema: cloneSchema(OPEN_OBJECT_SCHEMA),
    response_schema: cloneSchema(OPEN_OBJECT_SCHEMA),
    task_metadata: taskMetadata('none', { response_required: false }),
  }
}

export function lookupHITLPayloadSchema(type: string): JSONSchema {
  return lookupHITLWorkflowDefinition(type).payload_schema
}

export function lookupHITLResponseSchema(type: string): JSONSchema {
  return lookupHITLWorkflowDefinition(type).response_schema
}

function isHITLWorkflowPreset(type: string): type is HITLWorkflowPreset {
  return HITL_WORKFLOW_PRESETS.includes(type as HITLWorkflowPreset)
}

function cloneDefinition(definition: HITLWorkflowDefinition): HITLWorkflowDefinition {
  return {
    ...definition,
    payload_schema: cloneSchema(definition.payload_schema),
    response_schema: cloneSchema(definition.response_schema),
    task_metadata: {
      ...definition.task_metadata,
      requirements: {
        ...definition.task_metadata.requirements,
        allowed_responder_source_types: [
          ...(definition.task_metadata.requirements.allowed_responder_source_types ?? []),
        ],
      },
    },
  }
}

function cloneSchema<T extends JSONSchema>(schema: T): T {
  return JSON.parse(JSON.stringify(schema)) as T
}
