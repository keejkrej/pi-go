// Ported from packages/ai/src/providers/opencode-headers.ts (pi v1.5.2).

package providers

import (
	"context"
	"reflect"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/omap"
)

const ohOpencodeSessionHeader = "x-opencode-session"

func ohHasHeader(headers ai.ProviderHeaders, name string) bool {
	if headers == nil {
		return false
	}
	expected := strings.ToLower(name)
	for _, key := range headers.Keys() {
		if strings.ToLower(key) == expected {
			return true
		}
	}
	return false
}

func ohMergedHeaders(headers ai.ProviderHeaders, sessionId string) ai.ProviderHeaders {
	out := omap.NewMap[string, *string]()
	if headers != nil {
		for k, v := range headers.All() {
			out.Set(k, v)
		}
	}
	sid := sessionId
	out.Set(ohOpencodeSessionHeader, &sid)
	return out
}

func ohWithSessionHeader(options ai.ProviderStreamOptions) ai.ProviderStreamOptions {
	if options == nil {
		return nil
	}
	base := options.BaseStreamOptions()
	if base == nil || base.SessionId == nil || *base.SessionId == "" || ohHasHeader(base.Headers, ohOpencodeSessionHeader) {
		return options
	}
	merged := ohMergedHeaders(base.Headers, *base.SessionId)
	rv := reflect.ValueOf(options)
	if rv.Kind() == reflect.Ptr && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct {
		copied := reflect.New(rv.Elem().Type())
		copied.Elem().Set(rv.Elem())
		if copiedOpts, ok := copied.Interface().(ai.ProviderStreamOptions); ok {
			if cpBase := copiedOpts.BaseStreamOptions(); cpBase != nil {
				cpBase.Headers = merged
			}
			return copiedOpts
		}
	}
	cp := *base
	cp.Headers = merged
	return &cp
}

func ohWithSessionHeaderSimple(options *ai.SimpleStreamOptions) *ai.SimpleStreamOptions {
	if options == nil || options.SessionId == nil || *options.SessionId == "" || ohHasHeader(options.Headers, ohOpencodeSessionHeader) {
		return options
	}
	cp := *options
	cp.Headers = ohMergedHeaders(options.Headers, *options.SessionId)
	return &cp
}

// WithOpenCodeSessionHeader adds OpenCode's required per-conversation routing header before API dispatch.
func WithOpenCodeSessionHeader(streams ai.ProviderStreams) ai.ProviderStreams {
	out := streams
	if streams.Stream != nil {
		out.Stream = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			return streams.Stream(ctx, model, transcript, ohWithSessionHeader(options))
		}
	}
	if streams.StreamSimple != nil {
		out.StreamSimple = func(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return streams.StreamSimple(ctx, model, transcript, ohWithSessionHeaderSimple(options))
		}
	}
	return out
}
