package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestWithFileMutationQueue_SerializationAndSymlinkAliases(t *testing.T) {
	ctx := context.Background()

	t.Run("serializes operations on the same file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "same.txt")
		var mu sync.Mutex
		var order []string
		var wg sync.WaitGroup
		started := make(chan struct{})

		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := WithFileMutationQueue(ctx, p, func() (struct{}, error) {
				mu.Lock()
				order = append(order, "first:start")
				mu.Unlock()
				close(started)
				time.Sleep(30 * time.Millisecond)
				mu.Lock()
				order = append(order, "first:end")
				mu.Unlock()
				return struct{}{}, nil
			})
			if err != nil {
				t.Errorf("first failed: %v", err)
			}
		}()

		<-started
		go func() {
			defer wg.Done()
			_, err := WithFileMutationQueue(ctx, p, func() (struct{}, error) {
				mu.Lock()
				order = append(order, "second:start", "second:end")
				mu.Unlock()
				return struct{}{}, nil
			})
			if err != nil {
				t.Errorf("second failed: %v", err)
			}
		}()

		wg.Wait()
		want := []string{"first:start", "first:end", "second:start", "second:end"}
		if !slices.Equal(order, want) {
			t.Fatalf("order=%v, want %v", order, want)
		}
	})

	t.Run("allows different files to proceed in parallel", func(t *testing.T) {
		dir := t.TempDir()
		aPath := filepath.Join(dir, "a.txt")
		bPath := filepath.Join(dir, "b.txt")

		bothStarted := make(chan struct{})
		var mu sync.Mutex
		count := 0

		var wg sync.WaitGroup
		wg.Add(2)
		for _, p := range []string{aPath, bPath} {
			go func(target string) {
				defer wg.Done()
				_, err := WithFileMutationQueue(ctx, target, func() (struct{}, error) {
					mu.Lock()
					count++
					if count == 2 {
						close(bothStarted)
					}
					mu.Unlock()
					select {
					case <-bothStarted:
					case <-time.After(2 * time.Second):
						t.Errorf("expected parallel execution across distinct files")
					}
					return struct{}{}, nil
				})
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}(p)
		}
		wg.Wait()
	})

	t.Run("uses the same queue for symlink aliases", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.txt")
		alias := filepath.Join(dir, "alias.txt")
		if err := os.WriteFile(target, []byte("hello\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, alias); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}

		var mu sync.Mutex
		var order []string
		started := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			_, err := WithFileMutationQueue(ctx, target, func() (struct{}, error) {
				mu.Lock()
				order = append(order, "target:start")
				mu.Unlock()
				close(started)
				time.Sleep(30 * time.Millisecond)
				mu.Lock()
				order = append(order, "target:end")
				mu.Unlock()
				return struct{}{}, nil
			})
			if err != nil {
				t.Errorf("target failed: %v", err)
			}
		}()

		<-started
		go func() {
			defer wg.Done()
			_, err := WithFileMutationQueue(ctx, alias, func() (struct{}, error) {
				mu.Lock()
				order = append(order, "alias:start", "alias:end")
				mu.Unlock()
				return struct{}{}, nil
			})
			if err != nil {
				t.Errorf("alias failed: %v", err)
			}
		}()

		wg.Wait()
		want := []string{"target:start", "target:end", "alias:start", "alias:end"}
		if !slices.Equal(order, want) {
			t.Fatalf("order=%v, want %v", order, want)
		}
	})

	t.Run("releases lock on error", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "err.txt")
		_, err := WithFileMutationQueue(ctx, p, func() (int, error) {
			return 0, errors.New("boom")
		})
		if err == nil || err.Error() != "boom" {
			t.Fatalf("expected boom error, got %v", err)
		}
		got, err := WithFileMutationQueue(ctx, p, func() (int, error) {
			return 42, nil
		})
		if err != nil || got != 42 {
			t.Fatalf("got (%d, %v), want (42, nil)", got, err)
		}
	})
}
