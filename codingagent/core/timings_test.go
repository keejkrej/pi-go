package core

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func ttimiSetupTimingTest(t *testing.T, enabled bool) (*bytes.Buffer, *time.Time) {
	t.Helper()
	timiMu.Lock()
	origNamespaces := timiNamespaces
	origOrder := timiNamespaceOrder
	origStderr := timiStderr
	origNow := timiNow
	origOverride := timiEnvOverrideForUt

	buf := &bytes.Buffer{}
	cur := time.Unix(1000, 0)
	flag := enabled

	timiNamespaces = make(map[TimingLabel]*timiNamespace)
	timiNamespaceOrder = nil
	timiStderr = buf
	timiNow = func() time.Time { return cur }
	timiEnvOverrideForUt = &flag
	timiMu.Unlock()

	t.Cleanup(func() {
		timiMu.Lock()
		timiNamespaces = origNamespaces
		timiNamespaceOrder = origOrder
		timiStderr = origStderr
		timiNow = origNow
		timiEnvOverrideForUt = origOverride
		timiMu.Unlock()
	})
	return buf, &cur
}

func TestTimingsDisabledDoesNotRecordOrPrint(t *testing.T) {
	buf, cur := ttimiSetupTimingTest(t, false)
	ResetTimings()
	*cur = cur.Add(25 * time.Millisecond)
	Time("step1")
	PrintTimings()
	if buf.Len() != 0 {
		t.Fatalf("expected no output when disabled, got %q", buf.String())
	}
}

func TestTimingsRecordAndPrintNamespaces(t *testing.T) {
	buf, cur := ttimiSetupTimingTest(t, true)
	ResetTimings()
	*cur = cur.Add(15 * time.Millisecond)
	Time("load settings")
	*cur = cur.Add(35 * time.Millisecond)
	Time("load auth")

	ResetTimings(TimingLabelExtensions)
	*cur = cur.Add(40 * time.Millisecond)
	Time("load ext-a", TimingLabelExtensions)

	PrintTimings()
	out := buf.String()
	for _, substr := range []string{
		"--- Startup Timings: main ---",
		"  load settings: 15ms",
		"  load auth: 35ms",
		"  TOTAL: 50ms",
		"--- Startup Timings: extensions ---",
		"  load ext-a: 40ms",
		"  TOTAL: 40ms",
	} {
		if !strings.Contains(out, substr) {
			t.Fatalf("output missing %q in:\n%s", substr, out)
		}
	}
}

func TestTimingsPrintEmptyAfterResetIsNoop(t *testing.T) {
	buf, _ := ttimiSetupTimingTest(t, true)
	ResetTimings()
	PrintTimings()
	if buf.Len() != 0 {
		t.Fatalf("expected empty output when no timings recorded, got %q", buf.String())
	}
}
