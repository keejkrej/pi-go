// Ported from packages/coding-agent/test/experimental-server-profile.test.ts (pi v1.0.0).

package experimental_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/experimental"
)

const (
	tespFirstServerId  = "00000000-0000-4000-8000-000000000001"
	tespSecondServerId = "00000000-0000-4000-8000-000000000002"
)

func tespTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "psp-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func TestExperimentalServerProfile(t *testing.T) {
	t.Run("serializes launchers and preserves the server identity", func(t *testing.T) {
		dir := tespTempDir(t)
		first, err := experimental.AcquireServerProfile(dir)
		if err != nil {
			t.Fatalf("first AcquireServerProfile failed: %v", err)
		}

		var secondAcquired atomic.Bool
		type acquireResult struct {
			profile *experimental.ServerProfile
			err     error
		}
		resCh := make(chan acquireResult, 1)
		go func() {
			p, err := experimental.AcquireServerProfile(dir)
			if err == nil {
				secondAcquired.Store(true)
			}
			resCh <- acquireResult{profile: p, err: err}
		}()

		time.Sleep(60 * time.Millisecond)
		if secondAcquired.Load() {
			t.Fatal("expected second AcquireServerProfile to block while first held launcher lock")
		}

		if err := first.Release(); err != nil {
			t.Fatalf("first.Release failed: %v", err)
		}

		res := <-resCh
		if res.err != nil {
			t.Fatalf("second AcquireServerProfile failed: %v", res.err)
		}
		if !secondAcquired.Load() {
			t.Fatal("expected secondAcquired true after release")
		}
		if res.profile.ServerId != first.ServerId {
			t.Fatalf("expected matching serverId %s, got %s", first.ServerId, res.profile.ServerId)
		}

		raw, err := os.ReadFile(filepath.Join(dir, "default-server-id"))
		if err != nil {
			t.Fatalf("ReadFile default-server-id failed: %v", err)
		}
		if strings.TrimSpace(string(raw)) != string(first.ServerId) {
			t.Fatalf("default-server-id content %q != %q", strings.TrimSpace(string(raw)), first.ServerId)
		}
		if err := res.profile.Release(); err != nil {
			t.Fatalf("second.Release failed: %v", err)
		}
	})

	t.Run("does not serialize different server IDs in one directory", func(t *testing.T) {
		dir := tespTempDir(t)
		var first, second *experimental.ServerProfile
		var err1, err2 error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			first, err1 = experimental.AcquireServerProfile(dir, tespFirstServerId)
		}()
		go func() {
			defer wg.Done()
			second, err2 = experimental.AcquireServerProfile(dir, tespSecondServerId)
		}()
		wg.Wait()

		if err1 != nil || err2 != nil {
			t.Fatalf("concurrent AcquireServerProfile failed: err1=%v err2=%v", err1, err2)
		}
		if first.ServerId != tespFirstServerId {
			t.Fatalf("first serverId = %s, want %s", first.ServerId, tespFirstServerId)
		}
		if second.ServerId != tespSecondServerId {
			t.Fatalf("second serverId = %s, want %s", second.ServerId, tespSecondServerId)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir failed: %v", err)
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		want := []string{
			"launcher-" + tespFirstServerId + ".lock",
			"launcher-" + tespSecondServerId + ".lock",
		}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("lock entries = %v, want %v", names, want)
		}

		if err := first.Release(); err != nil {
			t.Fatalf("first.Release failed: %v", err)
		}
		if err := second.Release(); err != nil {
			t.Fatalf("second.Release failed: %v", err)
		}
	})

	t.Run("does not share the default identity across server directories", func(t *testing.T) {
		dir1 := tespTempDir(t)
		dir2 := tespTempDir(t)
		first, err := experimental.AcquireServerProfile(dir1)
		if err != nil {
			t.Fatalf("dir1 AcquireServerProfile failed: %v", err)
		}
		defer func() { _ = first.Release() }()
		second, err := experimental.AcquireServerProfile(dir2)
		if err != nil {
			t.Fatalf("dir2 AcquireServerProfile failed: %v", err)
		}
		defer func() { _ = second.Release() }()

		if first.ServerId == second.ServerId {
			t.Fatalf("expected distinct default server IDs across directories, both got %s", first.ServerId)
		}
	})

	t.Run("rejects a corrupt default identity", func(t *testing.T) {
		dir := tespTempDir(t)
		if err := os.WriteFile(filepath.Join(dir, "default-server-id"), []byte("invalid\n"), 0o600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		_, err := experimental.AcquireServerProfile(dir)
		if err == nil || !strings.Contains(err.Error(), "Invalid default experimental server identity") {
			t.Fatalf("expected invalid default server identity error, got %v", err)
		}
	})

	t.Run("rejects an invalid explicit server ID", func(t *testing.T) {
		dir := tespTempDir(t)
		_, err := experimental.AcquireServerProfile(dir, "invalid")
		if err == nil || !strings.Contains(err.Error(), "Invalid experimental server ID") {
			t.Fatalf("expected invalid explicit server ID error, got %v", err)
		}
	})
}
