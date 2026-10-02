package js

import "runtime"

// Platform returns process.platform for the running OS: "darwin", "linux",
// "win32", "freebsd", "openbsd", "netbsd", "sunos", "aix", "android", ...
func Platform() string {
	return platformFor(runtime.GOOS)
}

func platformFor(goos string) string {
	switch goos {
	case "windows":
		return "win32"
	case "solaris", "illumos":
		return "sunos"
	case "ios":
		return "darwin"
	}
	return goos
}

// Arch returns process.arch for the running CPU: "x64", "arm64", "ia32",
// "arm", "ppc64", "s390x", "riscv64", "loong64", "mips", "mipsel", ...
func Arch() string {
	return archFor(runtime.GOARCH)
}

func archFor(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "ppc64le":
		return "ppc64"
	case "mipsle":
		return "mipsel"
	case "mips64le":
		return "mips64el"
	case "wasm":
		return "wasm32"
	}
	return goarch
}
