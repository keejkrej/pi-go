// Ported from packages/coding-agent/src/experimental/services/agent-controller-provider.ts (pi v1.0.0).

package services

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
)

// CreateAgentController constructs an AgentController backed by a durable Harness and Conversation.
func CreateAgentController(harness durable.Harness, conversation durable.Conversation) AgentController {
	return &acpController{
		harness:      harness,
		conversation: conversation,
	}
}

type acpController struct {
	harness      durable.Harness
	conversation durable.Conversation
}

func (c *acpController) Prompt(request AgentPromptRequest, ctx chord.Context) (AgentOperationResponse, error) {
	sub, err := c.conversation.Submit(durable.SubmissionDraft{
		Type:     durable.SubmissionTypeInput,
		Content:  acpToInput(request),
		WhenBusy: durable.SubmissionWhenBusyReject,
	}, ctx)
	if err != nil {
		agentErr := acpToAgentError(err)
		return AgentOperationResponse{Accepted: false, OperationId: nil, Error: &agentErr}, nil
	}
	idStr := strconv.FormatInt(int64(sub.Id()), 10)
	return AgentOperationResponse{Accepted: true, OperationId: &idStr, Error: nil}, nil
}

func (c *acpController) Steer(request AgentPromptRequest, ctx chord.Context) (AgentQueueResponse, error) {
	return c.queue(durable.SubmissionWhenBusySteer, request, ctx)
}

func (c *acpController) FollowUp(request AgentPromptRequest, ctx chord.Context) (AgentQueueResponse, error) {
	return c.queue(durable.SubmissionWhenBusyFollowUp, request, ctx)
}

func (c *acpController) queue(whenBusy durable.SubmissionWhenBusy, request AgentPromptRequest, ctx chord.Context) (AgentQueueResponse, error) {
	sub, err := c.conversation.Submit(durable.SubmissionDraft{
		Type:     durable.SubmissionTypeInput,
		Content:  acpToInput(request),
		WhenBusy: whenBusy,
	}, ctx)
	if err != nil {
		agentErr := acpToAgentError(err)
		return AgentQueueResponse{Accepted: false, EntryId: nil, Error: &agentErr}, nil
	}
	idStr := strconv.FormatInt(int64(sub.Id()), 10)
	return AgentQueueResponse{Accepted: true, EntryId: &idStr, Error: nil}, nil
}

func (c *acpController) CancelQueued(entryId string, ctx chord.Context) (AgentCancelQueuedResult, error) {
	id, ok := acpParseSubmissionId(entryId)
	if !ok {
		return AgentCancelQueuedResult{Outcome: "not_found"}, nil
	}
	result, err := c.harness.AbortSubmission(ctx, id, c.conversation.Id())
	if err != nil {
		return AgentCancelQueuedResult{}, err
	}
	switch result {
	case durable.SubmissionAbortAborted:
		return AgentCancelQueuedResult{Outcome: "cancelled"}, nil
	case durable.SubmissionAbortNotFound:
		return AgentCancelQueuedResult{Outcome: "not_found"}, nil
	default:
		return AgentCancelQueuedResult{Outcome: "already_consumed"}, nil
	}
}

func (c *acpController) Abort(ctx chord.Context) error {
	return c.conversation.Abort(ctx)
}

func (c *acpController) Compact(request AgentCompactionRequest, ctx chord.Context) (AgentOperationResponse, error) {
	id, err := c.conversation.Compact(request.CustomInstructions, ctx)
	if err != nil {
		agentErr := acpToAgentError(err)
		return AgentOperationResponse{Accepted: false, OperationId: nil, Error: &agentErr}, nil
	}
	idStr := strconv.FormatInt(int64(id), 10)
	return AgentOperationResponse{Accepted: true, OperationId: &idStr, Error: nil}, nil
}

func (c *acpController) WaitForPrompt(operationId string, ctx chord.Context) (AgentPromptResult, error) {
	id, ok := acpParseSubmissionId(operationId)
	if !ok {
		return AgentPromptResult{}, fmt.Errorf("Unknown prompt: %s", operationId)
	}
	submission, err := c.harness.Submission(ctx, id)
	if err != nil || submission == nil {
		return AgentPromptResult{}, fmt.Errorf("Unknown prompt: %s", operationId)
	}
	settled, err := submission.Wait(ctx)
	if err != nil {
		return AgentPromptResult{}, err
	}
	if settled.Status == durable.SubmissionStatusUnanswered {
		return AgentPromptResult{Status: "unanswered", Text: nil, Reason: settled.Reason}, nil
	}
	var answerId *durable.EntryId
	if settled.Type == durable.SubmissionTypeInput {
		answerId = settled.Answer
	}
	var answerEntry *durable.EntryRecord
	if answerId != nil {
		if err := c.harness.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.Entry(*answerId)
			if err != nil {
				return err
			}
			answerEntry = e
			return nil
		}); err != nil {
			return AgentPromptResult{}, err
		}
	}
	text := ""
	if answerEntry != nil && len(answerEntry.Model) > 0 {
		if assistant, ok := answerEntry.Model[0].(*ai.AssistantMessage); ok && assistant != nil {
			var sb strings.Builder
			for _, block := range assistant.Content {
				if tc, ok := block.(*ai.TextContent); ok && tc != nil {
					sb.WriteString(tc.Text)
				}
			}
			text = sb.String()
		}
	}
	return AgentPromptResult{Status: "done", Text: &text, Reason: nil}, nil
}

func acpParseSubmissionId(value string) (durable.SubmissionId, bool) {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return 0, false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || id > 9007199254740991 {
		return 0, false
	}
	return durable.SubmissionId(id), true
}

func acpToInput(request AgentPromptRequest) durable.UserInput {
	if len(request.Images) == 0 {
		msg := request.Message
		return durable.UserInput{Text: &msg}
	}
	blocks := make(ai.ContentList, 0, 1+len(request.Images))
	blocks = append(blocks, &ai.TextContent{Type: "text", Text: request.Message})
	for _, img := range request.Images {
		blocks = append(blocks, &ai.ImageContent{
			Type:     "image",
			Data:     img.Data,
			MimeType: img.MimeType,
		})
	}
	return durable.UserInput{Blocks: blocks}
}

func acpToAgentError(err error) AgentOperationError {
	var busy *durable.ConversationBusy
	if errors.As(err, &busy) {
		return AgentOperationError{Code: "busy", Message: err.Error()}
	}
	return AgentOperationError{Code: "operation_failed", Message: err.Error()}
}
