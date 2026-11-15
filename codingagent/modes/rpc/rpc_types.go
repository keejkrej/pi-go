// Ported from packages/coding-agent/src/modes/rpc/rpc-types.ts (pi v1.5.2).

package rpc

import (
	"fmt"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// RpcCommandType identifies an RPC stdin command variant.
type RpcCommandType string

const (
	RpcCmdPrompt                     RpcCommandType = "prompt"
	RpcCmdSteer                      RpcCommandType = "steer"
	RpcCmdFollowUp                   RpcCommandType = "follow_up"
	RpcCmdAbort                      RpcCommandType = "abort"
	RpcCmdClearQueue                 RpcCommandType = "clear_queue"
	RpcCmdNewSession                 RpcCommandType = "new_session"
	RpcCmdGetState                   RpcCommandType = "get_state"
	RpcCmdSetModel                   RpcCommandType = "set_model"
	RpcCmdCycleModel                 RpcCommandType = "cycle_model"
	RpcCmdGetAvailableModels         RpcCommandType = "get_available_models"
	RpcCmdSetThinkingLevel           RpcCommandType = "set_thinking_level"
	RpcCmdCycleThinkingLevel         RpcCommandType = "cycle_thinking_level"
	RpcCmdGetAvailableThinkingLevels RpcCommandType = "get_available_thinking_levels"
	RpcCmdSetSteeringMode            RpcCommandType = "set_steering_mode"
	RpcCmdSetFollowUpMode            RpcCommandType = "set_follow_up_mode"
	RpcCmdCompact                    RpcCommandType = "compact"
	RpcCmdSetAutoCompaction          RpcCommandType = "set_auto_compaction"
	RpcCmdSetAutoRetry               RpcCommandType = "set_auto_retry"
	RpcCmdAbortRetry                 RpcCommandType = "abort_retry"
	RpcCmdBash                       RpcCommandType = "bash"
	RpcCmdAbortBash                  RpcCommandType = "abort_bash"
	RpcCmdGetSessionStats            RpcCommandType = "get_session_stats"
	RpcCmdExportHtml                 RpcCommandType = "export_html"
	RpcCmdSwitchSession              RpcCommandType = "switch_session"
	RpcCmdFork                       RpcCommandType = "fork"
	RpcCmdClone                      RpcCommandType = "clone"
	RpcCmdGetForkMessages            RpcCommandType = "get_fork_messages"
	RpcCmdGetEntries                 RpcCommandType = "get_entries"
	RpcCmdGetTree                    RpcCommandType = "get_tree"
	RpcCmdGetLastAssistantText       RpcCommandType = "get_last_assistant_text"
	RpcCmdSetSessionName             RpcCommandType = "set_session_name"
	RpcCmdGetMessages                RpcCommandType = "get_messages"
	RpcCmdGetCommands                RpcCommandType = "get_commands"
)

// RpcCommand is a command received on stdin in RPC mode.
type RpcCommand interface {
	isRpcCommand()
	CommandType() RpcCommandType
	CommandId() *string
	SetCommandId(id *string)
}

type rtCommandBase struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*rtCommandBase) isRpcCommand()             {}
func (b *rtCommandBase) CommandId() *string      { return b.Id }
func (b *rtCommandBase) SetCommandId(id *string) { b.Id = id }

// RpcPromptCommand sends a prompt message to the session.
type RpcPromptCommand struct {
	Id                *string            `json:"id,omitzero"`
	Type              RpcCommandType     `json:"type"`
	Message           string             `json:"message"`
	Images            []*ai.ImageContent `json:"images,omitzero"`
	StreamingBehavior *string            `json:"streamingBehavior,omitzero"`
}

func (*RpcPromptCommand) isRpcCommand()               {}
func (*RpcPromptCommand) CommandType() RpcCommandType { return RpcCmdPrompt }
func (c *RpcPromptCommand) CommandId() *string        { return c.Id }
func (c *RpcPromptCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcPromptCommand) MarshalJSON() ([]byte, error) {
	type alias RpcPromptCommand
	if c.Type == "" {
		c.Type = RpcCmdPrompt
	}
	return jsonx.Marshal(alias(c))
}

// RpcSteerCommand queues a steering message mid-run.
type RpcSteerCommand struct {
	Id      *string            `json:"id,omitzero"`
	Type    RpcCommandType     `json:"type"`
	Message string             `json:"message"`
	Images  []*ai.ImageContent `json:"images,omitzero"`
}

func (*RpcSteerCommand) isRpcCommand()               {}
func (*RpcSteerCommand) CommandType() RpcCommandType { return RpcCmdSteer }
func (c *RpcSteerCommand) CommandId() *string        { return c.Id }
func (c *RpcSteerCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSteerCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSteerCommand
	if c.Type == "" {
		c.Type = RpcCmdSteer
	}
	return jsonx.Marshal(alias(c))
}

// RpcFollowUpCommand queues a follow-up message after the run completes.
type RpcFollowUpCommand struct {
	Id      *string            `json:"id,omitzero"`
	Type    RpcCommandType     `json:"type"`
	Message string             `json:"message"`
	Images  []*ai.ImageContent `json:"images,omitzero"`
}

func (*RpcFollowUpCommand) isRpcCommand()               {}
func (*RpcFollowUpCommand) CommandType() RpcCommandType { return RpcCmdFollowUp }
func (c *RpcFollowUpCommand) CommandId() *string        { return c.Id }
func (c *RpcFollowUpCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcFollowUpCommand) MarshalJSON() ([]byte, error) {
	type alias RpcFollowUpCommand
	if c.Type == "" {
		c.Type = RpcCmdFollowUp
	}
	return jsonx.Marshal(alias(c))
}

// RpcAbortCommand aborts the active agent run.
type RpcAbortCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcAbortCommand) isRpcCommand()               {}
func (*RpcAbortCommand) CommandType() RpcCommandType { return RpcCmdAbort }
func (c *RpcAbortCommand) CommandId() *string        { return c.Id }
func (c *RpcAbortCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcAbortCommand) MarshalJSON() ([]byte, error) {
	type alias RpcAbortCommand
	if c.Type == "" {
		c.Type = RpcCmdAbort
	}
	return jsonx.Marshal(alias(c))
}

// RpcClearQueueCommand clears queued steering and follow-up messages.
type RpcClearQueueCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcClearQueueCommand) isRpcCommand()               {}
func (*RpcClearQueueCommand) CommandType() RpcCommandType { return RpcCmdClearQueue }
func (c *RpcClearQueueCommand) CommandId() *string        { return c.Id }
func (c *RpcClearQueueCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcClearQueueCommand) MarshalJSON() ([]byte, error) {
	type alias RpcClearQueueCommand
	if c.Type == "" {
		c.Type = RpcCmdClearQueue
	}
	return jsonx.Marshal(alias(c))
}

// RpcNewSessionCommand starts a fresh session.
type RpcNewSessionCommand struct {
	Id            *string        `json:"id,omitzero"`
	Type          RpcCommandType `json:"type"`
	ParentSession *string        `json:"parentSession,omitzero"`
}

func (*RpcNewSessionCommand) isRpcCommand()               {}
func (*RpcNewSessionCommand) CommandType() RpcCommandType { return RpcCmdNewSession }
func (c *RpcNewSessionCommand) CommandId() *string        { return c.Id }
func (c *RpcNewSessionCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcNewSessionCommand) MarshalJSON() ([]byte, error) {
	type alias RpcNewSessionCommand
	if c.Type == "" {
		c.Type = RpcCmdNewSession
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetStateCommand requests current session state.
type RpcGetStateCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetStateCommand) isRpcCommand()               {}
func (*RpcGetStateCommand) CommandType() RpcCommandType { return RpcCmdGetState }
func (c *RpcGetStateCommand) CommandId() *string        { return c.Id }
func (c *RpcGetStateCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcGetStateCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetStateCommand
	if c.Type == "" {
		c.Type = RpcCmdGetState
	}
	return jsonx.Marshal(alias(c))
}

// RpcSetModelCommand selects a model by provider and id.
type RpcSetModelCommand struct {
	Id       *string        `json:"id,omitzero"`
	Type     RpcCommandType `json:"type"`
	Provider string         `json:"provider"`
	ModelId  string         `json:"modelId"`
}

func (*RpcSetModelCommand) isRpcCommand()               {}
func (*RpcSetModelCommand) CommandType() RpcCommandType { return RpcCmdSetModel }
func (c *RpcSetModelCommand) CommandId() *string        { return c.Id }
func (c *RpcSetModelCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSetModelCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSetModelCommand
	if c.Type == "" {
		c.Type = RpcCmdSetModel
	}
	return jsonx.Marshal(alias(c))
}

// RpcCycleModelCommand cycles to the next available model.
type RpcCycleModelCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcCycleModelCommand) isRpcCommand()               {}
func (*RpcCycleModelCommand) CommandType() RpcCommandType { return RpcCmdCycleModel }
func (c *RpcCycleModelCommand) CommandId() *string        { return c.Id }
func (c *RpcCycleModelCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcCycleModelCommand) MarshalJSON() ([]byte, error) {
	type alias RpcCycleModelCommand
	if c.Type == "" {
		c.Type = RpcCmdCycleModel
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetAvailableModelsCommand queries available models.
type RpcGetAvailableModelsCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetAvailableModelsCommand) isRpcCommand()               {}
func (*RpcGetAvailableModelsCommand) CommandType() RpcCommandType { return RpcCmdGetAvailableModels }
func (c *RpcGetAvailableModelsCommand) CommandId() *string        { return c.Id }
func (c *RpcGetAvailableModelsCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcGetAvailableModelsCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetAvailableModelsCommand
	if c.Type == "" {
		c.Type = RpcCmdGetAvailableModels
	}
	return jsonx.Marshal(alias(c))
}

// RpcSetThinkingLevelCommand sets the agent thinking level.
type RpcSetThinkingLevelCommand struct {
	Id    *string             `json:"id,omitzero"`
	Type  RpcCommandType      `json:"type"`
	Level agent.ThinkingLevel `json:"level"`
}

func (*RpcSetThinkingLevelCommand) isRpcCommand()               {}
func (*RpcSetThinkingLevelCommand) CommandType() RpcCommandType { return RpcCmdSetThinkingLevel }
func (c *RpcSetThinkingLevelCommand) CommandId() *string        { return c.Id }
func (c *RpcSetThinkingLevelCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSetThinkingLevelCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSetThinkingLevelCommand
	if c.Type == "" {
		c.Type = RpcCmdSetThinkingLevel
	}
	return jsonx.Marshal(alias(c))
}

// RpcCycleThinkingLevelCommand cycles thinking level.
type RpcCycleThinkingLevelCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcCycleThinkingLevelCommand) isRpcCommand()               {}
func (*RpcCycleThinkingLevelCommand) CommandType() RpcCommandType { return RpcCmdCycleThinkingLevel }
func (c *RpcCycleThinkingLevelCommand) CommandId() *string        { return c.Id }
func (c *RpcCycleThinkingLevelCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcCycleThinkingLevelCommand) MarshalJSON() ([]byte, error) {
	type alias RpcCycleThinkingLevelCommand
	if c.Type == "" {
		c.Type = RpcCmdCycleThinkingLevel
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetAvailableThinkingLevelsCommand lists supported thinking levels.
type RpcGetAvailableThinkingLevelsCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetAvailableThinkingLevelsCommand) isRpcCommand() {}
func (*RpcGetAvailableThinkingLevelsCommand) CommandType() RpcCommandType {
	return RpcCmdGetAvailableThinkingLevels
}
func (c *RpcGetAvailableThinkingLevelsCommand) CommandId() *string      { return c.Id }
func (c *RpcGetAvailableThinkingLevelsCommand) SetCommandId(id *string) { c.Id = id }
func (c RpcGetAvailableThinkingLevelsCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetAvailableThinkingLevelsCommand
	if c.Type == "" {
		c.Type = RpcCmdGetAvailableThinkingLevels
	}
	return jsonx.Marshal(alias(c))
}

// RpcSetSteeringModeCommand sets the queue mode for steering messages.
type RpcSetSteeringModeCommand struct {
	Id   *string         `json:"id,omitzero"`
	Type RpcCommandType  `json:"type"`
	Mode agent.QueueMode `json:"mode"`
}

func (*RpcSetSteeringModeCommand) isRpcCommand()               {}
func (*RpcSetSteeringModeCommand) CommandType() RpcCommandType { return RpcCmdSetSteeringMode }
func (c *RpcSetSteeringModeCommand) CommandId() *string        { return c.Id }
func (c *RpcSetSteeringModeCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSetSteeringModeCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSetSteeringModeCommand
	if c.Type == "" {
		c.Type = RpcCmdSetSteeringMode
	}
	return jsonx.Marshal(alias(c))
}

// RpcSetFollowUpModeCommand sets the queue mode for follow-up messages.
type RpcSetFollowUpModeCommand struct {
	Id   *string         `json:"id,omitzero"`
	Type RpcCommandType  `json:"type"`
	Mode agent.QueueMode `json:"mode"`
}

func (*RpcSetFollowUpModeCommand) isRpcCommand()               {}
func (*RpcSetFollowUpModeCommand) CommandType() RpcCommandType { return RpcCmdSetFollowUpMode }
func (c *RpcSetFollowUpModeCommand) CommandId() *string        { return c.Id }
func (c *RpcSetFollowUpModeCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSetFollowUpModeCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSetFollowUpModeCommand
	if c.Type == "" {
		c.Type = RpcCmdSetFollowUpMode
	}
	return jsonx.Marshal(alias(c))
}

// RpcCompactCommand triggers manual compaction.
type RpcCompactCommand struct {
	Id                 *string        `json:"id,omitzero"`
	Type               RpcCommandType `json:"type"`
	CustomInstructions *string        `json:"customInstructions,omitzero"`
}

func (*RpcCompactCommand) isRpcCommand()               {}
func (*RpcCompactCommand) CommandType() RpcCommandType { return RpcCmdCompact }
func (c *RpcCompactCommand) CommandId() *string        { return c.Id }
func (c *RpcCompactCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcCompactCommand) MarshalJSON() ([]byte, error) {
	type alias RpcCompactCommand
	if c.Type == "" {
		c.Type = RpcCmdCompact
	}
	return jsonx.Marshal(alias(c))
}

// RpcSetAutoCompactionCommand toggles automatic compaction.
type RpcSetAutoCompactionCommand struct {
	Id      *string        `json:"id,omitzero"`
	Type    RpcCommandType `json:"type"`
	Enabled bool           `json:"enabled"`
}

func (*RpcSetAutoCompactionCommand) isRpcCommand()               {}
func (*RpcSetAutoCompactionCommand) CommandType() RpcCommandType { return RpcCmdSetAutoCompaction }
func (c *RpcSetAutoCompactionCommand) CommandId() *string        { return c.Id }
func (c *RpcSetAutoCompactionCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSetAutoCompactionCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSetAutoCompactionCommand
	if c.Type == "" {
		c.Type = RpcCmdSetAutoCompaction
	}
	return jsonx.Marshal(alias(c))
}

// RpcSetAutoRetryCommand toggles automatic retry.
type RpcSetAutoRetryCommand struct {
	Id      *string        `json:"id,omitzero"`
	Type    RpcCommandType `json:"type"`
	Enabled bool           `json:"enabled"`
}

func (*RpcSetAutoRetryCommand) isRpcCommand()               {}
func (*RpcSetAutoRetryCommand) CommandType() RpcCommandType { return RpcCmdSetAutoRetry }
func (c *RpcSetAutoRetryCommand) CommandId() *string        { return c.Id }
func (c *RpcSetAutoRetryCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSetAutoRetryCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSetAutoRetryCommand
	if c.Type == "" {
		c.Type = RpcCmdSetAutoRetry
	}
	return jsonx.Marshal(alias(c))
}

// RpcAbortRetryCommand aborts an active retry backoff.
type RpcAbortRetryCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcAbortRetryCommand) isRpcCommand()               {}
func (*RpcAbortRetryCommand) CommandType() RpcCommandType { return RpcCmdAbortRetry }
func (c *RpcAbortRetryCommand) CommandId() *string        { return c.Id }
func (c *RpcAbortRetryCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcAbortRetryCommand) MarshalJSON() ([]byte, error) {
	type alias RpcAbortRetryCommand
	if c.Type == "" {
		c.Type = RpcCmdAbortRetry
	}
	return jsonx.Marshal(alias(c))
}

// RpcBashCommand executes a user bash command.
type RpcBashCommand struct {
	Id                 *string        `json:"id,omitzero"`
	Type               RpcCommandType `json:"type"`
	Command            string         `json:"command"`
	ExcludeFromContext *bool          `json:"excludeFromContext,omitzero"`
}

func (*RpcBashCommand) isRpcCommand()               {}
func (*RpcBashCommand) CommandType() RpcCommandType { return RpcCmdBash }
func (c *RpcBashCommand) CommandId() *string        { return c.Id }
func (c *RpcBashCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcBashCommand) MarshalJSON() ([]byte, error) {
	type alias RpcBashCommand
	if c.Type == "" {
		c.Type = RpcCmdBash
	}
	return jsonx.Marshal(alias(c))
}

// RpcAbortBashCommand aborts an in-flight user bash execution.
type RpcAbortBashCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcAbortBashCommand) isRpcCommand()               {}
func (*RpcAbortBashCommand) CommandType() RpcCommandType { return RpcCmdAbortBash }
func (c *RpcAbortBashCommand) CommandId() *string        { return c.Id }
func (c *RpcAbortBashCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcAbortBashCommand) MarshalJSON() ([]byte, error) {
	type alias RpcAbortBashCommand
	if c.Type == "" {
		c.Type = RpcCmdAbortBash
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetSessionStatsCommand requests session statistics.
type RpcGetSessionStatsCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetSessionStatsCommand) isRpcCommand()               {}
func (*RpcGetSessionStatsCommand) CommandType() RpcCommandType { return RpcCmdGetSessionStats }
func (c *RpcGetSessionStatsCommand) CommandId() *string        { return c.Id }
func (c *RpcGetSessionStatsCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcGetSessionStatsCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetSessionStatsCommand
	if c.Type == "" {
		c.Type = RpcCmdGetSessionStats
	}
	return jsonx.Marshal(alias(c))
}

// RpcExportHtmlCommand exports the session to an HTML file.
type RpcExportHtmlCommand struct {
	Id         *string        `json:"id,omitzero"`
	Type       RpcCommandType `json:"type"`
	OutputPath *string        `json:"outputPath,omitzero"`
}

func (*RpcExportHtmlCommand) isRpcCommand()               {}
func (*RpcExportHtmlCommand) CommandType() RpcCommandType { return RpcCmdExportHtml }
func (c *RpcExportHtmlCommand) CommandId() *string        { return c.Id }
func (c *RpcExportHtmlCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcExportHtmlCommand) MarshalJSON() ([]byte, error) {
	type alias RpcExportHtmlCommand
	if c.Type == "" {
		c.Type = RpcCmdExportHtml
	}
	return jsonx.Marshal(alias(c))
}

// RpcSwitchSessionCommand switches to another session JSONL file.
type RpcSwitchSessionCommand struct {
	Id          *string        `json:"id,omitzero"`
	Type        RpcCommandType `json:"type"`
	SessionPath string         `json:"sessionPath"`
}

func (*RpcSwitchSessionCommand) isRpcCommand()               {}
func (*RpcSwitchSessionCommand) CommandType() RpcCommandType { return RpcCmdSwitchSession }
func (c *RpcSwitchSessionCommand) CommandId() *string        { return c.Id }
func (c *RpcSwitchSessionCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSwitchSessionCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSwitchSessionCommand
	if c.Type == "" {
		c.Type = RpcCmdSwitchSession
	}
	return jsonx.Marshal(alias(c))
}

// RpcForkCommand forks the session from entryId.
type RpcForkCommand struct {
	Id      *string        `json:"id,omitzero"`
	Type    RpcCommandType `json:"type"`
	EntryId string         `json:"entryId"`
}

func (*RpcForkCommand) isRpcCommand()               {}
func (*RpcForkCommand) CommandType() RpcCommandType { return RpcCmdFork }
func (c *RpcForkCommand) CommandId() *string        { return c.Id }
func (c *RpcForkCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcForkCommand) MarshalJSON() ([]byte, error) {
	type alias RpcForkCommand
	if c.Type == "" {
		c.Type = RpcCmdFork
	}
	return jsonx.Marshal(alias(c))
}

// RpcCloneCommand clones the current active branch into a new session.
type RpcCloneCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcCloneCommand) isRpcCommand()               {}
func (*RpcCloneCommand) CommandType() RpcCommandType { return RpcCmdClone }
func (c *RpcCloneCommand) CommandId() *string        { return c.Id }
func (c *RpcCloneCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcCloneCommand) MarshalJSON() ([]byte, error) {
	type alias RpcCloneCommand
	if c.Type == "" {
		c.Type = RpcCmdClone
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetForkMessagesCommand lists user messages available for forking.
type RpcGetForkMessagesCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetForkMessagesCommand) isRpcCommand()               {}
func (*RpcGetForkMessagesCommand) CommandType() RpcCommandType { return RpcCmdGetForkMessages }
func (c *RpcGetForkMessagesCommand) CommandId() *string        { return c.Id }
func (c *RpcGetForkMessagesCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcGetForkMessagesCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetForkMessagesCommand
	if c.Type == "" {
		c.Type = RpcCmdGetForkMessages
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetEntriesCommand returns session entries, optionally after a since entry id.
type RpcGetEntriesCommand struct {
	Id    *string        `json:"id,omitzero"`
	Type  RpcCommandType `json:"type"`
	Since *string        `json:"since,omitzero"`
}

func (*RpcGetEntriesCommand) isRpcCommand()               {}
func (*RpcGetEntriesCommand) CommandType() RpcCommandType { return RpcCmdGetEntries }
func (c *RpcGetEntriesCommand) CommandId() *string        { return c.Id }
func (c *RpcGetEntriesCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcGetEntriesCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetEntriesCommand
	if c.Type == "" {
		c.Type = RpcCmdGetEntries
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetTreeCommand requests the session entry tree.
type RpcGetTreeCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetTreeCommand) isRpcCommand()               {}
func (*RpcGetTreeCommand) CommandType() RpcCommandType { return RpcCmdGetTree }
func (c *RpcGetTreeCommand) CommandId() *string        { return c.Id }
func (c *RpcGetTreeCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcGetTreeCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetTreeCommand
	if c.Type == "" {
		c.Type = RpcCmdGetTree
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetLastAssistantTextCommand requests the text of the last assistant message.
type RpcGetLastAssistantTextCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetLastAssistantTextCommand) isRpcCommand() {}
func (*RpcGetLastAssistantTextCommand) CommandType() RpcCommandType {
	return RpcCmdGetLastAssistantText
}
func (c *RpcGetLastAssistantTextCommand) CommandId() *string      { return c.Id }
func (c *RpcGetLastAssistantTextCommand) SetCommandId(id *string) { c.Id = id }
func (c RpcGetLastAssistantTextCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetLastAssistantTextCommand
	if c.Type == "" {
		c.Type = RpcCmdGetLastAssistantText
	}
	return jsonx.Marshal(alias(c))
}

// RpcSetSessionNameCommand sets the session display name.
type RpcSetSessionNameCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
	Name string         `json:"name"`
}

func (*RpcSetSessionNameCommand) isRpcCommand()               {}
func (*RpcSetSessionNameCommand) CommandType() RpcCommandType { return RpcCmdSetSessionName }
func (c *RpcSetSessionNameCommand) CommandId() *string        { return c.Id }
func (c *RpcSetSessionNameCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcSetSessionNameCommand) MarshalJSON() ([]byte, error) {
	type alias RpcSetSessionNameCommand
	if c.Type == "" {
		c.Type = RpcCmdSetSessionName
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetMessagesCommand returns all messages in the active session state.
type RpcGetMessagesCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetMessagesCommand) isRpcCommand()               {}
func (*RpcGetMessagesCommand) CommandType() RpcCommandType { return RpcCmdGetMessages }
func (c *RpcGetMessagesCommand) CommandId() *string        { return c.Id }
func (c *RpcGetMessagesCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcGetMessagesCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetMessagesCommand
	if c.Type == "" {
		c.Type = RpcCmdGetMessages
	}
	return jsonx.Marshal(alias(c))
}

// RpcGetCommandsCommand lists available slash commands.
type RpcGetCommandsCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
}

func (*RpcGetCommandsCommand) isRpcCommand()               {}
func (*RpcGetCommandsCommand) CommandType() RpcCommandType { return RpcCmdGetCommands }
func (c *RpcGetCommandsCommand) CommandId() *string        { return c.Id }
func (c *RpcGetCommandsCommand) SetCommandId(id *string)   { c.Id = id }
func (c RpcGetCommandsCommand) MarshalJSON() ([]byte, error) {
	type alias RpcGetCommandsCommand
	if c.Type == "" {
		c.Type = RpcCmdGetCommands
	}
	return jsonx.Marshal(alias(c))
}

// RpcUnknownCommand captures unrecognized incoming commands so error replies retain id and type.
type RpcUnknownCommand struct {
	Id   *string        `json:"id,omitzero"`
	Type RpcCommandType `json:"type"`
	Raw  *jsonx.Object  `json:"-"`
}

func (*RpcUnknownCommand) isRpcCommand()                 {}
func (c *RpcUnknownCommand) CommandType() RpcCommandType { return c.Type }
func (c *RpcUnknownCommand) CommandId() *string          { return c.Id }
func (c *RpcUnknownCommand) SetCommandId(id *string)     { c.Id = id }

// UnmarshalRpcCommand decodes a parsed JSON object into a typed RpcCommand.
func UnmarshalRpcCommand(obj *jsonx.Object) (RpcCommand, error) {
	if obj == nil {
		return nil, fmt.Errorf("command must be a JSON object")
	}
	cmdTypeStr, _ := obj.GetString("type")
	cmdType := RpcCommandType(cmdTypeStr)

	switch cmdType {
	case RpcCmdPrompt:
		var cmd RpcPromptCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSteer:
		var cmd RpcSteerCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdFollowUp:
		var cmd RpcFollowUpCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdAbort:
		var cmd RpcAbortCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdClearQueue:
		var cmd RpcClearQueueCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdNewSession:
		var cmd RpcNewSessionCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetState:
		var cmd RpcGetStateCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSetModel:
		var cmd RpcSetModelCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdCycleModel:
		var cmd RpcCycleModelCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetAvailableModels:
		var cmd RpcGetAvailableModelsCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSetThinkingLevel:
		var cmd RpcSetThinkingLevelCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdCycleThinkingLevel:
		var cmd RpcCycleThinkingLevelCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetAvailableThinkingLevels:
		var cmd RpcGetAvailableThinkingLevelsCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSetSteeringMode:
		var cmd RpcSetSteeringModeCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSetFollowUpMode:
		var cmd RpcSetFollowUpModeCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdCompact:
		var cmd RpcCompactCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSetAutoCompaction:
		var cmd RpcSetAutoCompactionCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSetAutoRetry:
		var cmd RpcSetAutoRetryCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdAbortRetry:
		var cmd RpcAbortRetryCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdBash:
		var cmd RpcBashCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdAbortBash:
		var cmd RpcAbortBashCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetSessionStats:
		var cmd RpcGetSessionStatsCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdExportHtml:
		var cmd RpcExportHtmlCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSwitchSession:
		var cmd RpcSwitchSessionCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdFork:
		var cmd RpcForkCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdClone:
		var cmd RpcCloneCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetForkMessages:
		var cmd RpcGetForkMessagesCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetEntries:
		var cmd RpcGetEntriesCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetTree:
		var cmd RpcGetTreeCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetLastAssistantText:
		var cmd RpcGetLastAssistantTextCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdSetSessionName:
		var cmd RpcSetSessionNameCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetMessages:
		var cmd RpcGetMessagesCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	case RpcCmdGetCommands:
		var cmd RpcGetCommandsCommand
		if err := jsonx.Decode(obj, &cmd); err != nil {
			return nil, err
		}
		return &cmd, nil
	default:
		var id *string
		if idStr, ok := obj.GetString("id"); ok {
			id = &idStr
		}
		return &RpcUnknownCommand{
			Id:   id,
			Type: cmdType,
			Raw:  obj,
		}, nil
	}
}

// RpcSlashCommandSource identifies where an RPC slash command originated.
type RpcSlashCommandSource string

const (
	RpcSlashCommandSourceExtension RpcSlashCommandSource = "extension"
	RpcSlashCommandSourcePrompt    RpcSlashCommandSource = "prompt"
	RpcSlashCommandSourceSkill     RpcSlashCommandSource = "skill"
)

// RpcSlashCommand describes a slash command available for prompt invocation.
type RpcSlashCommand struct {
	Name        string                `json:"name"`
	Description *string               `json:"description,omitzero"`
	Source      RpcSlashCommandSource `json:"source"`
	SourceInfo  utils.SourceInfo      `json:"sourceInfo"`
}

// RpcSessionState reports session state returned by get_state.
type RpcSessionState struct {
	Model                 *ai.Model           `json:"model,omitzero"`
	ThinkingLevel         agent.ThinkingLevel `json:"thinkingLevel"`
	IsStreaming           bool                `json:"isStreaming"`
	IsCompacting          bool                `json:"isCompacting"`
	SteeringMode          agent.QueueMode     `json:"steeringMode"`
	FollowUpMode          agent.QueueMode     `json:"followUpMode"`
	SessionFile           *string             `json:"sessionFile,omitzero"`
	SessionId             string              `json:"sessionId"`
	SessionName           *string             `json:"sessionName,omitzero"`
	AutoCompactionEnabled bool                `json:"autoCompactionEnabled"`
	MessageCount          int                 `json:"messageCount"`
	PendingMessageCount   int                 `json:"pendingMessageCount"`
}

// RpcResponse is the stdout wire response to an RPC command.
type RpcResponse struct {
	Id      *string        `json:"id,omitzero"`
	Type    string         `json:"type"`
	Command string         `json:"command"`
	Success bool           `json:"success"`
	Data    jsonx.Opt[any] `json:"data,omitzero"`
	Error   *string        `json:"error,omitzero"`
}

// MarshalJSON guarantees Type is populated as "response".
func (r RpcResponse) MarshalJSON() ([]byte, error) {
	type alias RpcResponse
	if r.Type == "" {
		r.Type = "response"
	}
	return jsonx.Marshal(alias(r))
}

// RpcPromptData is the data payload of a prompt response.
type RpcPromptData struct {
	Disposition core.PromptDisposition `json:"disposition"`
}

// RpcQueuedInputData is the data payload of steer and follow_up responses.
type RpcQueuedInputData struct {
	Disposition core.QueuedInputDisposition `json:"disposition"`
}

// RpcCancelledData is the data payload for new_session, switch_session, and clone responses.
type RpcCancelledData struct {
	Cancelled bool `json:"cancelled"`
}

// RpcForkData is the data payload of a fork response.
type RpcForkData struct {
	Text      string `json:"text"`
	Cancelled bool   `json:"cancelled"`
}

// RpcAvailableModelsData is the data payload of a get_available_models response.
type RpcAvailableModelsData struct {
	Models []*ai.Model `json:"models"`
}

// RpcCycleThinkingLevelData is the data payload of a non-null cycle_thinking_level response.
type RpcCycleThinkingLevelData struct {
	Level agent.ThinkingLevel `json:"level"`
}

// RpcAvailableThinkingLevelsData is the data payload of get_available_thinking_levels.
type RpcAvailableThinkingLevelsData struct {
	Levels []agent.ThinkingLevel `json:"levels"`
}

// RpcExportHtmlData is the data payload of export_html.
type RpcExportHtmlData struct {
	Path string `json:"path"`
}

// RpcForkMessagesData is the data payload of get_fork_messages.
type RpcForkMessagesData struct {
	Messages []core.ForkableUserMessage `json:"messages"`
}

// RpcEntriesData is the wire payload for get_entries.
type RpcEntriesData struct {
	Entries []core.SessionEntry `json:"entries"`
	LeafId  *string             `json:"leafId"`
}

// RpcTreeData is the wire payload for get_tree.
type RpcTreeData struct {
	Tree   []core.SessionTreeNode `json:"tree"`
	LeafId *string                `json:"leafId"`
}

// RpcLastAssistantTextData is the data payload of get_last_assistant_text.
type RpcLastAssistantTextData struct {
	Text *string `json:"text"`
}

// RpcMessagesData is the data payload of get_messages.
type RpcMessagesData struct {
	Messages agent.AgentMessageList `json:"messages"`
}

// RpcCommandsData is the data payload of get_commands.
type RpcCommandsData struct {
	Commands []RpcSlashCommand `json:"commands"`
}

// RpcExtensionUIRequest is an extension UI prompt or notification emitted on stdout.
type RpcExtensionUIRequest struct {
	Type            string   `json:"type"`
	Id              string   `json:"id"`
	Method          string   `json:"method"`
	Title           *string  `json:"title,omitzero"`
	Options         []string `json:"options,omitzero"`
	Message         *string  `json:"message,omitzero"`
	Placeholder     *string  `json:"placeholder,omitzero"`
	Prefill         *string  `json:"prefill,omitzero"`
	Timeout         *int64   `json:"timeout,omitzero"`
	NotifyType      *string  `json:"notifyType,omitzero"`
	StatusKey       *string  `json:"statusKey,omitzero"`
	StatusText      *string  `json:"statusText,omitzero"`
	WidgetKey       *string  `json:"widgetKey,omitzero"`
	WidgetLines     []string `json:"widgetLines,omitzero"`
	WidgetPlacement *string  `json:"widgetPlacement,omitzero"`
	Text            *string  `json:"text,omitzero"`
}

// AgentSessionEventType allows RpcExtensionUIRequest to satisfy modes.JsonAgentSessionEvent.
func (*RpcExtensionUIRequest) AgentSessionEventType() string {
	return "extension_ui_request"
}

// MarshalJSON ensures Type defaults to "extension_ui_request".
func (r RpcExtensionUIRequest) MarshalJSON() ([]byte, error) {
	type alias RpcExtensionUIRequest
	if r.Type == "" {
		r.Type = "extension_ui_request"
	}
	return jsonx.Marshal(alias(r))
}

// RpcExtensionErrorEvent is emitted on stdout when an extension listener errors in RPC mode.
type RpcExtensionErrorEvent struct {
	Type          string `json:"type"`
	ExtensionPath string `json:"extensionPath"`
	Event         string `json:"event"`
	Error         string `json:"error"`
}

// AgentSessionEventType implements modes.JsonAgentSessionEvent.
func (*RpcExtensionErrorEvent) AgentSessionEventType() string {
	return "extension_error"
}

// MarshalJSON ensures Type defaults to "extension_error".
func (e RpcExtensionErrorEvent) MarshalJSON() ([]byte, error) {
	type alias RpcExtensionErrorEvent
	if e.Type == "" {
		e.Type = "extension_error"
	}
	return jsonx.Marshal(alias(e))
}

// RpcExtensionUIResponse is a client stdin reply to a blocking extension_ui_request.
type RpcExtensionUIResponse struct {
	Type      string  `json:"type"`
	Id        string  `json:"id"`
	Value     *string `json:"value,omitzero"`
	Confirmed *bool   `json:"confirmed,omitzero"`
	Cancelled *bool   `json:"cancelled,omitzero"`
}

// MarshalJSON ensures Type defaults to "extension_ui_response".
func (r RpcExtensionUIResponse) MarshalJSON() ([]byte, error) {
	type alias RpcExtensionUIResponse
	if r.Type == "" {
		r.Type = "extension_ui_response"
	}
	return jsonx.Marshal(alias(r))
}

// RpcUnknownEvent preserves unrecognized or wire-projected event JSON objects on the client.
type RpcUnknownEvent struct {
	TypeName string        `json:"-"`
	Raw      *jsonx.Object `json:"-"`
}

// AgentSessionEventType implements modes.JsonAgentSessionEvent.
func (e *RpcUnknownEvent) AgentSessionEventType() string {
	if e == nil {
		return ""
	}
	return e.TypeName
}

// MarshalJSON writes the underlying raw JSON object.
func (e *RpcUnknownEvent) MarshalJSON() ([]byte, error) {
	if e == nil || e.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(e.Raw)
}

func rtDecodeSessionAgentMessage(v any) (agent.AgentMessage, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return agent.DecodeAgentMessage(v)
	}
	role, _ := obj.GetString("role")
	switch role {
	case "system", "user", "assistant", "toolResult":
		return ai.DecodeMessage(obj)
	case "bashExecution":
		var out core.BashExecutionMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "custom":
		var out core.CustomMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "branchSummary":
		var out core.BranchSummaryMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "compactionSummary":
		var out core.CompactionSummaryMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return agent.DecodeAgentMessage(obj)
	}
}

func rtDecodeSessionEntry(v any) core.SessionEntry {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil
	}
	typ, _ := obj.GetString("type")
	switch typ {
	case "message":
		var base core.SessionEntryBase
		if err := jsonx.Decode(obj, &base); err != nil {
			return nil
		}
		base.Raw = obj
		msgVal, _ := obj.Get("message")
		msg, err := rtDecodeSessionAgentMessage(msgVal)
		if err != nil {
			var msgObj *jsonx.Object
			if cast, ok := msgVal.(*jsonx.Object); ok {
				msgObj = cast
			}
			msg = &agent.UnknownAgentMessage{Raw: msgObj}
		}
		return &core.SessionMessageEntry{
			SessionEntryBase: base,
			Message:          msg,
		}
	case "thinking_level_change":
		var out core.ThinkingLevelChangeEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "model_change":
		var out core.ModelChangeEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "usage":
		var out core.UsageEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "compaction":
		var out core.CompactionEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "branch_summary":
		var out core.BranchSummaryEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "custom":
		var out core.CustomEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "custom_message":
		var out core.CustomMessageEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "context_edit":
		var out core.ContextEditEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "label":
		var out core.LabelEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "session_info":
		var out core.SessionInfoEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	default:
		var out core.UnknownSessionEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	}
}

func rtDecodeSessionTreeNode(v any) (core.SessionTreeNode, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return core.SessionTreeNode{}, fmt.Errorf("tree node must be a JSON object")
	}
	var node core.SessionTreeNode
	if entryRaw, ok := obj.Get("entry"); ok {
		node.Entry = rtDecodeSessionEntry(entryRaw)
	}
	if lbl, ok := obj.GetString("label"); ok {
		node.Label = &lbl
	}
	if ts, ok := obj.GetString("labelTimestamp"); ok {
		node.LabelTimestamp = &ts
	}
	if childrenRaw, ok := obj.GetArray("children"); ok {
		node.Children = make([]core.SessionTreeNode, 0, len(childrenRaw))
		for _, childVal := range childrenRaw {
			child, err := rtDecodeSessionTreeNode(childVal)
			if err != nil {
				return core.SessionTreeNode{}, err
			}
			node.Children = append(node.Children, child)
		}
	} else {
		node.Children = []core.SessionTreeNode{}
	}
	return node, nil
}

func rtDecodeJsonAssistantMessageEvent(obj *jsonx.Object) (modes.JsonAssistantMessageEvent, error) {
	if obj == nil {
		return nil, fmt.Errorf("assistantMessageEvent must be a JSON object")
	}
	typ, _ := obj.GetString("type")
	switch typ {
	case "start":
		var ev modes.JsonAssistantMessageEventStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "text_start":
		var ev modes.JsonAssistantMessageEventTextStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "text_delta":
		var ev modes.JsonAssistantMessageEventTextDelta
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "text_end":
		var ev modes.JsonAssistantMessageEventTextEnd
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "thinking_start":
		var ev modes.JsonAssistantMessageEventThinkingStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "thinking_delta":
		var ev modes.JsonAssistantMessageEventThinkingDelta
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "thinking_end":
		var ev modes.JsonAssistantMessageEventThinkingEnd
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "toolcall_start":
		var ev modes.JsonAssistantMessageEventToolcallStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "toolcall_delta":
		var ev modes.JsonAssistantMessageEventToolcallDelta
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "toolcall_end":
		var ev modes.JsonAssistantMessageEventToolcallEnd
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "done":
		var ev modes.JsonAssistantMessageEventDone
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "error":
		var ev modes.JsonAssistantMessageEventError
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	default:
		return &modes.JsonAssistantMessageEventUnknown{Raw: obj.Clone()}, nil
	}
}

// DecodeRpcEvent parses a decoded JSON object from RPC stdout into a typed
// modes.JsonAgentSessionEvent.
func DecodeRpcEvent(obj *jsonx.Object) (modes.JsonAgentSessionEvent, error) {
	if obj == nil {
		return nil, fmt.Errorf("event must be a JSON object")
	}
	typ, _ := obj.GetString("type")
	switch typ {
	case "agent_start":
		var ev core.AgentSessionEventAgentStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "agent_end":
		var ev core.AgentSessionEventAgentEnd
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "turn_start":
		var ev core.AgentSessionEventTurnStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "turn_end":
		var ev core.AgentSessionEventTurnEnd
		if msgRaw, ok := obj.Get("message"); ok && msgRaw != nil {
			msg, err := rtDecodeSessionAgentMessage(msgRaw)
			if err != nil {
				return nil, err
			}
			ev.Message = msg
		}
		if trRaw, ok := obj.Get("toolResults"); ok && trRaw != nil {
			if err := jsonx.Decode(trRaw, &ev.ToolResults); err != nil {
				return nil, err
			}
		}
		ev.Type = "turn_end"
		return &ev, nil
	case "message_start":
		var ev core.AgentSessionEventMessageStart
		if msgRaw, ok := obj.Get("message"); ok && msgRaw != nil {
			msg, err := rtDecodeSessionAgentMessage(msgRaw)
			if err != nil {
				return nil, err
			}
			ev.Message = msg
		}
		ev.Type = "message_start"
		return &ev, nil
	case "message_update":
		var usage ai.Usage
		if uRaw, ok := obj.Get("usage"); ok && uRaw != nil {
			if err := jsonx.Decode(uRaw, &usage); err != nil {
				return nil, err
			}
		}
		ameObj, _ := obj.GetObject("assistantMessageEvent")
		ame, err := rtDecodeJsonAssistantMessageEvent(ameObj)
		if err != nil {
			return nil, err
		}
		return &modes.JsonMessageUpdateEvent{
			Type:                  "message_update",
			Usage:                 usage,
			AssistantMessageEvent: ame,
		}, nil
	case "message_end":
		var ev core.AgentSessionEventMessageEnd
		if msgRaw, ok := obj.Get("message"); ok && msgRaw != nil {
			msg, err := rtDecodeSessionAgentMessage(msgRaw)
			if err != nil {
				return nil, err
			}
			ev.Message = msg
		}
		ev.Type = "message_end"
		return &ev, nil
	case "tool_execution_start":
		var ev core.AgentSessionEventToolExecutionStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "tool_execution_update":
		var ev core.AgentSessionEventToolExecutionUpdate
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "tool_execution_end":
		var ev core.AgentSessionEventToolExecutionEnd
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "agent_settled":
		var ev core.AgentSessionEventAgentSettled
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "queue_update":
		var ev core.AgentSessionEventQueueUpdate
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "compaction_start":
		var ev core.AgentSessionEventCompactionStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "compaction_end":
		var ev core.AgentSessionEventCompactionEnd
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "entry_appended":
		entryRaw, _ := obj.Get("entry")
		return &core.AgentSessionEventEntryAppended{
			Type:  "entry_appended",
			Entry: rtDecodeSessionEntry(entryRaw),
		}, nil
	case "session_info_changed":
		var ev core.AgentSessionEventSessionInfoChanged
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "thinking_level_changed":
		var ev core.AgentSessionEventThinkingLevelChanged
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "auto_retry_start":
		var ev core.AgentSessionEventAutoRetryStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "auto_retry_end":
		var ev core.AgentSessionEventAutoRetryEnd
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "summarization_retry_scheduled":
		var ev core.AgentSessionEventSummarizationRetryScheduled
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "summarization_retry_attempt_start":
		var ev core.AgentSessionEventSummarizationRetryAttemptStart
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "summarization_retry_finished":
		var ev core.AgentSessionEventSummarizationRetryFinished
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "extension_ui_request":
		var ev RpcExtensionUIRequest
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	case "extension_error":
		var ev RpcExtensionErrorEvent
		if err := jsonx.Decode(obj, &ev); err != nil {
			return nil, err
		}
		return &ev, nil
	default:
		return &RpcUnknownEvent{
			TypeName: typ,
			Raw:      obj.Clone(),
		}, nil
	}
}
