// Ported from packages/ai/src/api/lazy.ts (pi v1.5.2).

package ai

import (
	"context"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
)

// LazyStreamObserver is a synchronous, read-only observation hook for LazyStream forwarding.
type LazyStreamObserver interface {
	OnEvent(event AssistantMessageEvent)
	OnResult(result *AssistantMessage)
}

// LazyApiCapabilities describes optional deferred capabilities exposed by a lazily loaded API module.
type LazyApiCapabilities struct {
	FetchDeferred  bool `json:"fetchDeferred,omitzero"`
	CancelDeferred bool `json:"cancelDeferred,omitzero"`
}

func alCreateSetupErrorMessage(model *Model, err error, timestamp int64, stopReason StopReason) *AssistantMessage {
	var apiName, provider, modelId string
	if model != nil {
		apiName = model.Api
		provider = string(model.Provider)
		modelId = model.Id
	}
	errMsg := FormatThrownValue(err)
	return &AssistantMessage{
		Role:         "assistant",
		Content:      ContentList{},
		Api:          Api(apiName),
		Provider:     ProviderId(provider),
		Model:        modelId,
		Usage:        Usage{},
		StopReason:   stopReason,
		ErrorMessage: &errMsg,
		Timestamp:    timestamp,
	}
}

// LazyStream returns an AssistantMessageEventStream synchronously while running async
// setup (auth resolution, lazy initialization) in a background goroutine. Setup failures
// terminate the stream with an AssistantMessageEventError.
func LazyStream(
	model *Model,
	setup func() (*AssistantMessageEventStream, error),
	observer LazyStreamObserver,
	ctx context.Context,
) *AssistantMessageEventStream {
	startedAt := js.DateNow()
	outer := NewAssistantMessageEventStream()
	observedResult := false
	observeResult := func(result *AssistantMessage) {
		if observedResult {
			return
		}
		observedResult = true
		if observer != nil {
			observer.OnResult(result)
		}
	}

	fail := func(err error) {
		reason := StopReasonError
		if ctx != nil && ctx.Err() != nil {
			reason = StopReasonAborted
		}
		msg := alCreateSetupErrorMessage(model, err, startedAt, reason)
		event := &AssistantMessageEventError{
			Type:   "error",
			Reason: reason,
			Error:  msg,
		}
		outer.Push(event)
		if observer != nil {
			observer.OnEvent(event)
		}
		outer.End(msg)
		observeResult(msg)
	}

	go func() {
		if setup == nil {
			fail(fmt.Errorf("lazy stream setup is nil"))
			return
		}
		inner, err := setup()
		if err != nil {
			fail(err)
			return
		}
		if inner == nil {
			fail(fmt.Errorf("lazy stream setup returned nil stream"))
			return
		}

		var res *AssistantMessage
		for event := range inner.All() {
			outer.Push(event)
			if observer != nil {
				observer.OnEvent(event)
			}
			switch e := event.(type) {
			case *AssistantMessageEventDone:
				res = e.Message
			case *AssistantMessageEventError:
				res = e.Error
			}
		}
		if finalMsg, err := inner.Result(context.Background()); err == nil && finalMsg != nil {
			res = finalMsg
		}
		outer.End(res)
		observeResult(res)
	}()

	return outer
}

// LazyApi wraps a lazily loaded ProviderStreams factory into a ProviderStreams value.
// Layer L1 stays free of ai/api imports because callers supply load().
func LazyApi(load func() (ProviderStreams, error), capabilities *LazyApiCapabilities) ProviderStreams {
	var (
		mu     sync.Mutex
		loaded bool
		cached ProviderStreams
	)
	getImpl := func() (ProviderStreams, error) {
		mu.Lock()
		defer mu.Unlock()
		if loaded {
			return cached, nil
		}
		if load == nil {
			return ProviderStreams{}, fmt.Errorf("lazy api loader is nil")
		}
		impl, err := load()
		if err != nil {
			return ProviderStreams{}, err
		}
		cached = impl
		loaded = true
		return cached, nil
	}

	api := ProviderStreams{
		Stream: func(ctx context.Context, model *Model, c *TranscriptContext, options ProviderStreamOptions) *AssistantMessageEventStream {
			return LazyStream(model, func() (*AssistantMessageEventStream, error) {
				impl, err := getImpl()
				if err != nil {
					return nil, err
				}
				if impl.Stream == nil {
					return nil, fmt.Errorf("API does not implement Stream")
				}
				return impl.Stream(ctx, model, c, options), nil
			}, nil, ctx)
		},
		StreamSimple: func(ctx context.Context, model *Model, c *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
			return LazyStream(model, func() (*AssistantMessageEventStream, error) {
				impl, err := getImpl()
				if err != nil {
					return nil, err
				}
				if impl.StreamSimple == nil {
					return nil, fmt.Errorf("API does not implement StreamSimple")
				}
				return impl.StreamSimple(ctx, model, c, options), nil
			}, nil, ctx)
		},
	}

	if capabilities != nil && capabilities.FetchDeferred {
		api.FetchDeferred = func(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream {
			return LazyStream(model, func() (*AssistantMessageEventStream, error) {
				impl, err := getImpl()
				if err != nil {
					return nil, err
				}
				if impl.FetchDeferred == nil {
					return nil, fmt.Errorf("API does not support deferred responses")
				}
				return impl.FetchDeferred(ctx, model, handle, options), nil
			}, nil, ctx)
		}
	}

	if capabilities != nil && capabilities.CancelDeferred {
		api.CancelDeferred = func(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error {
			impl, err := getImpl()
			if err != nil {
				return err
			}
			if impl.CancelDeferred == nil {
				return fmt.Errorf("API cannot cancel deferred responses")
			}
			return impl.CancelDeferred(ctx, model, handle, options)
		}
	}

	return api
}
