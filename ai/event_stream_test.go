package ai_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestEventStream(t *testing.T) {
	ctx := context.Background()

	t.Run("drains buffered events in order and ignores events pushed after completion", func(t *testing.T) {
		stream := ai.NewEventStream(
			func(event int) bool { return event == 3 },
			func(event int) int { return event },
		)
		stream.Push(1)
		stream.Push(2)
		stream.Push(3)
		stream.Push(4)

		res, err := stream.Result(ctx)
		if err != nil {
			t.Fatalf("Result: %v", err)
		}
		if res != 3 {
			t.Fatalf("Result = %d, want 3", res)
		}

		var events []int
		for ev := range stream.All() {
			events = append(events, ev)
		}
		if !slices.Equal(events, []int{1, 2, 3}) {
			t.Fatalf("events = %v, want [1 2 3]", events)
		}
	})

	t.Run("preserves order when events arrive after buffered draining starts", func(t *testing.T) {
		stream := ai.NewEventStream(
			func(int) bool { return false },
			func(event int) int { return event },
		)
		stream.Push(1)
		stream.Push(2)

		v, ok, err := stream.Next(ctx)
		if err != nil || !ok || v != 1 {
			t.Fatalf("first Next = (%d, %v, %v), want (1, true, nil)", v, ok, err)
		}

		stream.Push(3)
		v, ok, err = stream.Next(ctx)
		if err != nil || !ok || v != 2 {
			t.Fatalf("second Next = (%d, %v, %v), want (2, true, nil)", v, ok, err)
		}
		v, ok, err = stream.Next(ctx)
		if err != nil || !ok || v != 3 {
			t.Fatalf("third Next = (%d, %v, %v), want (3, true, nil)", v, ok, err)
		}

		stream.End(3)
		v, ok, err = stream.Next(ctx)
		if err != nil || ok || v != 0 {
			t.Fatalf("end Next = (%d, %v, %v), want (0, false, nil)", v, ok, err)
		}
	})

	t.Run("delivers events to waiting consumers in registration order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			stream := ai.NewEventStream(
				func(int) bool { return false },
				func(event int) int { return event },
			)
			type nextRes struct {
				val int
				ok  bool
				err error
			}
			firstCh := make(chan nextRes, 1)
			secondCh := make(chan nextRes, 1)

			go func() {
				v, ok, err := stream.Next(ctx)
				firstCh <- nextRes{v, ok, err}
			}()
			synctest.Wait()

			go func() {
				v, ok, err := stream.Next(ctx)
				secondCh <- nextRes{v, ok, err}
			}()
			synctest.Wait()

			stream.Push(1)
			stream.Push(2)

			first := <-firstCh
			second := <-secondCh
			if first.err != nil || !first.ok || first.val != 1 {
				t.Fatalf("first = %+v, want {1 true nil}", first)
			}
			if second.err != nil || !second.ok || second.val != 2 {
				t.Fatalf("second = %+v, want {2 true nil}", second)
			}
			stream.End(0)
		})
	})

	t.Run("drains buffered events after end and resolves the explicit result", func(t *testing.T) {
		stream := ai.NewEventStream(
			func(int) bool { return false },
			func(event int) string { return fmt.Sprintf("%d", event) },
		)
		stream.Push(1)
		stream.Push(2)
		stream.End("complete")

		res, err := stream.Result(ctx)
		if err != nil {
			t.Fatalf("Result: %v", err)
		}
		if res != "complete" {
			t.Fatalf("Result = %q, want %q", res, "complete")
		}

		var events []int
		for ev := range stream.All() {
			events = append(events, ev)
		}
		if !slices.Equal(events, []int{1, 2}) {
			t.Fatalf("events = %v, want [1 2]", events)
		}
	})

	t.Run("wakes all waiting consumers when ended without a result", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			stream := ai.NewEventStream(
				func(int) bool { return false },
				func(event int) int { return event },
			)
			type nextRes struct {
				ok  bool
				err error
			}
			firstCh := make(chan nextRes, 1)
			secondCh := make(chan nextRes, 1)

			go func() {
				_, ok, err := stream.Next(ctx)
				firstCh <- nextRes{ok, err}
			}()
			go func() {
				_, ok, err := stream.Next(ctx)
				secondCh <- nextRes{ok, err}
			}()
			synctest.Wait()

			stream.End()

			first := <-firstCh
			second := <-secondCh
			if first.err != nil || first.ok {
				t.Fatalf("first = %+v, want {false nil}", first)
			}
			if second.err != nil || second.ok {
				t.Fatalf("second = %+v, want {false nil}", second)
			}
		})
	})
}

func testEventStreamAssistantMessage(timestamp int64, durationMs *int64) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        ai.KnownApiOpenaiResponses,
		Provider:   "openai",
		Model:      "m",
		Usage:      ai.Usage{},
		StopReason: ai.StopReasonStop,
		Timestamp:  timestamp,
		DurationMs: durationMs,
	}
}

func TestAssistantMessageEventStreamTiming(t *testing.T) {
	ctx := context.Background()

	t.Run("sets durationMs on the final done or error message of a response it saw start", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			done := ai.NewAssistantMessageEventStream()
			answer := testEventStreamAssistantMessage(js.DateNow(), nil)
			time.Sleep(20 * time.Millisecond)
			done.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: answer})
			if answer.DurationMs == nil || *answer.DurationMs < 15 {
				t.Fatalf("answer.DurationMs = %v, want >= 15", answer.DurationMs)
			}
			res, err := done.Result(ctx)
			if err != nil {
				t.Fatalf("Result: %v", err)
			}
			if res.DurationMs == nil || *res.DurationMs != *answer.DurationMs {
				t.Fatalf("res.DurationMs = %v, want %v", res.DurationMs, *answer.DurationMs)
			}

			failed := ai.NewAssistantMessageEventStream()
			errMsg := testEventStreamAssistantMessage(js.DateNow(), nil)
			errMsg.StopReason = ai.StopReasonError
			failed.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
			if errMsg.DurationMs == nil || *errMsg.DurationMs < 0 {
				t.Fatalf("errMsg.DurationMs = %v, want >= 0", errMsg.DurationMs)
			}

			ended := ai.NewAssistantMessageEventStream()
			result := testEventStreamAssistantMessage(js.DateNow(), nil)
			ended.End(result)
			if result.DurationMs == nil || *result.DurationMs < 0 {
				t.Fatalf("result.DurationMs = %v, want >= 0", result.DurationMs)
			}
		})
	})

	t.Run("keeps an existing duration, so a forwarding stream keeps the inner measurement", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			outer := ai.NewAssistantMessageEventStream()
			time.Sleep(20 * time.Millisecond)
			inner := ai.NewAssistantMessageEventStream()
			answer := testEventStreamAssistantMessage(js.DateNow(), nil)
			inner.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: answer})
			if answer.DurationMs == nil {
				t.Fatal("expected inner duration to be stamped")
			}
			measured := *answer.DurationMs
			outer.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: answer})
			if answer.DurationMs == nil || *answer.DurationMs != measured {
				t.Fatalf("answer.DurationMs = %v, want %d", answer.DurationMs, measured)
			}
			if measured >= 20 {
				t.Fatalf("measured = %d, want < 20", measured)
			}

			presetVal := int64(1234)
			preset := testEventStreamAssistantMessage(js.DateNow(), &presetVal)
			ai.NewAssistantMessageEventStream().Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: preset})
			if preset.DurationMs == nil || *preset.DurationMs != 1234 {
				t.Fatalf("preset.DurationMs = %v, want 1234", preset.DurationMs)
			}
		})
	})

	t.Run("leaves a message untimed when it started before the stream, such as a fetched deferred result", func(t *testing.T) {
		fetched := testEventStreamAssistantMessage(js.DateNow()-60_000, nil)
		ai.NewAssistantMessageEventStream().Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: fetched})
		if fetched.DurationMs != nil {
			t.Fatalf("fetched.DurationMs = %v, want nil", *fetched.DurationMs)
		}
	})

	t.Run("does not time a message pushed after the stream completed", func(t *testing.T) {
		stream := ai.NewAssistantMessageEventStream()
		stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: testEventStreamAssistantMessage(js.DateNow(), nil)})
		late := testEventStreamAssistantMessage(js.DateNow(), nil)
		stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: late})
		if late.DurationMs != nil {
			t.Fatalf("late.DurationMs = %v, want nil", *late.DurationMs)
		}
	})
}

func TestAssistantMessageEventStreamSnapshotIsolation(t *testing.T) {
	ctx := context.Background()

	t.Run("concurrent producer mutations do not race with consumer reading events and result", func(t *testing.T) {
		for iter := range 25 {
			stream := ai.NewAssistantMessageEventStream()
			cache1h := 10
			reasoning := 5
			totalFromProvider := false
			expiresAt := int64(1000)
			output := testEventStreamAssistantMessage(js.DateNow(), nil)
			output.StopReason = ai.StopReasonPending
			output.Usage.CacheWrite1h = &cache1h
			output.Usage.Reasoning = &reasoning
			output.Usage.Cost.TotalFromProvider = &totalFromProvider
			output.Diagnostics = []ai.AssistantMessageDiagnostic{{
				Type:      "info",
				Timestamp: 123,
				Error:     &ai.DiagnosticErrorInfo{Message: "init", Code: jsonx.ObjectOf("k", "v")},
				Details:   jsonx.ObjectOf("step", 0),
			}}
			output.Deferred = &ai.DeferredHandle{
				Provider:  "anthropic",
				ModelId:   "m",
				Api:       "anthropic-messages",
				Id:        "def-1",
				ExpiresAt: &expiresAt,
				Data:      jsonx.Some[any](jsonx.ObjectOf("cursor", "0")),
			}

			consumerDone := make(chan struct{})
			var seenPartials []*ai.AssistantMessage
			var seenToolEnd *ai.ToolCall
			go func() {
				defer close(consumerDone)
				inspect := func(msg *ai.AssistantMessage) {
					if msg == nil {
						return
					}
					_ = msg.ResponseId
					if msg.ResponseId != nil {
						_ = *msg.ResponseId
					}
					_ = msg.Usage.Input
					_ = msg.Usage.TotalTokens
					_ = msg.StopReason
					_ = msg.DurationMs
					for _, blk := range msg.Content {
						switch b := blk.(type) {
						case *ai.TextContent:
							_ = b.Text
						case *ai.ToolCall:
							if b.Arguments != nil {
								_, _ = b.Arguments.Get("path")
							}
						}
					}
					for _, d := range msg.Diagnostics {
						if d.Details != nil {
							_, _ = d.Details.Get("step")
						}
					}
					if msg.Deferred != nil {
						if v, ok := msg.Deferred.Data.Get(); ok {
							if obj, ok := v.(*jsonx.Object); ok {
								_, _ = obj.Get("cursor")
							}
						}
					}
					seenPartials = append(seenPartials, msg)
				}
				for ev := range stream.All() {
					switch e := ev.(type) {
					case *ai.AssistantMessageEventStart:
						inspect(e.Partial)
					case *ai.AssistantMessageEventTextDelta:
						inspect(e.Partial)
					case *ai.AssistantMessageEventToolcallDelta:
						inspect(e.Partial)
					case *ai.AssistantMessageEventToolcallEnd:
						inspect(e.Partial)
						if e.ToolCall != nil && e.ToolCall.Arguments != nil {
							_, _ = e.ToolCall.Arguments.Get("path")
							seenToolEnd = e.ToolCall
						}
					case *ai.AssistantMessageEventDone:
						inspect(e.Message)
					}
				}
			}()

			stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: output})

			respID := fmt.Sprintf("resp_%d", iter)
			output.ResponseId = &respID
			output.Usage.Input = 42
			output.Usage.TotalTokens = 42
			textBlk := &ai.TextContent{Type: "text", Text: "hello"}
			output.Content = append(output.Content, textBlk)
			stream.Push(&ai.AssistantMessageEventTextDelta{Type: "text_delta", ContentIndex: 0, Delta: "hello", Partial: output})

			textBlk.Text = "hello world"
			output.Usage.Output = 17
			output.Usage.TotalTokens = 59
			tc := &ai.ToolCall{Type: "toolCall", Id: "call_1", Name: "read", Arguments: jsonx.ObjectOf("path", "a.txt")}
			output.Content = append(output.Content, tc)
			stream.Push(&ai.AssistantMessageEventToolcallDelta{Type: "toolcall_delta", ContentIndex: 1, Delta: `{"path":"a.txt"}`, Partial: output})

			tc.Arguments.Set("path", "b.txt")
			stream.Push(&ai.AssistantMessageEventToolcallEnd{Type: "toolcall_end", ContentIndex: 1, ToolCall: tc, Partial: output})

			tc.Arguments.Set("path", "mutated_after_toolcall_end.txt")
			respID2 := fmt.Sprintf("resp_%d_final", iter)
			output.ResponseId = &respID2
			output.StopReason = ai.StopReasonToolUse
			output.Diagnostics[0].Details.Set("step", 99)
			if v, ok := output.Deferred.Data.Get(); ok {
				if obj, ok := v.(*jsonx.Object); ok {
					obj.Set("cursor", "99")
				}
			}
			stream.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonToolUse, Message: output})

			// Mutate producer message again concurrently after Done before End.
			output.Usage.Input = 999
			output.ResponseId = nil
			stream.End(output)

			<-consumerDone
			res, err := stream.Result(ctx)
			if err != nil {
				t.Fatalf("Result: %v", err)
			}
			if res.StopReason != ai.StopReasonToolUse || res.Usage.Input != 42 || res.ResponseId == nil || *res.ResponseId != respID2 {
				t.Fatalf("unexpected Result snapshot: stopReason=%q input=%d respId=%v", res.StopReason, res.Usage.Input, res.ResponseId)
			}
			if len(seenPartials) != 5 {
				t.Fatalf("seenPartials = %d, want 5", len(seenPartials))
			}
			if seenPartials[0].StopReason != ai.StopReasonPending || seenPartials[0].ResponseId != nil {
				t.Fatalf("start snapshot was mutated: stopReason=%q respId=%v", seenPartials[0].StopReason, seenPartials[0].ResponseId)
			}
			if seenToolEnd == nil || seenToolEnd.Arguments == nil {
				t.Fatal("expected ToolCall on ToolcallEnd event")
			}
			if gotPath, _ := seenToolEnd.Arguments.GetString("path"); gotPath != "b.txt" {
				t.Fatalf("toolcall_end snapshot mutated: path=%q, want b.txt", gotPath)
			}
		}
	})

	t.Run("End(result) stamps duration in-place and isolates returned Result from later mutations", func(t *testing.T) {
		stream := ai.NewAssistantMessageEventStream()
		output := testEventStreamAssistantMessage(js.DateNow(), nil)
		respID := "resp_end"
		output.ResponseId = &respID
		tc := &ai.ToolCall{Type: "toolCall", Id: "call_end", Name: "bash", Arguments: jsonx.ObjectOf("command", "ls")}
		output.Content = append(output.Content, tc)

		stream.End(output)
		if output.DurationMs == nil {
			t.Fatal("expected End(output) to stamp output.DurationMs in-place")
		}
		output.ResponseId = nil
		tc.Arguments.Set("command", "rm -rf /mutated")

		res, err := stream.Result(ctx)
		if err != nil {
			t.Fatalf("Result: %v", err)
		}
		if res.ResponseId == nil || *res.ResponseId != respID {
			t.Fatalf("Result.ResponseId = %v, want %q", res.ResponseId, respID)
		}
		gotTC := res.Content[0].(*ai.ToolCall)
		if cmd, _ := gotTC.Arguments.GetString("command"); cmd != "ls" {
			t.Fatalf("Result toolCall mutated: command=%q, want ls", cmd)
		}
	})
}
