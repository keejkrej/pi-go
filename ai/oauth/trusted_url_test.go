package oauth

import "testing"

func TestTrustedUrl_TrustedHttpUrlMatchesWhatwgRules(t *testing.T) {
	valid := []struct {
		in   any
		want string
	}{
		{"https://github.com/login/device", "https://github.com/login/device"},
		{"https://github.com/login/\x1b]8;;evil", "https://github.com/login/%1B]8;;evil"},
		{"http://localhost:80/test", "http://localhost/test"},
		{"https://EXAMPLE.COM:443", "https://example.com/"},
		{"https://EXAMPLE.COM:8443/a b?q=1#f", "https://example.com:8443/a%20b?q=1#f"},
		{"  https://github.com/a\tb\nc  ", "https://github.com/abc"},
		{"https://[::1]:443/cb", "https://[::1]/cb"},
		{"https://github.com/café?x=a b", "https://github.com/caf%C3%A9?x=a%20b"},
	}
	for _, tc := range valid {
		got := TrustedHttpUrl(tc.in)
		if got == nil || *got != tc.want {
			t.Fatalf("TrustedHttpUrl(%#v) = %v, want %q", tc.in, got, tc.want)
		}
	}

	invalid := []any{
		nil,
		123,
		true,
		"",
		"not a url",
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,<script>",
		"https://",
		"https://host name/a",
		"https://host\x1bname/a",
		"https://example.com:65536/a",
	}
	for _, in := range invalid {
		if got := TrustedHttpUrl(in); got != nil {
			t.Fatalf("TrustedHttpUrl(%#v) = %q, want nil", in, *got)
		}
	}
}
