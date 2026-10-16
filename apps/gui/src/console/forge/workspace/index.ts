// PR workspace (G3). Import from here.
//
//   <PrWorkspace prRef={{ repo, number }} />       embeddable component
//   prWorkspacePath(prRef, { file? })              app path (G4 inbox links)
//   <PrWorkspaceLink prRef={...} />                client-side link
//   PR_WORKSPACE_ROUTE + PrWorkspacePage           the single route App.tsx mounts
export { PrWorkspace, type PrWorkspaceProps } from './PrWorkspace'
export { PrWorkspaceLink } from './PrWorkspaceLink'
export { default as PrWorkspacePage } from './PrWorkspacePage'
export { PR_WORKSPACE_ROUTE, prWorkspacePath, prRefFromParams } from './route'
export { WORKSPACE_ACTIONS, commentAction, reviewAction, registerWorkspaceActions } from './actions'
export { useWorkspace, type Workspace, type WorkspaceData, type WriteResult, type PendingComment } from './useWorkspace'
export { buildThreadIndex, splitRows, unifiedRows, tokenize, langOf, buildFileTree, LARGE_FILE_LINES, ROW_CHUNK } from './diff-utils'
export type { Thread, ThreadIndex } from './diff-utils'
