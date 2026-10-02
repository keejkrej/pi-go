package js

import "testing"

func TestPlatform_GoToNodeNames(t *testing.T) {
	platforms := map[string]string{
		"darwin": "darwin", "linux": "linux", "windows": "win32", "freebsd": "freebsd", "openbsd": "openbsd",
		"netbsd": "netbsd", "solaris": "sunos", "illumos": "sunos", "aix": "aix", "android": "android",
	}
	for goos, want := range platforms {
		if got := platformFor(goos); got != want {
			t.Errorf("platformFor(%q) = %q, want %q", goos, got, want)
		}
	}
	arches := map[string]string{
		"amd64": "x64", "arm64": "arm64", "386": "ia32", "arm": "arm", "ppc64le": "ppc64", "ppc64": "ppc64",
		"s390x": "s390x", "riscv64": "riscv64", "loong64": "loong64", "mips": "mips", "mipsle": "mipsel",
		"mips64le": "mips64el",
	}
	for goarch, want := range arches {
		if got := archFor(goarch); got != want {
			t.Errorf("archFor(%q) = %q, want %q", goarch, got, want)
		}
	}
}
