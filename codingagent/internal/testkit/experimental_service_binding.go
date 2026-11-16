// Ported from packages/coding-agent/test/experimental-service-binding.ts (pi v1.0.0).

package testkit

import (
	"errors"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
)

// ExperimentalServiceBindingOptions configures a test service binding.
type ExperimentalServiceBindingOptions struct {
	Services []chord.ServiceRef
	OnError  func(error)
}

// ServerServiceBinding combines ServerServiceSource and RemoteServices for test assertions.
type ServerServiceBinding interface {
	services.ServerServiceSource
	chord.RemoteServices
}

// SessionServiceBinding combines SessionServiceSource and RemoteServices for test assertions.
type SessionServiceBinding interface {
	services.SessionServiceSource
	chord.RemoteServices
}

type esbServerBinding struct {
	source   services.ServerServiceSource
	services chord.RemoteServices
	done     chan struct{}
	mu       sync.Mutex
	initErr  error
}

// CreateServerServiceBinding creates a test helper for binding server services without a facet host.
func CreateServerServiceBinding(c *client.Client, options ExperimentalServiceBindingOptions) ServerServiceBinding {
	onError := options.OnError
	if onError == nil {
		onError = func(error) {}
	}
	source := services.CreateServerServiceSource(c, services.ServiceSourceOptions{OnError: onError})
	remote := source.Open(&chord.RemoteServiceSourceOpenOptions{
		Services:     options.Services,
		AssertAccess: func() {},
		OnError:      onError,
	})
	b := &esbServerBinding{
		source:   source,
		services: remote,
		done:     make(chan struct{}),
	}
	go func() {
		defer close(b.done)
		if err := remote.Ready(chord.BackgroundContext); err != nil {
			b.mu.Lock()
			b.initErr = err
			b.mu.Unlock()
			onError(err)
		}
	}()
	return b
}

func (b *esbServerBinding) AcceptsUnavailableServices() bool {
	return b.source.AcceptsUnavailableServices()
}

func (b *esbServerBinding) Connection() chord.ReplicatedState[services.ServerConnectionState] {
	return b.source.Connection()
}

func (b *esbServerBinding) Catalogue(call chord.Context) ([]chord.ServiceCatalogueEntry, error) {
	return b.source.Catalogue(call)
}

func (b *esbServerBinding) Open(options *chord.RemoteServiceSourceOpenOptions) chord.RemoteServices {
	return b.source.Open(options)
}

func (b *esbServerBinding) Use(service any) any {
	return b.services.Use(service)
}

func (b *esbServerBinding) Observe(service any, handler func(service any, call chord.Context) error) func() {
	return b.services.Observe(service, handler)
}

func (b *esbServerBinding) Ready(call chord.Context) error {
	<-b.done
	b.mu.Lock()
	initErr := b.initErr
	b.mu.Unlock()
	if initErr != nil {
		return initErr
	}
	return b.services.Ready(call)
}

func (b *esbServerBinding) Dispose(ctx chord.Context) error {
	var errs []error
	if err := b.services.Dispose(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := b.source.Dispose(ctx); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("Failed to dispose server service binding: %w", errors.Join(errs...))
	}
	return nil
}

type esbSessionBinding struct {
	source   services.SessionServiceSource
	services chord.RemoteServices
	done     chan struct{}
	mu       sync.Mutex
	initErr  error
}

// CreateSessionServiceBinding creates a test helper for binding selected-Session services without a facet host.
func CreateSessionServiceBinding(c *client.Client, options ExperimentalServiceBindingOptions) SessionServiceBinding {
	onError := options.OnError
	if onError == nil {
		onError = func(error) {}
	}
	source := services.CreateSessionServiceSource(c, services.ServiceSourceOptions{OnError: onError})
	remote := source.Open(&chord.RemoteServiceSourceOpenOptions{
		Services:     options.Services,
		AssertAccess: func() {},
		OnError:      onError,
	})
	b := &esbSessionBinding{
		source:   source,
		services: remote,
		done:     make(chan struct{}),
	}
	go func() {
		defer close(b.done)
		if err := remote.Ready(chord.BackgroundContext); err != nil {
			b.mu.Lock()
			b.initErr = err
			b.mu.Unlock()
			onError(err)
		}
	}()
	return b
}

func (b *esbSessionBinding) AcceptsUnavailableServices() bool {
	return b.source.AcceptsUnavailableServices()
}

func (b *esbSessionBinding) Attachment() chord.ReplicatedState[services.SessionAttachmentState] {
	return b.source.Attachment()
}

func (b *esbSessionBinding) Catalogue(call chord.Context) ([]chord.ServiceCatalogueEntry, error) {
	return b.source.Catalogue(call)
}

func (b *esbSessionBinding) Open(options *chord.RemoteServiceSourceOpenOptions) chord.RemoteServices {
	return b.source.Open(options)
}

func (b *esbSessionBinding) Use(service any) any {
	return b.services.Use(service)
}

func (b *esbSessionBinding) Observe(service any, handler func(service any, call chord.Context) error) func() {
	return b.services.Observe(service, handler)
}

func (b *esbSessionBinding) Ready(call chord.Context) error {
	<-b.done
	b.mu.Lock()
	initErr := b.initErr
	b.mu.Unlock()
	if initErr != nil {
		return initErr
	}
	att, ok := b.source.Attachment().Value()
	if ok && att.Status == "detached" {
		return b.source.WhenDetached(call)
	}
	if ok && att.SessionId != "" {
		return b.source.WhenAttached(att.SessionId, call)
	}
	return nil
}

func (b *esbSessionBinding) WhenAttached(sessionId string, ctx chord.Context) error {
	return b.source.WhenAttached(sessionId, ctx)
}

func (b *esbSessionBinding) WhenDetached(ctx chord.Context) error {
	return b.source.WhenDetached(ctx)
}

func (b *esbSessionBinding) Dispose(ctx chord.Context) error {
	var errs []error
	if err := b.services.Dispose(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := b.source.Dispose(ctx); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("Failed to dispose Session service binding: %w", errors.Join(errs...))
	}
	return nil
}
