// Agent Console chat (C1). Import from here.
//
//   <ChatProvider client={createMockChatClient()}>
//     <ChatDock open onClose={...} agent={recipient} />
//
// Wrap in AutolinkProvider (R1) at the shell so message refs resolve to chips.
export { ChatProvider, useChat } from './context'
export { ChatPanel, type ChatPanelProps } from './ChatPanel'
export { ChatDock, ChatButton } from './ChatDock'
export { ChatComposer } from './ChatComposer'
export { ThreadView } from './ThreadView'
export { ToolChips } from './ToolChips'
export { CapabilityBadge } from './CapabilityBadge'
export { useChatThread, useChatThreads, useChatUnread } from './hooks'
export { applyChatEvent, mergeMessages, type ChatThreadState } from './reducer'
export { createMockChatClient, MOCK_CHAT_AGENTS, MOCK_REPLIES, type MockChatClient, type MockChatOptions } from './mock'
export { createMailChatClient, chatMessageFromThread, MAIL_CAPABILITY, type MailChatOptions } from './mail'
export * from './types'
