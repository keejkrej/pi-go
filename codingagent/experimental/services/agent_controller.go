// Ported from packages/coding-agent/src/experimental/services/agent-controller.ts (pi v1.0.0).

package services

import (
	"fmt"

	"github.com/keejkrej/pi-go/chord"
)

// AgentPromptImage is an image block included in a presentation prompt request.
type AgentPromptImage struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// AgentPromptRequest carries prompt text and optional image attachments.
type AgentPromptRequest struct {
	Message string             `json:"message"`
	Images  []AgentPromptImage `json:"images"`
}

// AgentOperationError describes an agent controller operation failure.
type AgentOperationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AgentOperationResponse identifies the durable submission of a prompt or the task of a compaction.
type AgentOperationResponse struct {
	Accepted    bool                 `json:"accepted"`
	OperationId *string              `json:"operationId"`
	Error       *AgentOperationError `json:"error"`
}

func (AgentOperationResponse) isSlashCommandRunResult() {}

// AgentQueueResponse identifies a queued steering or follow-up submission.
type AgentQueueResponse struct {
	Accepted bool                 `json:"accepted"`
	EntryId  *string              `json:"entryId"`
	Error    *AgentOperationError `json:"error"`
}

func (AgentQueueResponse) isSlashCommandRunResult() {}

// AgentCancelQueuedResult reports the cancellation attempt outcome for a queued submission.
type AgentCancelQueuedResult struct {
	Outcome string `json:"outcome"`
}

// AgentPromptResult is the settled outcome of a prompt: the answer's text, or why it got none.
type AgentPromptResult struct {
	Status string  `json:"status"`
	Text   *string `json:"text"`
	Reason *string `json:"reason"`
}

// AgentCompactionRequest configures manual conversation compaction.
type AgentCompactionRequest struct {
	CustomInstructions *string `json:"customInstructions"`
}

// AgentController is the presentation-safe command facade over the worker-owned root conversation.
type AgentController interface {
	// Prompt starts a run; rejected with code "busy" while one is active.
	Prompt(request AgentPromptRequest, ctx chord.Context) (AgentOperationResponse, error)
	// Steer steers the active run, or starts one when idle.
	Steer(request AgentPromptRequest, ctx chord.Context) (AgentQueueResponse, error)
	// FollowUp queues input for after the active run, or starts one when idle.
	FollowUp(request AgentPromptRequest, ctx chord.Context) (AgentQueueResponse, error)
	CancelQueued(entryId string, ctx chord.Context) (AgentCancelQueuedResult, error)
	// Abort withdraws queued input and aborts the active run and compaction.
	Abort(ctx chord.Context) error
	Compact(request AgentCompactionRequest, ctx chord.Context) (AgentOperationResponse, error)
	// WaitForPrompt waits until the prompt with this operationId is answered or settles unanswered.
	WaitForPrompt(operationId string, ctx chord.Context) (AgentPromptResult, error)
}

// AgentControllerService is the Chord service token for AgentController.
var AgentControllerService = connDefineService[AgentController]("pi.agent-controller")

// ProvideAgentController builds a Chord provider member table for impl.
func ProvideAgentController(impl AgentController) map[string]any {
	return map[string]any{
		"prompt": func(rawReq any, ctx chord.Context) (any, error) {
			req, err := connDecodeJson[AgentPromptRequest](rawReq)
			if err != nil {
				return nil, err
			}
			resp, err := impl.Prompt(req, ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(resp)
		},
		"steer": func(rawReq any, ctx chord.Context) (any, error) {
			req, err := connDecodeJson[AgentPromptRequest](rawReq)
			if err != nil {
				return nil, err
			}
			resp, err := impl.Steer(req, ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(resp)
		},
		"followUp": func(rawReq any, ctx chord.Context) (any, error) {
			req, err := connDecodeJson[AgentPromptRequest](rawReq)
			if err != nil {
				return nil, err
			}
			resp, err := impl.FollowUp(req, ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(resp)
		},
		"cancelQueued": func(entryId string, ctx chord.Context) (any, error) {
			resp, err := impl.CancelQueued(entryId, ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(resp)
		},
		"abort": func(ctx chord.Context) error {
			return impl.Abort(ctx)
		},
		"compact": func(rawReq any, ctx chord.Context) (any, error) {
			req, err := connDecodeJson[AgentCompactionRequest](rawReq)
			if err != nil {
				return nil, err
			}
			resp, err := impl.Compact(req, ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(resp)
		},
		"waitForPrompt": func(operationId string, ctx chord.Context) (any, error) {
			resp, err := impl.WaitForPrompt(operationId, ctx)
			if err != nil {
				return nil, err
			}
			return connEncodeJsonValue(resp)
		},
	}
}

// AsAgentController wraps a Chord service handle or concrete implementation as AgentController.
func AsAgentController(raw any) AgentController {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(AgentController); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &acClient{view: view}
	}
	panic(fmt.Sprintf("services: unsupported AgentController handle %T", raw))
}

type acClient struct {
	view connServiceView
}

func (c *acClient) Prompt(request AgentPromptRequest, ctx chord.Context) (AgentOperationResponse, error) {
	return acCall[AgentOperationResponse](c.view, "prompt", request, ctx)
}

func (c *acClient) Steer(request AgentPromptRequest, ctx chord.Context) (AgentQueueResponse, error) {
	return acCall[AgentQueueResponse](c.view, "steer", request, ctx)
}

func (c *acClient) FollowUp(request AgentPromptRequest, ctx chord.Context) (AgentQueueResponse, error) {
	return acCall[AgentQueueResponse](c.view, "followUp", request, ctx)
}

func (c *acClient) CancelQueued(entryId string, ctx chord.Context) (AgentCancelQueuedResult, error) {
	return acCall[AgentCancelQueuedResult](c.view, "cancelQueued", entryId, ctx)
}

func (c *acClient) Abort(ctx chord.Context) error {
	_, err := c.view.Call("abort", ctx)
	return err
}

func (c *acClient) Compact(request AgentCompactionRequest, ctx chord.Context) (AgentOperationResponse, error) {
	return acCall[AgentOperationResponse](c.view, "compact", request, ctx)
}

func (c *acClient) WaitForPrompt(operationId string, ctx chord.Context) (AgentPromptResult, error) {
	return acCall[AgentPromptResult](c.view, "waitForPrompt", operationId, ctx)
}

func acCall[Res any](view connServiceView, member string, arg any, ctx chord.Context) (Res, error) {
	var zero Res
	encoded, err := connEncodeJsonValue(arg)
	if err != nil {
		return zero, err
	}
	raw, err := view.Call(member, encoded, ctx)
	if err != nil {
		return zero, err
	}
	return connDecodeJson[Res](raw)
}
