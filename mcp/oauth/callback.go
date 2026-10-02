// Ported from packages/mcp/src/oauth/callback.ts (pi v1.0.0).

package oauth

import (
	"net/http"
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
	panic("unported: UnmarshalOAuthCallbackPage")
}

// DecodeOAuthCallbackPage decodes a callback page from a jsonx value.
func DecodeOAuthCallbackPage(v any) (OAuthCallbackPage, error) {
	panic("unported: DecodeOAuthCallbackPage")
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
	TimeoutMs    *int64
	// RenderPage renders the browser page as HTML. Nil means a plain-text message.
	RenderPage func(page OAuthCallbackPage) string
}

// callPending is one WaitForCallback waiter.
type callPending struct {
	resolve func(*OAuthCallback)
	reject  func(error)
	timer   *time.Timer
}

// OAuthCallbackServer accepts one OAuth redirect on a loopback port.
type OAuthCallbackServer struct {
	mu          sync.Mutex
	RedirectUrl string
	server      *http.Server
	path        string
	timeoutMs   int64
	renderPage  func(page OAuthCallbackPage) string
	pending     *omap.Map[string, *callPending]
}

// OAuthCallbackServerListen is OAuthCallbackServer.listen. Nil options use the TS defaults.
func OAuthCallbackServerListen(options *OAuthCallbackServerOptions) (*OAuthCallbackServer, error) {
	panic("unported: OAuthCallbackServerListen")
}

// WaitForCallback waits for the redirect that carries state.
func (s *OAuthCallbackServer) WaitForCallback(state string) (*OAuthCallback, error) {
	panic("unported: OAuthCallbackServer.WaitForCallback")
}

// Close rejects pending waits and stops the listener.
func (s *OAuthCallbackServer) Close() error {
	panic("unported: OAuthCallbackServer.Close")
}
