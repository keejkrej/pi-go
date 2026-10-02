// Ported from chalk@6.0.0 source/vendor/supports-color/index.js (os.release() on win32).

package ansi

import (
	"strconv"

	"golang.org/x/sys/windows"
)

// supportsColorOSRelease is Node's os.release() on Windows: "<major>.<minor>.<build>" from RtlGetVersion.
func supportsColorOSRelease() string {
	v := windows.RtlGetVersion()
	return strconv.FormatUint(uint64(v.MajorVersion), 10) + "." + strconv.FormatUint(uint64(v.MinorVersion), 10) + "." + strconv.FormatUint(uint64(v.BuildNumber), 10)
}
