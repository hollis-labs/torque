// Agent Console autolink renderer (R1). Import from here.
//
//   <AutolinkProvider resolvers={...}>   once per page (R2: at the shell)
//     <Linkified text="see CW-20261016-0101 and @claude-code" />
//
// G2 adds forge kinds: `getRefRegistry().register(kind)` plus, per kind,
// `registerRefIcon`, `registerRefActions`, `registerRefDetail` and a resolver
// entry in the provider's `resolvers`.
export { AutolinkProvider, type AutolinkProviderProps } from './Provider'
export { Linkified, type LinkifiedProps } from './Linkified'
export { RefChip, type RefChipProps } from './RefChip'
export { PreviewCardView, StatusPill, type PreviewCardViewProps } from './PreviewCardView'
export { RefIcon } from './RefIcon'
export { TONE } from './tone'
export { RefDrawer } from './RefDrawer'
export { useAutolink, useRefState, type AutolinkContextValue, type ActivateEvent } from './context'
export {
  registerRefActions, registerRefDetail, registerRefIcon, refActions, refDetail, refIcon,
  type RefAction, type RefActionProvider, type RefDetailProps,
} from './extensions'
export { createLiveResolvers } from './live'
export { LinkText, ShellAutolink, type LinkTextProps } from './LinkText'
export { linkifiedMarkdownComponents } from './markdown'
export { setAutolinkSurface, isAutolinkSurfaceEnabled, type AutolinkSurface } from './surfaces'
