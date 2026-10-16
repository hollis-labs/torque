import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react'
import { SeamsContext } from '../events/context'
import { createActionBus, MockIdentityConfirm, type ActionBus, type IdentityConfirm } from '../seams'
import { registerAgentActions } from './actions'
import { useAgentsClient } from './context'

export interface AgentEditValue {
  bus: ActionBus
  identity: IdentityConfirm
}

const EditContext = createContext<AgentEditValue | null>(null)

/**
 * Resolves the bus and IdentityConfirm that agent-changing actions run through.
 *
 * Rule: an agent change never completes on a silent grant. Order of preference:
 *  1. explicit `bus` + `identity` props (tests, previews, a shell that owns both);
 *  2. the ConsoleProvider seams, using whatever IdentityConfirm is installed. If that
 *     is the stock MockIdentityConfirm in 'auto-grant' mode (ConsoleProvider's default
 *     when the shell passes no `identity`), it is NOT a step-up, so a local MANUAL
 *     mock replaces it and the dialog appears. A real IdentityConfirm, or a mock in
 *     'manual' / 'auto-deny', is used as is;
 *  3. no seams: a local bus with a manual mock.
 * `allowAutoGrant` opts out of the substitution (fixtures and demos only).
 * See BACKEND_NEEDS.md, "Installing the step-up in the shell".
 */
// eslint-disable-next-line react-refresh/only-export-components
export function resolveStepUp(
  seams: { actionBus: ActionBus; identity: IdentityConfirm } | null,
  props: { bus?: ActionBus; identity?: IdentityConfirm; allowAutoGrant?: boolean },
): AgentEditValue {
  const { bus, identity, allowAutoGrant } = props
  if (bus && identity) return { bus, identity }
  if (seams) {
    const installed = identity ?? seams.identity
    if (!allowAutoGrant && installed instanceof MockIdentityConfirm && installed.mode === 'auto-grant') {
      const manual = new MockIdentityConfirm({ mode: 'manual' })
      return { bus: createActionBus({ identity: manual }), identity: manual }
    }
    return { bus: bus ?? (identity ? createActionBus({ identity }) : seams.actionBus), identity: installed }
  }
  const id = identity ?? new MockIdentityConfirm({ mode: 'manual' })
  return { bus: bus ?? createActionBus({ identity: id }), identity: id }
}

export function AgentEditProvider({ bus, identity, allowAutoGrant, children }: { bus?: ActionBus; identity?: IdentityConfirm; allowAutoGrant?: boolean; children: ReactNode }) {
  const seams = useContext(SeamsContext)
  const client = useAgentsClient()
  const value = useMemo<AgentEditValue>(() => resolveStepUp(seams, { bus, identity, allowAutoGrant }), [bus, identity, allowAutoGrant, seams])
  useEffect(() => registerAgentActions(value.bus, client), [value.bus, client])
  return <EditContext.Provider value={value}>{children}</EditContext.Provider>
}

/** Fallback keeps the directory working when the provider is absent (tests, previews). */
// eslint-disable-next-line react-refresh/only-export-components
export function useAgentEdit(): AgentEditValue | null {
  return useContext(EditContext)
}
