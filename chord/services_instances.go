// Ported from packages/chord/src/services/instances.ts (pi v1.0.0).

package chord

import (
	"iter"
	"sync"

	"github.com/keejkrej/pi-go/internal/omap"
)

type InstanceDirectoryEntry interface {
	Key() string
	Generation() int
	Service() any
	Deactivate()
}

type InstanceDirectoryOptions struct {
	Ready   bool        `json:"ready"`
	OnError func(error) `json:"onError"`
}

type siObserverTask struct {
	cancel func(reason any)
}

type siObserver struct {
	handler func(service any, context Context) error
	// tasks is keyed by InstanceDirectoryEntry.Key. A live key belongs to one entry.
	tasks  *omap.Map[string, siObserverTask]
	closed bool
}

// InstanceDirectory owns keyed instance lifetime and the cancellable tasks observing those instances.
type InstanceDirectory[TEntry InstanceDirectoryEntry] struct {
	mu          sync.Mutex
	entries     *omap.Map[string, TEntry]
	observers   *omap.Set[*siObserver]
	reportError func(error)
	ready       bool
	disposed    bool
}

func NewInstanceDirectory[TEntry InstanceDirectoryEntry](options *InstanceDirectoryOptions) *InstanceDirectory[TEntry] {
	panic("unported: NewInstanceDirectory")
}

func (d *InstanceDirectory[TEntry]) ObserverCount() int {
	panic("unported: InstanceDirectory.ObserverCount")
}

func (d *InstanceDirectory[TEntry]) Values() iter.Seq[TEntry] {
	panic("unported: InstanceDirectory.Values")
}

func (d *InstanceDirectory[TEntry]) Get(key string) (TEntry, bool) {
	panic("unported: InstanceDirectory.Get")
}

func (d *InstanceDirectory[TEntry]) Insert(entry TEntry) error {
	panic("unported: InstanceDirectory.Insert")
}

func (d *InstanceDirectory[TEntry]) Replace(entry TEntry) error {
	panic("unported: InstanceDirectory.Replace")
}

func (d *InstanceDirectory[TEntry]) Remove(entry TEntry) {
	panic("unported: InstanceDirectory.Remove")
}

func (d *InstanceDirectory[TEntry]) Ready() error {
	panic("unported: InstanceDirectory.Ready")
}

func (d *InstanceDirectory[TEntry]) Reset() {
	panic("unported: InstanceDirectory.Reset")
}

func (d *InstanceDirectory[TEntry]) Observe(handler func(service any, context Context) error) (func(), error) {
	panic("unported: InstanceDirectory.Observe")
}

func (d *InstanceDirectory[TEntry]) Dispose() error {
	panic("unported: InstanceDirectory.Dispose")
}

func (d *InstanceDirectory[TEntry]) remove(entry TEntry) {
	panic("unported: InstanceDirectory.remove")
}

func (d *InstanceDirectory[TEntry]) startAll(entry TEntry) {
	panic("unported: InstanceDirectory.startAll")
}

func (d *InstanceDirectory[TEntry]) start(observer *siObserver, entry TEntry) {
	panic("unported: InstanceDirectory.start")
}

func (d *InstanceDirectory[TEntry]) assertActive() error {
	panic("unported: InstanceDirectory.assertActive")
}

func siToError(value any) error {
	panic("unported: siToError")
}
