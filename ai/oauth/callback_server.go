// Ported from packages/ai/src/auth/oauth/callback-server.ts (pi v1.5.2).

package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

// OAuthCallbackServerOptions configures a temporary loopback OAuth redirect server.
type OAuthCallbackServerOptions[T any] struct {
	// ProviderName is shown on the browser result page, for example "OpenAI".
	ProviderName string
	// Host is the address to listen on.
	Host string
	// Port is the TCP port to listen on; 0 selects a free port.
	Port int
	// Path is the expected callback path, for example "/auth/callback".
	Path string
	// RedirectHost overrides Host in RedirectUri when set, for example "localhost".
	RedirectHost *string
	// State is the expected state query parameter. Nil skips state verification.
	State *string
	// Complete finishes the sign-in with the received authorization code before the browser page is sent.
	Complete func(ctx context.Context, code string) (T, error)
	// TimeoutMs fails Wait with "<ProviderName> sign-in timed out" if exceeded.
	TimeoutMs *int64
}

type csResult[T any] struct {
	value T
	ok    bool
	err   error
}

// OAuthCallbackServer listens on a loopback HTTP port for a single OAuth redirect callback.
type OAuthCallbackServer[T any] struct {
	RedirectUri string

	mu      sync.Mutex
	claimed bool
	settled bool
	result  csResult[T]
	done    chan struct{}
	timer   *time.Timer
	stopCtx func() bool
	server  *http.Server
	wg      sync.WaitGroup
}

func csSendPage(w http.ResponseWriter, status int, html string) {
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(html))
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
}

// StartOAuthCallbackServer binds a loopback HTTP server for handling a browser OAuth callback.
func StartOAuthCallbackServer[T any](ctx context.Context, options OAuthCallbackServerOptions[T]) (*OAuthCallbackServer[T], error) {
	if ctx.Err() != nil {
		return nil, errors.New("Login cancelled")
	}

	listenAddr := net.JoinHostPort(options.Host, strconv.Itoa(options.Port))
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, errors.New("OAuth callback server did not bind to TCP")
	}

	redirectHost := options.Host
	if options.RedirectHost != nil {
		redirectHost = *options.RedirectHost
	}
	formattedHost := redirectHost
	if strings.Contains(formattedHost, ":") && !strings.HasPrefix(formattedHost, "[") {
		formattedHost = "[" + formattedHost + "]"
	}
	redirectUri := fmt.Sprintf("http://%s:%d%s", formattedHost, tcpAddr.Port, options.Path)

	s := &OAuthCallbackServer[T]{
		RedirectUri: redirectUri,
		done:        make(chan struct{}),
	}

	var srv *http.Server
	srv = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != options.Path {
				csSendPage(w, http.StatusNotFound, ai.OauthErrorHtml("Callback route not found."))
				return
			}
			query := r.URL.Query()
			if options.State != nil {
				if !query.Has("state") || query.Get("state") != *options.State {
					csSendPage(w, http.StatusBadRequest, ai.OauthErrorHtml("State mismatch."))
					return
				}
			}

			s.mu.Lock()
			if s.claimed || s.settled {
				s.mu.Unlock()
				csSendPage(w, http.StatusConflict, ai.OauthErrorHtml("This sign-in has already been handled."))
				return
			}
			if errCode := query.Get("error"); errCode != "" {
				description := errCode
				if query.Has("error_description") {
					description = query.Get("error_description")
				}
				s.finishLocked(csResult[T]{err: fmt.Errorf("%s authorization failed: %s", options.ProviderName, description)})
				s.mu.Unlock()
				csSendPage(w, http.StatusBadRequest, ai.OauthErrorHtml(fmt.Sprintf("%s authorization failed.", options.ProviderName), description))
				return
			}
			code := query.Get("code")
			if code == "" {
				s.mu.Unlock()
				csSendPage(w, http.StatusBadRequest, ai.OauthErrorHtml("Missing authorization code."))
				return
			}
			s.claimed = true
			s.mu.Unlock()

			value, err := options.Complete(ctx, code)
			if err != nil {
				csSendPage(w, http.StatusBadGateway, ai.OauthErrorHtml(fmt.Sprintf("%s sign-in failed.", options.ProviderName), err.Error()))
				s.finish(csResult[T]{err: err})
				return
			}
			csSendPage(w, http.StatusOK, ai.OauthSuccessHtml(fmt.Sprintf("Signed in to %s. You may now close this page.", options.ProviderName)))
			s.finish(csResult[T]{value: value, ok: true})
		}),
	}
	s.server = srv

	if ctx.Done() != nil {
		s.stopCtx = context.AfterFunc(ctx, func() {
			s.finish(csResult[T]{err: errors.New("Login cancelled")})
		})
	}
	if options.TimeoutMs != nil {
		timeout := time.Duration(*options.TimeoutMs) * time.Millisecond
		if timeout < 0 {
			timeout = 0
		}
		s.mu.Lock()
		if !s.settled {
			s.timer = time.AfterFunc(timeout, func() {
				s.finish(csResult[T]{err: fmt.Errorf("%s sign-in timed out", options.ProviderName)})
			})
		}
		s.mu.Unlock()
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.finish(csResult[T]{err: serveErr})
		}
	}()

	return s, nil
}

func (s *OAuthCallbackServer[T]) finish(res csResult[T]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishLocked(res)
}

func (s *OAuthCallbackServer[T]) finishLocked(res csResult[T]) {
	if s.settled {
		return
	}
	s.settled = true
	s.result = res
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.stopCtx != nil {
		stop := s.stopCtx
		s.stopCtx = nil
		stop()
	}
	close(s.done)
}

// Wait blocks until a callback completes (ok=true), Cancel is called (ok=false, err=nil),
// or the callback fails/times out/is cancelled (err != nil).
func (s *OAuthCallbackServer[T]) Wait(ctx context.Context) (T, bool, error) {
	var zero T
	select {
	case <-s.done:
		return s.result.value, s.result.ok, s.result.err
	case <-ctx.Done():
		// Give any synchronous AfterFunc finish a chance to win deterministically if done is already closed.
		select {
		case <-s.done:
			return s.result.value, s.result.ok, s.result.err
		default:
			return zero, false, errors.New("Login cancelled")
		}
	}
}

// Cancel stops waiting for a browser callback unless a callback is already being completed.
func (s *OAuthCallbackServer[T]) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.claimed {
		s.finishLocked(csResult[T]{ok: false})
	}
}

// Close closes the callback server and rejects any pending Wait.
func (s *OAuthCallbackServer[T]) Close() {
	s.finish(csResult[T]{err: errors.New("OAuth callback server closed")})
	if s.server != nil {
		_ = s.server.Close()
	}
	s.wg.Wait()
}

// CallbackOrManualInputKind identifies whether the user finished via loopback callback or manual prompt.
type CallbackOrManualInputKind string

const (
	CallbackOrManualInputKindCallback CallbackOrManualInputKind = "callback"
	CallbackOrManualInputKindManual   CallbackOrManualInputKind = "manual"
)

// CallbackOrManualInputResult holds the outcome of WaitForCallbackOrManualInput.
type CallbackOrManualInputResult[T any] struct {
	Type  CallbackOrManualInputKind
	Value T
	Input string
}

// ManualInputPrompt configures the fallback manual authorization code prompt.
type ManualInputPrompt struct {
	Message     string
	Placeholder string
}

type csManualResult struct {
	input string
	err   error
}

// WaitForCallbackOrManualInput races the loopback callback server against a manual code prompt.
// Without a callback server, only the manual prompt is used.
func WaitForCallbackOrManualInput[T any](
	ctx context.Context,
	interaction ai.AuthInteraction,
	callback *OAuthCallbackServer[T],
	prompt ManualInputPrompt,
) (CallbackOrManualInputResult[T], error) {
	manualCtx, manualCancel := context.WithCancel(ctx)
	defer manualCancel()

	manualCh := make(chan csManualResult, 1)
	placeholder := prompt.Placeholder
	go func() {
		input, err := interaction.Prompt(manualCtx, &ai.AuthPromptManualCode{
			Type:          ai.AuthPromptKindManualCode,
			Message:       prompt.Message,
			Placeholder:   &placeholder,
			PromptContext: manualCtx,
		})
		manualCh <- csManualResult{input: input, err: err}
		if callback != nil {
			callback.Cancel()
		}
	}()

	if callback != nil {
		value, ok, err := callback.Wait(ctx)
		if err != nil {
			return CallbackOrManualInputResult[T]{}, err
		}
		select {
		case res := <-manualCh:
			if res.err != nil {
				return CallbackOrManualInputResult[T]{}, res.err
			}
			if ok {
				return CallbackOrManualInputResult[T]{
					Type:  CallbackOrManualInputKindCallback,
					Value: value,
				}, nil
			}
			return CallbackOrManualInputResult[T]{
				Type:  CallbackOrManualInputKindManual,
				Input: res.input,
			}, nil
		default:
			if ok {
				return CallbackOrManualInputResult[T]{
					Type:  CallbackOrManualInputKindCallback,
					Value: value,
				}, nil
			}
			res := <-manualCh
			if res.err != nil {
				return CallbackOrManualInputResult[T]{}, res.err
			}
			return CallbackOrManualInputResult[T]{
				Type:  CallbackOrManualInputKindManual,
				Input: res.input,
			}, nil
		}
	}

	res := <-manualCh
	if res.err != nil {
		return CallbackOrManualInputResult[T]{}, res.err
	}
	return CallbackOrManualInputResult[T]{
		Type:  CallbackOrManualInputKindManual,
		Input: res.input,
	}, nil
}
