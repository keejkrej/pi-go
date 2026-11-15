package ai_test

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/keejkrej/pi-go/ai"
)

func retryAssistantMsg(text string, stopReason ai.StopReason, errMsg string) *ai.AssistantMessage {
	var content ai.ContentList
	if text != "" {
		content = ai.ContentList{&ai.TextContent{Type: "text", Text: text}}
	} else {
		content = ai.ContentList{}
	}
	var errPtr *string
	if errMsg != "" {
		e := errMsg
		errPtr = &e
	}
	if stopReason == "" {
		stopReason = ai.StopReasonStop
	}
	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      content,
		Api:          "test-api",
		Provider:     "test-provider",
		Model:        "test-model",
		StopReason:   stopReason,
		ErrorMessage: errPtr,
	}
}

func TestProviderRetryClassification(t *testing.T) {
	openAIExplicitRetryMessage := "An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID req_******** in your message."
	bedrockExplicitRetryMessage := `{"message":"The system encountered an unexpected error during processing. Try your request again."}`
	nvidiaNIMResourceExhaustedMessage := "ResourceExhausted: Worker local total request limit reached (288/48)"
	bunFetchSocketClosedMessage := "The socket connection was closed unexpectedly. For more information, pass `verbose: true` in the second argument to fetch()"
	openAIResponsesEarlyEofMessage := "OpenAI Responses stream ended before a terminal response event"
	wrappedDnsLookupError := "The pending stream has been canceled (caused by: getaddrinfo ENOTFOUND bedrock-runtime.us-east-1.amazonaws.com)"
	azurePeakLoadError := "The system is currently experiencing high demand and cannot process your request. Your request exceeds the maximum usage size allowed during peak load. For improved capacity reliability, consider switching to Provisioned Throughput."

	retryableCases := []string{
		openAIExplicitRetryMessage,
		bedrockExplicitRetryMessage,
		nvidiaNIMResourceExhaustedMessage,
		bunFetchSocketClosedMessage,
		"Error: exceeded request buffer limit while retrying upstream",
		wrappedDnsLookupError,
		"connect ENOTFOUND api.example.com",
		"EAI_AGAIN api.example.com",
		"getaddrinfo failed for api.example.com",
		"read ECONNRESET",
		"connect ETIMEDOUT 140.82.113.3:443",
		"write EPIPE",
		"connect ECONNREFUSED 127.0.0.1:443",
		"ECONNABORTED",
		"Connection reset by peer",
		"The pending stream has been canceled",
		"The pending stream has been canceled (caused by: socket closed)",
		"The service is temporarily unavailable. Please retry later.",
		"Service unavailable: The service is temporarily unavailable.",
		"request to https://oauth2.googleapis.com/token failed, reason:",
		"request to https://oauth2.googleapis.com/token failed, reason: connect ECONNREFUSED 127.0.0.1:9",
		"unknown: Unable to forward upstream stream",
		"upstream_error: Upstream request failed",
		"upstream_stream_read_error: Upstream response stream was interrupted",
		"Upstream HTTP/2 stream failed",
		"Claude Code's stream from Anthropic was cut short after the reply had begun",
		`{"type":"error","error":{"type":"api_error","message":"Connection closed."}}`,
		openAIResponsesEarlyEofMessage,
		azurePeakLoadError,
		"subscription_sharing_usage_unavailable: Usage cannot be checked.",
		"subscription_sharing_user_unavailable: User cannot be loaded.",
		"overloaded_error",
		"520 status code (no body)",
		"524 status code (no body)",
	}
	for _, errMsg := range retryableCases {
		msg := retryAssistantMsg("", ai.StopReasonError, errMsg)
		if !ai.IsRetryableAssistantError(msg) {
			t.Errorf("expected retryable error for %q", errMsg)
		}
	}

	nonRetryableCases := []string{
		"Invalid value EXECONNRESET",
		"Unknown field ETIMEDOUTX",
		"Invalid key ERR_EPIPE_ID",
		"429 quota exceeded",
		`OpenAI API error (429): {"code":"subscription_sharing_usage_limit_exceeded","message":"Usage limit reached."}`,
		`OpenAI API error (429): {"code":"subscription_sharing_workplace_individual_usage_limit_exceeded","message":"Usage limit reached."}`,
		`401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"},"request_id":"req_011CU503xyz"}`,
		"400 image exceeds 5 MB maximum: 5316784 bytes > 5242880 bytes",
	}
	for _, errMsg := range nonRetryableCases {
		msg := retryAssistantMsg("", ai.StopReasonError, errMsg)
		if ai.IsRetryableAssistantError(msg) {
			t.Errorf("expected non-retryable error for %q", errMsg)
		}
	}

	if ai.IsRetryableAssistantError(retryAssistantMsg("not an error", ai.StopReasonStop, "")) {
		t.Fatal("expected stop message not to be retryable")
	}
}

func TestRetryDelayMs(t *testing.T) {
	if got := ai.RetryDelayMs(&ai.RetryPolicy{BaseDelayMs: 2000}, 6); got != 60000 {
		t.Fatalf("got %d, want 60000", got)
	}
	max5000 := int64(5000)
	if got := ai.RetryDelayMs(&ai.RetryPolicy{BaseDelayMs: 2000, MaxAgentDelayMs: &max5000}, 5); got != 5000 {
		t.Fatalf("got %d, want 5000", got)
	}
	max0 := int64(0)
	if got := ai.RetryDelayMs(&ai.RetryPolicy{BaseDelayMs: 2000, MaxAgentDelayMs: &max0}, 5); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestRetryAssistantCall(t *testing.T) {
	disabled := &ai.RetryPolicy{Enabled: false, MaxRetries: 3, BaseDelayMs: 0}
	enabled := &ai.RetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 0}

	t.Run("returns a successful response immediately without retrying", func(t *testing.T) {
		var calls int
		res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
			calls++
			return retryAssistantMsg("ok", ai.StopReasonStop, ""), nil
		}, enabled, nil)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(res.Content) != 1 || res.Content[0].(*ai.TextContent).Text != "ok" || calls != 1 {
			t.Fatalf("got calls=%d content=%#v", calls, res.Content)
		}
	})

	t.Run("does not retry an aborted message", func(t *testing.T) {
		var calls, scheduled int
		res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
			calls++
			return retryAssistantMsg("", ai.StopReasonAborted, ""), nil
		}, enabled, &ai.RetryCallbacks{
			OnRetryScheduled: func(int, int, int64, string) { scheduled++ },
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.StopReason != ai.StopReasonAborted || calls != 1 || scheduled != 0 {
			t.Fatalf("got stop=%s calls=%d scheduled=%d", res.StopReason, calls, scheduled)
		}
	})

	t.Run("does not retry a non-retryable error (quota/billing)", func(t *testing.T) {
		var calls, scheduled, finished int
		res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
			calls++
			return retryAssistantMsg("", ai.StopReasonError, "insufficient_quota"), nil
		}, enabled, &ai.RetryCallbacks{
			OnRetryScheduled: func(int, int, int64, string) { scheduled++ },
			OnRetryFinished:  func(bool, int, *string) { finished++ },
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.StopReason != ai.StopReasonError || calls != 1 || scheduled != 0 || finished != 0 {
			t.Fatalf("got stop=%s calls=%d scheduled=%d finished=%d", res.StopReason, calls, scheduled, finished)
		}
	})

	t.Run("retries a transient error up to maxRetries then returns the final error", func(t *testing.T) {
		var calls, scheduled int
		var finSuccess bool
		var finAttempt int
		var finErr *string
		res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
			calls++
			return retryAssistantMsg("", ai.StopReasonError, "terminated"), nil
		}, enabled, &ai.RetryCallbacks{
			OnRetryScheduled: func(int, int, int64, string) { scheduled++ },
			OnRetryFinished: func(success bool, attempt int, finalErr *string) {
				finSuccess, finAttempt, finErr = success, attempt, finalErr
			},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.StopReason != ai.StopReasonError || calls != 4 || scheduled != 3 || finSuccess || finAttempt != 3 || finErr == nil || *finErr != "terminated" {
			t.Fatalf("unexpected result: stop=%s calls=%d scheduled=%d fin=(%v,%d,%v)", res.StopReason, calls, scheduled, finSuccess, finAttempt, finErr)
		}
	})

	t.Run("reports capped retry delays", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			max15 := int64(15)
			policy := &ai.RetryPolicy{Enabled: true, MaxRetries: 4, BaseDelayMs: 10, MaxAgentDelayMs: &max15}
			var n int
			var delays []int64
			res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
				n++
				if n < 5 {
					return retryAssistantMsg("", ai.StopReasonError, "terminated"), nil
				}
				return retryAssistantMsg("recovered", ai.StopReasonStop, ""), nil
			}, policy, &ai.RetryCallbacks{
				OnRetryScheduled: func(_ int, _ int, delayMs int64, _ string) {
					delays = append(delays, delayMs)
				},
			})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if res.StopReason != ai.StopReasonStop {
				t.Fatalf("stop = %s", res.StopReason)
			}
			if !slices.Equal(delays, []int64{10, 15, 15, 15}) {
				t.Fatalf("delays = %v, want [10 15 15 15]", delays)
			}
		})
	})

	t.Run("stops retrying once a call succeeds", func(t *testing.T) {
		var n, calls int
		var finSuccess bool
		var finAttempt int
		res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
			calls++
			n++
			if n < 3 {
				return retryAssistantMsg("", ai.StopReasonError, "terminated"), nil
			}
			return retryAssistantMsg("recovered", ai.StopReasonStop, ""), nil
		}, enabled, &ai.RetryCallbacks{
			OnRetryFinished: func(success bool, attempt int, finalErr *string) {
				finSuccess, finAttempt = success, attempt
				if finalErr != nil {
					t.Errorf("expected nil finalErr, got %q", *finalErr)
				}
			},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.Content[0].(*ai.TextContent).Text != "recovered" || calls != 3 || !finSuccess || finAttempt != 2 {
			t.Fatalf("unexpected state: calls=%d fin=(%v,%d)", calls, finSuccess, finAttempt)
		}
	})

	t.Run("reports an aborted retried call as unsuccessful", func(t *testing.T) {
		var n, calls int
		var finSuccess bool
		var finAttempt int
		var finErr *string
		res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
			calls++
			n++
			if n == 1 {
				return retryAssistantMsg("", ai.StopReasonError, "terminated"), nil
			}
			return retryAssistantMsg("", ai.StopReasonAborted, ""), nil
		}, enabled, &ai.RetryCallbacks{
			OnRetryFinished: func(success bool, attempt int, finalErr *string) {
				finSuccess, finAttempt, finErr = success, attempt, finalErr
			},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.StopReason != ai.StopReasonAborted || calls != 2 || finSuccess || finAttempt != 1 || finErr != nil {
			t.Fatalf("unexpected state: stop=%s calls=%d fin=(%v,%d,%v)", res.StopReason, calls, finSuccess, finAttempt, finErr)
		}
	})

	t.Run("does not retry when policy is disabled", func(t *testing.T) {
		var calls, scheduled, finished int
		res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
			calls++
			return retryAssistantMsg("", ai.StopReasonError, "terminated"), nil
		}, disabled, &ai.RetryCallbacks{
			OnRetryScheduled: func(int, int, int64, string) { scheduled++ },
			OnRetryFinished:  func(bool, int, *string) { finished++ },
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.StopReason != ai.StopReasonError || calls != 1 || scheduled != 0 || finished != 0 {
			t.Fatalf("calls=%d scheduled=%d finished=%d", calls, scheduled, finished)
		}
	})

	t.Run("emits onRetryAttemptStart after backoff before each retried call", func(t *testing.T) {
		var events []string
		var n int
		res, err := ai.RetryAssistantCall(t.Context(), func() (*ai.AssistantMessage, error) {
			events = append(events, "produce:"+string(rune('0'+n)))
			n++
			if n < 3 {
				return retryAssistantMsg("", ai.StopReasonError, "terminated"), nil
			}
			return retryAssistantMsg("recovered", ai.StopReasonStop, ""), nil
		}, enabled, &ai.RetryCallbacks{
			OnRetryScheduled: func(attempt int, _ int, _ int64, _ string) {
				events = append(events, "retry:"+string(rune('0'+attempt)))
			},
			OnRetryAttemptStart: func() {
				events = append(events, "attempt-start")
			},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.Content[0].(*ai.TextContent).Text != "recovered" {
			t.Fatalf("unexpected content: %#v", res.Content)
		}
		want := []string{
			"produce:0",
			"retry:1",
			"attempt-start",
			"produce:1",
			"retry:2",
			"attempt-start",
			"produce:2",
		}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
	})

	t.Run("aborts backoff sleep via signal returns aborted message and emits onRetryFinished(false)", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			var calls atomic.Int32
			var mu sync.Mutex
			var finSuccess bool
			var finAttempt int
			var finErr *string

			type out struct {
				msg *ai.AssistantMessage
				err error
			}
			ch := make(chan out, 1)
			go func() {
				res, err := ai.RetryAssistantCall(ctx, func() (*ai.AssistantMessage, error) {
					calls.Add(1)
					return retryAssistantMsg("", ai.StopReasonError, "terminated"), nil
				}, &ai.RetryPolicy{Enabled: true, MaxRetries: 5, BaseDelayMs: 10000}, &ai.RetryCallbacks{
					OnRetryFinished: func(success bool, attempt int, finalErr *string) {
						mu.Lock()
						defer mu.Unlock()
						finSuccess, finAttempt, finErr = success, attempt, finalErr
					},
				})
				ch <- out{res, err}
			}()

			synctest.Wait()
			if got := calls.Load(); got != 1 {
				t.Fatalf("calls = %d, want 1", got)
			}
			cancel()

			got := <-ch
			if got.err != nil {
				t.Fatalf("unexpected err: %v", got.err)
			}
			if got.msg.StopReason != ai.StopReasonAborted || got.msg.ErrorMessage != nil {
				t.Fatalf("unexpected msg: stop=%s errMsg=%v", got.msg.StopReason, got.msg.ErrorMessage)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1", calls.Load())
			}
			mu.Lock()
			defer mu.Unlock()
			if finSuccess || finAttempt != 1 || finErr == nil || *finErr != "terminated" {
				t.Fatalf("fin=(%v,%d,%v)", finSuccess, finAttempt, finErr)
			}
		})
	})
}
