// Ported from packages/ai/src/utils/node-http-proxy.ts (pi v1.5.2).

package ai

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// UnsupportedProxyProtocolMessage is returned when proxy env resolves to a non-HTTP(S) scheme.
const UnsupportedProxyProtocolMessage = "Unsupported proxy protocol. SOCKS and PAC proxy URLs are not supported; use an HTTP or HTTPS proxy URL."

var unhpDefaultProxyPorts = map[string]int{
	"ftp":    21,
	"gopher": 70,
	"http":   80,
	"https":  443,
	"ws":     80,
	"wss":    443,
}

var (
	unhpClientMu   sync.Mutex
	unhpTransport  = unhpNewTransport(90 * time.Second)
	unhpHTTPClient = &http.Client{Transport: unhpTransport}
)

func unhpNewTransport(idleTimeout time.Duration) *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.IdleConnTimeout = idleTimeout
	base.Proxy = func(req *http.Request) (*url.URL, error) {
		if req == nil || req.URL == nil {
			return nil, nil
		}
		return ResolveHttpProxyUrlForTarget(req.URL.String(), nil)
	}
	return base
}

// NodeHttpProxyDefaultClient returns the shared process-wide HTTP client with env proxy resolution (PORTING.md §10).
func NodeHttpProxyDefaultClient() *http.Client {
	unhpClientMu.Lock()
	c := unhpHTTPClient
	unhpClientMu.Unlock()
	return c
}

// NodeHttpProxyConfigure updates the process-wide HTTP client's idle connection timeout in milliseconds (PORTING.md §10).
func NodeHttpProxyConfigure(idleTimeoutMs int64) {
	d := time.Duration(idleTimeoutMs) * time.Millisecond
	if d < 0 {
		d = 0
	}
	unhpClientMu.Lock()
	unhpTransport = unhpNewTransport(d)
	unhpHTTPClient = &http.Client{Transport: unhpTransport}
	unhpClientMu.Unlock()
}

func unhpGetProxyEnv(key string, env ProviderEnv) string {
	lower := strings.ToLower(key)
	upper := strings.ToUpper(key)
	if env != nil {
		if v := env[lower]; v != "" {
			return v
		}
		if v := env[upper]; v != "" {
			return v
		}
	}
	if v := GetProviderEnvValue(lower, nil); v != "" {
		return v
	}
	return GetProviderEnvValue(upper, nil)
}

func unhpStripBrackets(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

type unhpNoProxyEntry struct {
	host string
	port int
}

func unhpParseNoProxyEntry(entry string) (unhpNoProxyEntry, bool) {
	trimmed := strings.ToLower(js.Trim(entry))
	if trimmed == "" {
		return unhpNoProxyEntry{}, false
	}
	if strings.HasPrefix(trimmed, "[") {
		if closing := strings.IndexByte(trimmed, ']'); closing != -1 {
			host := trimmed[1:closing]
			rest := trimmed[closing+1:]
			if strings.HasPrefix(rest, ":") {
				if p, err := strconv.Atoi(rest[1:]); err == nil {
					return unhpNoProxyEntry{host: host, port: p}, true
				}
				return unhpNoProxyEntry{host: host, port: 0}, true
			}
			return unhpNoProxyEntry{host: host, port: 0}, true
		}
	}
	if strings.Count(trimmed, ":") > 1 {
		return unhpNoProxyEntry{host: trimmed, port: 0}, true
	}
	if colon := strings.LastIndexByte(trimmed, ':'); colon != -1 && colon == strings.IndexByte(trimmed, ':') {
		if p, err := strconv.Atoi(trimmed[colon+1:]); err == nil {
			return unhpNoProxyEntry{host: trimmed[:colon], port: p}, true
		}
	}
	return unhpNoProxyEntry{host: trimmed, port: 0}, true
}

func unhpShouldProxyHostname(hostname string, port int, env ProviderEnv) bool {
	noProxy := strings.ToLower(unhpGetProxyEnv("no_proxy", env))
	if noProxy == "" {
		return true
	}
	if noProxy == "*" {
		return false
	}
	normalizedHost := unhpStripBrackets(strings.ToLower(hostname))
	for _, entry := range strings.FieldsFunc(noProxy, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
	}) {
		parsed, ok := unhpParseNoProxyEntry(entry)
		if !ok {
			continue
		}
		if parsed.port != 0 && parsed.port != port {
			continue
		}
		domain := unhpStripBrackets(parsed.host)
		if strings.HasPrefix(domain, "*.") {
			domain = domain[2:]
		} else if strings.HasPrefix(domain, ".") || strings.HasPrefix(domain, "*") {
			domain = domain[1:]
		}
		if domain == "" {
			continue
		}
		if normalizedHost == domain || strings.HasSuffix(normalizedHost, "."+domain) {
			return false
		}
	}
	return true
}

func unhpGetProxyForUrl(targetUrl string, env ProviderEnv) string {
	parsedUrl, err := url.Parse(targetUrl)
	if err != nil || parsedUrl.Scheme == "" || parsedUrl.Host == "" {
		return ""
	}
	protocol := strings.ToLower(parsedUrl.Scheme)
	hostname := unhpStripBrackets(parsedUrl.Hostname())
	port := 0
	if pStr := parsedUrl.Port(); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil {
			port = p
		}
	}
	if port == 0 {
		port = unhpDefaultProxyPorts[protocol]
	}
	if !unhpShouldProxyHostname(hostname, port, env) {
		return ""
	}
	proxy := unhpGetProxyEnv(protocol+"_proxy", env)
	if proxy == "" {
		proxy = unhpGetProxyEnv("all_proxy", env)
	}
	if proxy != "" && !strings.Contains(proxy, "://") {
		proxy = protocol + "://" + proxy
	}
	return proxy
}

// ResolveHttpProxyUrlForTarget resolves an HTTP or HTTPS proxy URL for targetUrl using env overrides
// and standard proxy environment variables. Returns (nil, nil) when no proxy applies.
func ResolveHttpProxyUrlForTarget(targetUrl string, env ProviderEnv) (*url.URL, error) {
	proxy := unhpGetProxyForUrl(targetUrl, env)
	if proxy == "" {
		return nil, nil
	}
	proxyUrl, err := url.Parse(proxy)
	if err != nil || proxyUrl.Scheme == "" || (proxyUrl.Host == "" && proxyUrl.Opaque == "") {
		errMsg := "invalid URL"
		if err != nil {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("Invalid proxy URL %s: %s", jsonx.MustStringify(proxy), errMsg)
	}
	scheme := strings.ToLower(proxyUrl.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%s Got %s:", UnsupportedProxyProtocolMessage, scheme)
	}
	return proxyUrl, nil
}
