// Package interactive is the Go port of these pi v1.0.0 TypeScript files
// (authoritative mapping: porting/filemap.tsv):
//
//   - packages/coding-agent/src/modes/interactive/bug-report.ts -> bug_report.go
//   - packages/coding-agent/src/modes/interactive/chat-viewport.ts -> chat_viewport.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L1-L432 -> interactive_mode_helpers.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L433-L1355 -> interactive_mode.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L433-L1355 -> interactive_mode_core.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L1356-L2987 -> interactive_mode_resources.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L1356-L2987 -> interactive_mode_session_binding.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L1356-L2987 -> interactive_mode_extension_ui.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L2988-L4765 -> interactive_mode_input.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L2988-L4765 -> interactive_mode_events.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L2988-L4765 -> interactive_mode_transcript.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L2988-L4765 -> interactive_mode_lifecycle.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L2988-L4765 -> interactive_mode_queues.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L4766-L5695 -> interactive_mode_selectors.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L5696-L7025 -> interactive_mode_auth.go
//   - packages/coding-agent/src/modes/interactive/interactive-mode.ts#L5696-L7025 -> interactive_mode_commands.go
//   - packages/coding-agent/src/modes/interactive/session-share.ts -> session_share.go
//   - packages/coding-agent/src/modes/interactive/tui-renderer.ts -> tui_renderer.go
package interactive
