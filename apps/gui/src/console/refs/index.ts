// Agent Console reference registry (F2). Import from here.
export type {
  KindResolver, OpenTarget, PreviewCard, RefCardPhase, RefEntity, RefHit, RefIdentity, RefKind, RefKindId, RefMeta,
  RefSegment, RefState, ResolveContext, ResolveOutcome, Segment, StatusTone,
} from './types'
export { createRefRegistry, type RefRegistry } from './registry'
export { codeRanges, detect, detectRefs, refKey, uniqueRefs, type DetectOptions } from './detect'
export { createRefResolverClient, type RefResolverClient, type ResolverClientOptions } from './resolver'
export { buildPreviewCard } from './preview'
export { registerBuiltinKinds } from './kinds/builtin'
export { isWordChar, matches, trimTrailing } from './scan-utils'
export { TORQUE_ENTITIES } from './kinds/torque'
export { parseTesseractKey, tesseractKeyCanonical, TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND } from './kinds/tesseract'
export { createMockResolver, createMockResolvers, createTorqueTaskResolver, MOCK_ENTITIES, type MockResolverOptions } from './mock'

import { createRefRegistry, type RefRegistry } from './registry'
import { registerBuiltinKinds } from './kinds/builtin'

let shared: RefRegistry | null = null

/** The app-wide registry with built-ins. Feature modules (G2) call `.register(...)` on it at startup. */
export function getRefRegistry(): RefRegistry {
  if (!shared) {
    shared = createRefRegistry()
    registerBuiltinKinds(shared)
  }
  return shared
}

/** A fresh registry with built-ins, for tests and isolated renderers. */
export function createDefaultRefRegistry(): RefRegistry {
  const r = createRefRegistry()
  registerBuiltinKinds(r)
  return r
}
