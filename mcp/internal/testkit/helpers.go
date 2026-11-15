// Ported from packages/mcp/test/helpers.ts (pi v1.5.2).

package testkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// HttpHandler handles one test HTTP request given the bound server origin.
type HttpHandler func(w http.ResponseWriter, r *http.Request, origin string)

var (
	helpMu      sync.Mutex
	helpServers []*httptest.Server
)

// ReadBody reads and closes r.Body as UTF-8 text.
func ReadBody(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Listen starts a loopback HTTP test server and returns its origin URL.
// Servers started inside a test are closed automatically on t.Cleanup and may
// also be closed eagerly via CloseServers.
func Listen(t testing.TB, handler HttpHandler) string {
	t.Helper()
	var (
		mu     sync.RWMutex
		origin string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		orig := origin
		mu.RUnlock()
		handler(w, r, orig)
	}))
	mu.Lock()
	origin = srv.URL
	mu.Unlock()

	helpMu.Lock()
	helpServers = append(helpServers, srv)
	helpMu.Unlock()

	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return origin
}

// CloseServers forcefully closes all servers registered via Listen.
func CloseServers() {
	helpMu.Lock()
	srvs := helpServers
	helpServers = nil
	helpMu.Unlock()
	for _, srv := range srvs {
		srv.CloseClientConnections()
		srv.Close()
	}
}
