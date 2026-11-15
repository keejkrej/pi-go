// Ported from packages/mcp/src/oauth/callback.ts (pi v1.5.2).

package oauth

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// OAuthCallback is a successful authorization redirect.
type OAuthCallback struct {
	Code  string  `json:"code"`
	State string  `json:"state"`
	Iss   *string `json:"iss,omitzero"`
}

// OAuthCallbackPage is the browser page after the redirect.
// {ok:true} is *OAuthCallbackPageOk. {ok:false} is *OAuthCallbackPageFailed.
type OAuthCallbackPage interface {
	isOAuthCallbackPage()
}

// OAuthCallbackPageOk is a successful callback page.
type OAuthCallbackPageOk struct {
	Ok bool `json:"ok"`
}

func (*OAuthCallbackPageOk) isOAuthCallbackPage() {}

// OAuthCallbackPageFailed is a failed callback page.
type OAuthCallbackPageFailed struct {
	Ok      bool    `json:"ok"`
	Message string  `json:"message"`
	Details *string `json:"details,omitzero"`
}

func (*OAuthCallbackPageFailed) isOAuthCallbackPage() {}

// OAuthCallbackPageUnknown is an unrecognized callback page, kept verbatim.
type OAuthCallbackPageUnknown struct {
	Raw *jsonx.Object
}

func (*OAuthCallbackPageUnknown) isOAuthCallbackPage() {}

// UnmarshalOAuthCallbackPage decodes a callback page from JSON bytes.
func UnmarshalOAuthCallbackPage(data []byte) (OAuthCallbackPage, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeOAuthCallbackPage(v)
}

// DecodeOAuthCallbackPage decodes a callback page from a jsonx value.
func DecodeOAuthCallbackPage(v any) (OAuthCallbackPage, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("Invalid callback page")
	}
	okVal, hasOk := obj.Get("ok")
	if hasOk {
		if b, isBool := okVal.(bool); isBool {
			if b {
				return &OAuthCallbackPageOk{Ok: true}, nil
			}
			var failed OAuthCallbackPageFailed
			if err := jsonx.Decode(obj, &failed); err != nil {
				return nil, err
			}
			return &failed, nil
		}
	}
	return &OAuthCallbackPageUnknown{Raw: obj}, nil
}

// OAuthCallbackServerOptions are the options of OAuthCallbackServerListen.
type OAuthCallbackServerOptions struct {
	// Host is the address to listen on. Default: 127.0.0.1.
	Host *string
	// RedirectHost is the host name in RedirectUrl, for example localhost for a client registered
	// with it while listening on 127.0.0.1. Default: Host.
	RedirectHost *string
	Port         *int
	Path         *string
	// ExtraPaths lists additional callback paths (for server-specific redirect URIs).
	ExtraPaths []string
	TimeoutMs  *int64
	// RenderPage renders the browser page as HTML. Nil means a plain-text message.
	RenderPage func(page OAuthCallbackPage) string
}

type callPending struct {
	resolve func(*OAuthCallback)
	reject  func(error)
	timer   *time.Timer
	path    *string
}

type callResult struct {
	callback *OAuthCallback
	err      error
}

// OAuthCallbackServer accepts one OAuth redirect on a loopback port.
type OAuthCallbackServer struct {
	mu          sync.Mutex
	RedirectUrl string
	server      *http.Server
	paths       []string
	timeoutMs   int64
	renderPage  func(page OAuthCallbackPage) string
	pending     *omap.Map[string, *callPending]
	closed      bool
}

// OAuthCallbackServerListen is OAuthCallbackServer.listen. Nil options use the TS defaults.
func OAuthCallbackServerListen(options *OAuthCallbackServerOptions) (*OAuthCallbackServer, error) {
	host := "127.0.0.1"
	redirectHost := ""
	port := 0
	path := "/callback"
	timeoutMs := int64(5 * 60_000)
	var extraPaths []string
	var renderPage func(page OAuthCallbackPage) string
	if options != nil {
		if options.Host != nil {
			host = *options.Host
		}
		if options.RedirectHost != nil {
			redirectHost = *options.RedirectHost
		}
		if options.Port != nil {
			port = *options.Port
		}
		if options.Path != nil {
			path = *options.Path
		}
		extraPaths = options.ExtraPaths
		if options.TimeoutMs != nil {
			timeoutMs = *options.TimeoutMs
		}
		renderPage = options.RenderPage
	}
	if redirectHost == "" {
		redirectHost = host
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("OAuth callback server did not bind to TCP")
	}

	hostForUrl := redirectHost
	if strings.Contains(hostForUrl, ":") && !strings.HasPrefix(hostForUrl, "[") {
		hostForUrl = "[" + hostForUrl + "]"
	}
	redirectUrl := fmt.Sprintf("http://%s:%d%s", hostForUrl, tcpAddr.Port, path)

	paths := make([]string, 0, 1+len(extraPaths))
	paths = append(paths, path)
	paths = append(paths, extraPaths...)

	inst := &OAuthCallbackServer{
		RedirectUrl: redirectUrl,
		paths:       paths,
		timeoutMs:   timeoutMs,
		renderPage:  renderPage,
		pending:     omap.NewMap[string, *callPending](),
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(inst.callHandle),
	}
	inst.server = srv
	go func() {
		_ = srv.Serve(ln)
	}()
	return inst, nil
}

// WaitForCallback waits for the redirect that carries state.
func (s *OAuthCallbackServer) WaitForCallback(state string) (*OAuthCallback, error) {
	return s.callWaitForCallback(state, nil)
}

// WaitForCallbackOnPath waits for the redirect carrying state on exact path (RFC 9700 section 4.4.2.2).
func (s *OAuthCallbackServer) WaitForCallbackOnPath(state string, path string) (*OAuthCallback, error) {
	return s.callWaitForCallback(state, &path)
}

func (s *OAuthCallbackServer) callWaitForCallback(state string, expectedPath *string) (*OAuthCallback, error) {
	ch := make(chan callResult, 1)
	var once sync.Once
	resolve := func(cb *OAuthCallback) {
		once.Do(func() {
			ch <- callResult{callback: cb}
		})
	}
	reject := func(err error) {
		once.Do(func() {
			ch <- callResult{err: err}
		})
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("OAuth callback server closed")
	}
	if _, exists := s.pending.Get(state); exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("OAuth state is already pending")
	}
	var p *callPending
	timer := time.AfterFunc(time.Duration(s.timeoutMs)*time.Millisecond, func() {
		s.mu.Lock()
		cur, ok := s.pending.Get(state)
		if ok && cur == p {
			s.pending.Delete(state)
		}
		s.mu.Unlock()
		reject(fmt.Errorf("OAuth callback timed out"))
	})
	p = &callPending{
		resolve: resolve,
		reject:  reject,
		timer:   timer,
		path:    expectedPath,
	}
	s.pending.Set(state, p)
	s.mu.Unlock()

	res := <-ch
	return res.callback, res.err
}

// Close rejects pending waits and stops the listener.
func (s *OAuthCallbackServer) Close() error {
	s.mu.Lock()
	s.closed = true
	var waiters []*callPending
	for _, p := range s.pending.All() {
		waiters = append(waiters, p)
	}
	s.pending.Clear()
	s.mu.Unlock()

	for _, p := range waiters {
		p.timer.Stop()
		p.reject(fmt.Errorf("OAuth callback server closed"))
	}
	return s.server.Close()
}

func callPlainText(page OAuthCallbackPage) string {
	switch p := page.(type) {
	case *OAuthCallbackPageOk:
		return "Authorization complete. You may close this window."
	case *OAuthCallbackPageFailed:
		if p.Details != nil && *p.Details != "" {
			return p.Message + "\n\n" + *p.Details
		}
		return p.Message
	default:
		return ""
	}
}

func (s *OAuthCallbackServer) callReply(w http.ResponseWriter, status int, page OAuthCallbackPage) {
	if s.renderPage != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(s.renderPage(page)))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(callPlainText(page)))
}

func (s *OAuthCallbackServer) callHandle(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Path
	if !slices.Contains(s.paths, reqPath) {
		s.callReply(w, http.StatusNotFound, &OAuthCallbackPageFailed{Ok: false, Message: "Not found"})
		return
	}
	q := r.URL.Query()
	state := q.Get("state")

	s.mu.Lock()
	var pending *callPending
	if state != "" {
		pending, _ = s.pending.Get(state)
		if pending != nil {
			s.pending.Delete(state)
		}
	}
	s.mu.Unlock()

	if state == "" || pending == nil {
		s.callReply(w, http.StatusBadRequest, &OAuthCallbackPageFailed{Ok: false, Message: "Invalid or expired OAuth state"})
		return
	}
	pending.timer.Stop()

	if pending.path != nil && reqPath != *pending.path {
		pending.reject(fmt.Errorf("The authorization response arrived on another redirect URI"))
		s.callReply(w, http.StatusBadRequest, &OAuthCallbackPageFailed{Ok: false, Message: "Unexpected redirect URI"})
		return
	}
	if errParam := q.Get("error"); errParam != "" {
		desc := q.Get("error_description")
		if desc == "" {
			desc = errParam
		}
		pending.reject(fmt.Errorf("%s", desc))
		s.callReply(w, http.StatusOK, &OAuthCallbackPageFailed{
			Ok:      false,
			Message: "Authorization failed. You may close this window.",
			Details: &desc,
		})
		return
	}
	code := q.Get("code")
	if code == "" {
		pending.reject(fmt.Errorf("OAuth callback did not include an authorization code"))
		s.callReply(w, http.StatusBadRequest, &OAuthCallbackPageFailed{Ok: false, Message: "Missing authorization code"})
		return
	}
	var issPtr *string
	if iss := q.Get("iss"); iss != "" {
		issPtr = &iss
	}
	pending.resolve(&OAuthCallback{Code: code, State: state, Iss: issPtr})
	s.callReply(w, http.StatusOK, &OAuthCallbackPageOk{Ok: true})
}
