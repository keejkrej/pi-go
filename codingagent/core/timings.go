// Ported from packages/coding-agent/src/core/timings.ts (pi v1.5.2).

package core

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// TimingLabel names a startup timing instrumentation namespace.
type TimingLabel string

const (
	TimingLabelMain       TimingLabel = "main"
	TimingLabelExtensions TimingLabel = "extensions"
)

type timiEntry struct {
	label string
	ms    int64
}

type timiNamespace struct {
	timings  []timiEntry
	lastTime time.Time
}

var (
	timiMu               sync.Mutex
	timiNamespaces       = make(map[TimingLabel]*timiNamespace)
	timiNamespaceOrder   []TimingLabel
	timiStderr           io.Writer = os.Stderr
	timiNow                        = time.Now
	timiEnvOverrideForUt *bool
)

func timiEnabled() bool {
	if timiEnvOverrideForUt != nil {
		return *timiEnvOverrideForUt
	}
	return os.Getenv("PI_TIMING") == "1"
}

func timiResolveNamespace(namespace []TimingLabel) TimingLabel {
	if len(namespace) > 0 && namespace[0] != "" {
		return namespace[0]
	}
	return TimingLabelMain
}

func timiResetLocked(ns TimingLabel, now time.Time) {
	if _, exists := timiNamespaces[ns]; !exists {
		timiNamespaceOrder = append(timiNamespaceOrder, ns)
	}
	timiNamespaces[ns] = &timiNamespace{
		timings:  nil,
		lastTime: now,
	}
}

// ResetTimings clears recorded timings for the given namespace (default "main").
func ResetTimings(namespace ...TimingLabel) {
	if !timiEnabled() {
		return
	}
	ns := timiResolveNamespace(namespace)
	timiMu.Lock()
	defer timiMu.Unlock()
	timiResetLocked(ns, timiNow())
}

// Time records elapsed milliseconds since the previous timing mark in the namespace.
func Time(label string, namespace ...TimingLabel) {
	if !timiEnabled() {
		return
	}
	ns := timiResolveNamespace(namespace)
	timiMu.Lock()
	defer timiMu.Unlock()

	now := timiNow()
	entry, ok := timiNamespaces[ns]
	if !ok {
		timiResetLocked(ns, now)
		entry = timiNamespaces[ns]
	}
	elapsedMs := now.Sub(entry.lastTime).Milliseconds()
	entry.timings = append(entry.timings, timiEntry{label: label, ms: elapsedMs})
	entry.lastTime = now
}

func timiPrintGroup(w io.Writer, title string, timings []timiEntry) {
	var printable []timiEntry
	for _, t := range timings {
		if t.ms >= 0 {
			printable = append(printable, t)
		}
	}
	if len(printable) == 0 {
		return
	}
	var total int64
	fmt.Fprintf(w, "\n--- %s ---\n", title)
	for _, t := range printable {
		fmt.Fprintf(w, "  %s: %dms\n", t.label, t.ms)
		total += t.ms
	}
	fmt.Fprintf(w, "  TOTAL: %dms\n", total)
	fmt.Fprintf(w, "%s\n\n", strings.Repeat("-", len(title)+8))
}

// PrintTimings writes all non-empty startup timing groups to stderr.
func PrintTimings() {
	if !timiEnabled() {
		return
	}
	type timiSnapshot struct {
		ns      TimingLabel
		timings []timiEntry
	}
	timiMu.Lock()
	w := timiStderr
	snap := make([]timiSnapshot, 0, len(timiNamespaceOrder))
	for _, ns := range timiNamespaceOrder {
		if group, ok := timiNamespaces[ns]; ok {
			copied := append([]timiEntry(nil), group.timings...)
			snap = append(snap, timiSnapshot{ns: ns, timings: copied})
		}
	}
	timiMu.Unlock()

	for _, s := range snap {
		timiPrintGroup(w, fmt.Sprintf("Startup Timings: %s", s.ns), s.timings)
	}
}
