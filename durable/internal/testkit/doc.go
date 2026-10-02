// Package testkit is the Go port of these pi v1.0.0 TypeScript files
// (authoritative mapping: porting/filemap.tsv):
//
//   - packages/durable/test/chat-support.ts -> chat_support.go
//   - packages/durable/test/examples/00-conversation.ts -> examples_00_conversation.go
//   - packages/durable/test/examples/01-documents.ts -> examples_01_documents.go
//   - packages/durable/test/examples/02-forks.ts -> examples_02_forks.go
//   - packages/durable/test/examples/03-owned-conversations.ts -> examples_03_owned_conversations.go
//   - packages/durable/test/examples/04-chord-state.ts -> examples_04_chord_state.go
//   - packages/durable/test/examples/05-watches.ts -> examples_05_watches.go
//   - packages/durable/test/examples/06-harness.ts -> examples_06_harness.go
//   - packages/durable/test/examples/07-configuration.ts -> examples_07_configuration.go
//   - packages/durable/test/examples/08-harness-conversations.ts -> examples_08_harness_conversations.go
//   - packages/durable/test/examples/09-context.ts -> examples_09_context.go
//   - packages/durable/test/examples/10-registry-reload.ts -> examples_10_registry_reload.go
//   - packages/durable/test/examples/11-extension-state.ts -> examples_11_extension_state.go
//   - packages/durable/test/examples/12-tasks.ts -> examples_12_tasks.go
//   - packages/durable/test/examples/13-recovery.ts -> examples_13_recovery.go
//   - packages/durable/test/examples/14-chat.ts -> examples_14_chat.go
//   - packages/durable/test/examples/15-system-prompt.ts -> examples_15_system_prompt.go
//   - packages/durable/test/examples/17-coding-tools.ts -> examples_17_coding_tools.go
//   - packages/durable/test/examples/18-print.ts -> examples_18_print.go
//   - packages/durable/test/examples/19-json.ts -> examples_19_json.go
//   - packages/durable/test/examples/20-inbox.ts -> examples_20_inbox.go
//   - packages/durable/test/examples/21-late-join.ts -> examples_21_late_join.go
//   - packages/durable/test/examples/22-subagent-foreground.ts -> examples_22_subagent_foreground.go
//   - packages/durable/test/examples/23-subagent-background.ts -> examples_23_subagent_background.go
//   - packages/durable/test/examples/24-child-tasks.ts -> examples_24_child_tasks.go
//   - packages/durable/test/examples/25-compaction.ts -> examples_25_compaction.go
//   - packages/durable/test/examples/26-coding-agent.ts -> examples_26_coding_agent.go
//   - packages/durable/test/examples/27-plan-mode.ts -> examples_27_plan_mode.go
//   - packages/durable/test/examples/28-reviewer.ts -> examples_28_reviewer.go
//   - packages/durable/test/examples/29-sandbox-per-conversation.ts -> examples_29_sandbox_per_conversation.go
//   - packages/durable/test/examples/30-tool-override.ts -> examples_30_tool_override.go
//   - packages/durable/test/examples/31-reload-and-restart.ts -> examples_31_reload_and_restart.go
//   - packages/durable/test/harness-support.ts -> harness_support.go
//   - packages/durable/test/session-support.ts -> session_support.go
//   - packages/durable/test/task-support.ts -> task_support.go
package testkit
