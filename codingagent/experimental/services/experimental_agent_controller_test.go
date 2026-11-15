// Ported from packages/coding-agent/test/experimental-agent-controller.test.ts (pi v1.0.0).

package services_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func teacMustOpenFaux(t *testing.T, responses ...providers.FauxResponseStep) *testkit.FauxConversation {
	t.Helper()
	fc, err := testkit.OpenFauxConversation(responses...)
	if err != nil {
		t.Fatalf("OpenFauxConversation failed: %v", err)
	}
	t.Cleanup(func() {
		_ = fc.Close()
	})
	return fc
}

func TestAgentControllerPromptsRootConversationThroughServiceCatalogue(t *testing.T) {
	fc := teacMustOpenFaux(t, providers.FauxResponseMessage{
		Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello back"), nil),
	})
	host, err := chord.CreateFacetHost(&chord.FacetOptions{
		Facets: []chord.Facet{
			chord.DefineFacet(chord.Facet{
				Id: "test-agent-controller",
				Setup: func(env chord.FacetEnvironment) error {
					env.Provide(
						services.AgentControllerService,
						services.ProvideAgentController(services.CreateAgentController(fc.Harness, fc.Conversation)),
					)
					return nil
				},
			}),
		},
	})
	if err != nil {
		t.Fatalf("CreateFacetHost failed: %v", err)
	}
	defer func() { _ = host.Dispose() }()

	cat := host.Services().Catalogue()
	wantCat := []chord.ServiceCatalogueEntry{{ServiceId: services.AgentControllerService.Id, Mode: chord.ServiceModeSingleton}}
	if !reflect.DeepEqual(cat, wantCat) {
		t.Fatalf("expected catalogue %v, got %v", wantCat, cat)
	}

	reqJson, err := jsonx.Encode(services.AgentPromptRequest{Message: "hello", Images: nil})
	if err != nil {
		t.Fatalf("jsonx.Encode failed: %v", err)
	}
	rawResp, ok, err := host.Services().Invoke(chord.ServiceCall{
		ServiceId: services.AgentControllerService.Id,
		Member:    "prompt",
		Args:      []chord.JsonValue{reqJson},
	}, chord.BackgroundContext)
	if err != nil || !ok {
		t.Fatalf("invoke prompt failed (ok=%v): %v", ok, err)
	}
	var resp services.AgentOperationResponse
	if err := jsonx.Decode(rawResp, &resp); err != nil {
		t.Fatalf("decode prompt response: %v", err)
	}
	if !resp.Accepted || resp.OperationId == nil || *resp.OperationId == "" || resp.Error != nil {
		t.Fatalf("unexpected prompt response: %+v", resp)
	}

	rawWait, ok, err := host.Services().Invoke(chord.ServiceCall{
		ServiceId: services.AgentControllerService.Id,
		Member:    "waitForPrompt",
		Args:      []chord.JsonValue{*resp.OperationId},
	}, chord.BackgroundContext)
	if err != nil || !ok {
		t.Fatalf("invoke waitForPrompt failed (ok=%v): %v", ok, err)
	}
	var waitRes services.AgentPromptResult
	if err := jsonx.Decode(rawWait, &waitRes); err != nil {
		t.Fatalf("decode waitForPrompt result: %v", err)
	}
	if waitRes.Status != "done" || waitRes.Text == nil || *waitRes.Text != "hello back" || waitRes.Reason != nil {
		t.Fatalf("unexpected waitForPrompt result: %+v", waitRes)
	}
}

func TestAgentControllerRejectsPromptWhileBusyAndQueuesSteerAndFollowUp(t *testing.T) {
	pending := testkit.PendingResponse()
	fc := teacMustOpenFaux(t, pending.Step)
	controller := services.CreateAgentController(fc.Harness, fc.Conversation)

	first, err := controller.Prompt(services.AgentPromptRequest{Message: "first", Images: nil}, chord.BackgroundContext)
	if err != nil || !first.Accepted || first.OperationId == nil {
		t.Fatalf("expected first prompt accepted, got %+v err=%v", first, err)
	}
	select {
	case <-pending.Reached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for pending faux response to be reached")
	}

	second, err := controller.Prompt(services.AgentPromptRequest{Message: "second", Images: nil}, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("second Prompt returned unexpected error: %v", err)
	}
	if second.Accepted || second.OperationId != nil || second.Error == nil || second.Error.Code != "busy" || !strings.Contains(second.Error.Message, "busy") {
		t.Fatalf("expected busy rejection for second prompt, got %+v", second)
	}

	steer, err := controller.Steer(services.AgentPromptRequest{Message: "steer", Images: nil}, chord.BackgroundContext)
	if err != nil || !steer.Accepted || steer.EntryId == nil || steer.Error != nil {
		t.Fatalf("expected steer accepted, got %+v err=%v", steer, err)
	}
	followUp, err := controller.FollowUp(services.AgentPromptRequest{Message: "later", Images: nil}, chord.BackgroundContext)
	if err != nil || !followUp.Accepted || followUp.EntryId == nil || followUp.Error != nil {
		t.Fatalf("expected followUp accepted, got %+v err=%v", followUp, err)
	}

	inboxView, err := fc.Conversation.ViewState(chord.BackgroundContext)
	if err != nil {
		t.Fatalf("ViewState failed: %v", err)
	}
	val, _ := inboxView.Value()
	inboxView.Dispose()
	var inbox durable.InboxState
	if rawInbox := val.Docs["pi.inbox"]; rawInbox != nil {
		if err := jsonx.Decode(rawInbox, &inbox); err != nil {
			t.Fatalf("decode pi.inbox failed: %v", err)
		}
	}
	var modes []string
	for _, item := range inbox.Items {
		modes = append(modes, string(item.Mode))
	}
	if !reflect.DeepEqual(modes, []string{"steer", "followUp"}) {
		t.Fatalf("expected inbox modes [steer followUp], got %v", modes)
	}

	resCancel, err := controller.CancelQueued(*followUp.EntryId, chord.BackgroundContext)
	if err != nil || resCancel.Outcome != "cancelled" {
		t.Fatalf("expected cancelled, got %+v err=%v", resCancel, err)
	}
	resAlready, err := controller.CancelQueued(*followUp.EntryId, chord.BackgroundContext)
	if err != nil || resAlready.Outcome != "already_consumed" {
		t.Fatalf("expected already_consumed, got %+v err=%v", resAlready, err)
	}
	resMissing, err := controller.CancelQueued("999", chord.BackgroundContext)
	if err != nil || resMissing.Outcome != "not_found" {
		t.Fatalf("expected not_found for 999, got %+v err=%v", resMissing, err)
	}
	resBad, err := controller.CancelQueued("not-an-id", chord.BackgroundContext)
	if err != nil || resBad.Outcome != "not_found" {
		t.Fatalf("expected not_found for not-an-id, got %+v err=%v", resBad, err)
	}

	if err := controller.Abort(chord.BackgroundContext); err != nil {
		t.Fatalf("Abort failed: %v", err)
	}
	liveView, err := fc.Conversation.ViewState(chord.BackgroundContext)
	if err != nil {
		t.Fatalf("ViewState after abort failed: %v", err)
	}
	valAfter, _ := liveView.Value()
	liveView.Dispose()
	var live durable.LiveState
	if rawLive := valAfter.Docs["pi.live"]; rawLive != nil {
		if err := jsonx.Decode(rawLive, &live); err != nil {
			t.Fatalf("decode pi.live failed: %v", err)
		}
	}
	if live.Run != nil {
		t.Fatalf("expected pi.live.run cleared after abort, got %+v", live.Run)
	}

	waitRes, err := controller.WaitForPrompt(*first.OperationId, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("WaitForPrompt failed: %v", err)
	}
	if waitRes.Status != "unanswered" || waitRes.Text != nil {
		t.Fatalf("expected unanswered prompt result with nil text, got %+v", waitRes)
	}
}

func TestAgentControllerStartsCompactionTask(t *testing.T) {
	fc := teacMustOpenFaux(t)
	controller := services.CreateAgentController(fc.Harness, fc.Conversation)
	instr := "short"
	res, err := controller.Compact(services.AgentCompactionRequest{CustomInstructions: &instr}, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if !res.Accepted || res.OperationId == nil || *res.OperationId == "" || res.Error != nil {
		t.Fatalf("unexpected Compact response: %+v", res)
	}
}
