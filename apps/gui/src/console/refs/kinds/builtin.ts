import type { RefRegistry } from '../registry'
import { createAgentKind } from './agent'
import { createPathKind } from './path'
import { createTesseractKinds } from './tesseract'
import { createTorqueKinds } from './torque'
import { createUrlKind } from './url'
import { createWorkstreamKind } from './workstream'

/**
 * Built-in kinds in priority order (higher wins an overlap):
 * url 100 > tesseract.key 90 > torque.* 80 > tesseract.item 70 > workstream 60 > agent 50 > path 40.
 *
 * Forge kinds (G2) register themselves with the registry from their own
 * module; they are intentionally not listed here.
 */
export function registerBuiltinKinds(registry: RefRegistry): void {
  const kinds = [
    createUrlKind(),
    ...createTesseractKinds(),
    ...createTorqueKinds(),
    createWorkstreamKind(),
    createAgentKind(),
    createPathKind(),
  ]
  for (const k of kinds) registry.register(k)
}
