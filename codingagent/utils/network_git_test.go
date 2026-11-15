package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type netMockCodedError struct {
	code string
	msg  string
}

func (e netMockCodedError) Error() string     { return e.msg }
func (e netMockCodedError) ErrorCode() string { return e.code }

type netMockMultiError struct {
	errs []error
}

func (e netMockMultiError) Error() string   { return "aggregate" }
func (e netMockMultiError) Unwrap() []error { return e.errs }

type netRoundTripFunc func(*http.Request) (*http.Response, error)

func (f netRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func netJsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestManagementHttpFetchWithRetry(t *testing.T) {
	origClient := mhClient
	origSleep := mhSleep
	t.Cleanup(func() {
		mhClient = origClient
		mhSleep = origSleep
	})
	mhSleep = func(context.Context, float64) error { return nil }

	// 1. Retries transient transport failures up to 3 attempts total.
	calls := 0
	mhClient = func() *http.Client {
		return &http.Client{
			Transport: netRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls <= 2 {
					return nil, errors.New("fetch failed")
				}
				return netJsonResponse(200, `{"ok":true}`), nil
			}),
		}
	}
	resp, err := FetchWithRetry(context.Background(), "https://example.test", nil)
	if err != nil || resp.StatusCode != 200 || calls != 3 {
		t.Fatalf("retry calls=%d resp=%v err=%v", calls, resp, err)
	}
	_ = resp.Body.Close()

	// 2. Retries transient 503 HTTP status.
	calls = 0
	mhClient = func() *http.Client {
		return &http.Client{
			Transport: netRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return netJsonResponse(503, "busy"), nil
				}
				return netJsonResponse(200, `{"ok":true}`), nil
			}),
		}
	}
	resp, err = FetchWithRetry(context.Background(), "https://example.test", nil)
	if err != nil || resp.StatusCode != 200 || calls != 2 {
		t.Fatalf("503 retry calls=%d resp=%v err=%v", calls, resp, err)
	}
	_ = resp.Body.Close()

	// 3. Shared timeout deadline across attempts & per-attempt timeout retry.
	var deadlines []string
	calls = 0
	mhClient = func() *http.Client {
		return &http.Client{
			Transport: netRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if d, ok := r.Context().Deadline(); ok {
					deadlines = append(deadlines, d.UTC().Format("15:04:05.000"))
				}
				if calls == 1 {
					return nil, errors.New("fetch failed")
				}
				return netJsonResponse(200, `{"ok":true}`), nil
			}),
		}
	}
	resp, err = FetchWithRetry(context.Background(), "https://example.test", &FetchWithRetryOptions{TimeoutMs: 5000})
	if err != nil || len(deadlines) != 2 || deadlines[0] != deadlines[1] {
		t.Fatalf("shared budget mismatch: deadlines=%v err=%v", deadlines, err)
	}
	_ = resp.Body.Close()

	// 4. Does not retry caller cancellation.
	calls = 0
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FetchWithRetry(canceled, "https://example.test", nil); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("expected context.Canceled without calls, got err=%v calls=%d", err, calls)
	}
}

func TestVersionCheck(t *testing.T) {
	origFetch := vcFetch
	t.Cleanup(func() { vcFetch = origFetch })

	if ComparePackageVersions("0.70.6", "0.70.5") <= 0 ||
		ComparePackageVersions("0.70.5", "0.70.5") != 0 ||
		ComparePackageVersions("0.70.4", "0.70.5") >= 0 ||
		ComparePackageVersions("5.0.0-beta.20", "5.0.0-beta.9") <= 0 ||
		IsNewerPackageVersion("0.70.5", "0.70.5") ||
		!IsNewerPackageVersion("0.70.6", "0.70.5") {
		t.Fatal("ComparePackageVersions/IsNewerPackageVersion failed")
	}

	nested := fmt.Errorf("fetch failed: %w", netMockMultiError{errs: []error{
		netMockCodedError{code: "ETIMEDOUT", msg: "connect timeout"},
		netMockCodedError{code: "ENETUNREACH", msg: "network unreachable"},
	}})
	if got := FormatVersionCheckError(nested); !strings.Contains(got, "(ETIMEDOUT, ENETUNREACH)") {
		t.Fatalf("FormatVersionCheckError = %q", got)
	}

	t.Setenv("PI_SKIP_VERSION_CHECK", "")
	vcFetch = func(_ context.Context, rawURL string, opts *FetchWithRetryOptions) (*http.Response, error) {
		if rawURL != VersionCheckUrl {
			t.Fatalf("unexpected url: %s", rawURL)
		}
		if opts.Headers.Get("Accept") != "application/json" || !strings.HasPrefix(opts.Headers.Get("User-Agent"), "pi/1.2.2 ") {
			t.Fatalf("unexpected headers: %+v", opts.Headers)
		}
		return netJsonResponse(200, `{"packageName":"@new-scope/pi","version":"1.2.4","note":" **Read this** "}`), nil
	}
	rel := CheckForNewPiVersion(context.Background(), "1.2.2")
	if rel == nil || rel.Version != "1.2.4" || rel.Note != "**Read this**" || rel.PackageName != "@new-scope/pi" {
		t.Fatalf("unexpected release: %+v", rel)
	}

	// PI_SKIP_VERSION_CHECK skips automatic CheckForNewPiVersion, not direct GetLatestPiVersion.
	t.Setenv("PI_SKIP_VERSION_CHECK", "1")
	called := 0
	vcFetch = func(context.Context, string, *FetchWithRetryOptions) (*http.Response, error) {
		called++
		return netJsonResponse(200, `{"version":"1.2.4"}`), nil
	}
	if got := CheckForNewPiVersion(context.Background(), "1.2.2"); got != nil || called != 0 {
		t.Fatalf("expected skipped automatic check, got %+v calls=%d", got, called)
	}
	if ver, err := GetLatestPiVersion(context.Background(), "1.2.2", nil); err != nil || ver != "1.2.4" || called != 1 {
		t.Fatalf("expected direct call despite env var: ver=%q err=%v calls=%d", ver, err, called)
	}
}

func TestParseGitUrl(t *testing.T) {
	p := ParseGitUrl("https://github.com/user/repo@v1.0.0")
	if p == nil || p.Host != "github.com" || p.Path != "user/repo" || p.Ref != "v1.0.0" || p.Repo != "https://github.com/user/repo" {
		t.Fatalf("unexpected https git parse: %+v", p)
	}

	s := ParseGitUrl("git:git@github.com:user/repo@v1.0.0")
	if s == nil || s.Host != "github.com" || s.Path != "user/repo" || s.Ref != "v1.0.0" || s.Repo != "git@github.com:user/repo" {
		t.Fatalf("unexpected scp git parse: %+v", s)
	}

	sh := ParseGitUrl("git:github.com/user/repo")
	if sh == nil || sh.Repo != "https://github.com/user/repo" {
		t.Fatalf("unexpected shorthand git parse: %+v", sh)
	}

	for _, bad := range []string{
		"git@github.com:user/repo",
		"github.com/user/repo",
		"user/repo",
		"git:git@evil.example:../../victim/repo",
		"https://evil.example/..%2F..%2Fvictim/repo",
		"https://evil.example/..%2F..%2Fvictim/repo%",
		"git:git@evil.example:/absolute/repo",
		"git:git@evil.example:user\\repo/name",
		"git:git@evil.example:user/repo\x00name",
	} {
		if got := ParseGitUrl(bad); got != nil {
			t.Fatalf("expected nil for %q, got %+v", bad, got)
		}
	}
}

func TestChangelog(t *testing.T) {
	md := strings.Join([]string{
		"[Project Trust](README.md#project-trust)",
		"[Extensions](docs/extensions.md#project_trust)",
		"[Examples](examples/extensions/)",
		"[Root README](../../README.md#supply-chain-hardening)",
	}, "\n")
	want := strings.Join([]string{
		"[Project Trust](https://github.com/earendil-works/pi/blob/v0.79.0/packages/coding-agent/README.md#project-trust)",
		"[Extensions](https://github.com/earendil-works/pi/blob/v0.79.0/packages/coding-agent/docs/extensions.md#project_trust)",
		"[Examples](https://github.com/earendil-works/pi/tree/v0.79.0/packages/coding-agent/examples/extensions/)",
		"[Root README](https://github.com/earendil-works/pi/blob/v0.79.0/README.md#supply-chain-hardening)",
	}, "\n")
	if got := NormalizeChangelogLinksForEntry(md, ChangelogEntry{Major: 0, Minor: 79, Patch: 0}); got != want {
		t.Fatalf("NormalizeChangelogLinksForEntry mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}

	legacy := strings.Join([]string{
		"[#5167](https://github.com/earendil-works/pi-mono/pull/5167)",
		"[#4163](https://github.com/badlogic/pi-mono/issues/4163)",
		"[Agent README](https://github.com/badlogic/pi-mono/blob/main/packages/agent/README.md)",
		"[External](https://example.com/docs)",
		"[Local anchor](#settings)",
	}, "\n")
	wantLegacy := strings.Join([]string{
		"[#5167](https://github.com/earendil-works/pi/pull/5167)",
		"[#4163](https://github.com/earendil-works/pi/issues/4163)",
		"[Agent README](https://github.com/earendil-works/pi/blob/v0.79.0/packages/agent/README.md)",
		"[External](https://example.com/docs)",
		"[Local anchor](#settings)",
	}, "\n")
	if got := NormalizeChangelogLinks(legacy, "0.79.0"); got != wantLegacy {
		t.Fatalf("legacy NormalizeChangelogLinks mismatch:\ngot:\n%s\nwant:\n%s", got, wantLegacy)
	}

	entries := []ChangelogEntry{
		{Major: 0, Minor: 1, Patch: 5, Content: "0.1.5"},
		{Major: 0, Minor: 1, Patch: 4, Content: "0.1.4"},
	}
	if res, ok := GetNewEntries(entries, "0.1.5-20261020.7"); !ok || len(res) != 0 {
		t.Fatalf("expected empty slice with ok=true, got %v %v", res, ok)
	}
	if res, ok := GetNewEntries(entries, "0.1.4-beta.1"); !ok || len(res) != 1 || res[0].Content != "0.1.5" {
		t.Fatalf("expected 0.1.5, got %v %v", res, ok)
	}
	if _, ok := GetNewEntries(entries, "latest"); ok {
		t.Fatal("expected ok=false for non-semver lastVersion")
	}
}

func TestToolsManager(t *testing.T) {
	origFetch := tmFetch
	origPlat := tmPlatform
	origArch := tmArch
	origExists := tmStatExists
	origLookPath := tmLookPath
	t.Cleanup(func() {
		tmFetch = origFetch
		tmPlatform = origPlat
		tmArch = origArch
		tmStatExists = origExists
		tmLookPath = origLookPath
	})

	tmStatExists = func(string) bool { return false }
	tmLookPath = func(string) (string, error) { return "", errors.New("not found") }
	tmPlatLinux := "linux"
	tmPlatform = func() string { return tmPlatLinux }
	tmArch = func() string { return "x64" }

	tmFetch = func(context.Context, string, *FetchWithRetryOptions) (*http.Response, error) {
		return &http.Response{
			StatusCode: 302,
			Header:     http.Header{"Location": {"https://github.com/sharkdp/fd/releases/tag/v10.4.2"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
	ver, err := GetLatestVersion(context.Background(), "sharkdp/fd")
	if err != nil || ver != "10.4.2" {
		t.Fatalf("GetLatestVersion = (%q, %v)", ver, err)
	}

	tmFetch = func(context.Context, string, *FetchWithRetryOptions) (*http.Response, error) {
		return netJsonResponse(404, "not found"), nil
	}
	if _, err := GetLatestVersion(context.Background(), "sharkdp/fd"); err == nil ||
		!strings.Contains(err.Error(), "Failed to resolve latest sharkdp/fd release: HTTP 404 without redirect") {
		t.Fatalf("expected 404 error, got %v", err)
	}

	// Offline mode skips download
	t.Setenv("PI_OFFLINE", "1")
	var statuses []ToolStatus
	if p := EnsureTool(context.Background(), "fd", func(s ToolStatus) { statuses = append(statuses, s) }); p != nil {
		t.Fatalf("expected nil in offline mode, got %v", *p)
	}
	if len(statuses) != 1 || !strings.Contains(statuses[0].Message, "Offline mode enabled") {
		t.Fatalf("unexpected offline statuses: %+v", statuses)
	}

	// Unsupported arm / ia32 emits clear warning without fetching
	t.Setenv("PI_OFFLINE", "")
	tmArch = func() string { return "arm" }
	called := 0
	tmFetch = func(context.Context, string, *FetchWithRetryOptions) (*http.Response, error) {
		called++
		return nil, nil
	}
	statuses = nil
	if p := EnsureTool(context.Background(), "rg", func(s ToolStatus) { statuses = append(statuses, s) }); p != nil || called != 0 {
		t.Fatalf("expected unsupported arch bailout: p=%v called=%d", p, called)
	}
	if len(statuses) != 1 || !strings.Contains(statuses[0].Message, "Automatic download is not supported on linux/arm.") {
		t.Fatalf("unexpected unsupported statuses: %+v", statuses)
	}
}

func TestSyntaxHighlightAndHtmlAndZip(t *testing.T) {
	for _, lang := range []string{
		"python", "java", "go", "javascript", "cpp", "typescript", "php", "ruby",
		"c", "csharp", "nix", "bash", "rust", "scala", "kotlin", "swift",
		"dart", "groovy", "perl", "lua",
	} {
		if !SupportsLanguage(lang) {
			t.Fatalf("expected eager support for language %q", lang)
		}
	}
	if SupportsLanguage("ada") {
		t.Fatal("expected deferred language 'ada' to be false before LoadAllHighlightLanguages")
	}
	eagerCount := GetHighlightLanguageCount()
	LoadAllHighlightLanguages()
	if !SupportsLanguage("ada") || !SupportsLanguage("diff") {
		t.Fatal("expected full language support for 'ada' and 'diff' after LoadAllHighlightLanguages")
	}
	if GetHighlightLanguageCount() <= eagerCount {
		t.Fatalf("expected GetHighlightLanguageCount() > %d after LoadAllHighlightLanguages, got %d", eagerCount, GetHighlightLanguageCount())
	}

	rendered := RenderHighlightedHtml(`<span class="hljs-keyword">const</span> value`, HighlightTheme{
		"keyword": func(s string) string { return "[keyword:" + s + "]" },
	})
	if rendered != "[keyword:const] value" {
		t.Fatalf("RenderHighlightedHtml = %q", rendered)
	}

	decoded := RenderHighlightedHtml("&lt;tag attr=&quot;value&quot;&gt;&amp;#x41;&#65;&lt;/tag&gt;", nil)
	if decoded != `<tag attr="value">&#x41;A</tag>` {
		t.Fatalf("entity decode mismatch: %q", decoded)
	}

	nested := RenderHighlightedHtml(`<span class="hljs-string">a<span class="hljs-subst">${x}</span>b</span>`, HighlightTheme{
		"string": func(s string) string { return "[string:" + s + "]" },
	})
	if nested != "[string:a][string:${x}][string:b]" {
		t.Fatalf("nested scope inheritance mismatch: %q", nested)
	}

	unscoped := RenderHighlightedHtml(`<span class="hljs-string">a<span class="language-xml">b</span>c</span>`, HighlightTheme{
		"string": func(s string) string { return "[string:" + s + "]" },
	})
	if unscoped != "[string:a][string:b][string:c]" {
		t.Fatalf("unscoped span inheritance mismatch: %q", unscoped)
	}

	tsOut := Highlight("const value = 1", HighlightOptions{
		Language:       "typescript",
		IgnoreIllegals: true,
		Theme: HighlightTheme{
			"keyword": func(s string) string { return "[keyword:" + s + "]" },
			"number":  func(s string) string { return "[number:" + s + "]" },
		},
	})
	if !strings.Contains(tsOut, "[keyword:const]") || !strings.Contains(tsOut, "[number:1]") {
		t.Fatalf("Highlight output mismatch: %q", tsOut)
	}

	// Zip create & extract + zip slip rejection
	zipBytes, err := CreateZip([]ZipEntry{{Name: "dir/hello.txt", Data: []byte("world")}})
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "test.zip")
	if err := os.WriteFile(zipPath, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	extractDir := filepath.Join(tmp, "out")
	if err := ExtractZip(zipPath, extractDir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(extractDir, "dir", "hello.txt"))
	if err != nil || !bytes.Equal(got, []byte("world")) {
		t.Fatalf("zip extract mismatch: got=%q err=%v", string(got), err)
	}
}
