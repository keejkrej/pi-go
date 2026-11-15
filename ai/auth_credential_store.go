// Ported from packages/ai/src/auth/credential-store.ts (pi v1.5.2).

package ai

import (
	"context"
	"maps"
	"sync"

	"github.com/keejkrej/pi-go/internal/omap"
)

// InMemoryCredentialStore is the default process-local CredentialStore keyed by Provider.Id.
// Modify and Delete calls for the same providerId execute in strict FIFO order while honoring
// lock-wait cancellation on ctx.
type InMemoryCredentialStore struct {
	mu          sync.Mutex
	credentials *omap.Map[string, Credential]
	chains      map[string]chan struct{}
}

// NewInMemoryCredentialStore returns an empty InMemoryCredentialStore.
func NewInMemoryCredentialStore() *InMemoryCredentialStore {
	return &InMemoryCredentialStore{
		credentials: omap.NewMap[string, Credential](),
		chains:      make(map[string]chan struct{}),
	}
}

func (s *InMemoryCredentialStore) ensureInit() {
	if s.credentials == nil {
		s.credentials = omap.NewMap[string, Credential]()
	}
	if s.chains == nil {
		s.chains = make(map[string]chan struct{})
	}
}

func acsCloneCredential(c Credential) Credential {
	if c == nil {
		return nil
	}
	switch v := c.(type) {
	case *ApiKeyCredential:
		if v == nil {
			return nil
		}
		cp := ApiKeyCredential{Type: v.Type}
		if cp.Type == "" {
			cp.Type = AuthTypeApiKey
		}
		if v.Key != nil {
			k := *v.Key
			cp.Key = &k
		}
		if v.Env != nil {
			cp.Env = maps.Clone(v.Env)
		}
		return &cp
	case *OAuthCredential:
		if v == nil {
			return nil
		}
		cp := OAuthCredential{
			Type: v.Type,
			OAuthCredentials: OAuthCredentials{
				Refresh: v.Refresh,
				Access:  v.Access,
				Expires: v.Expires,
			},
		}
		if cp.Type == "" {
			cp.Type = AuthTypeOAuth
		}
		if v.Extra != nil {
			cp.Extra = v.Extra.Clone()
		}
		return &cp
	default:
		return c
	}
}

func (s *InMemoryCredentialStore) enqueue(
	ctx context.Context,
	providerId string,
	task func() (Credential, error),
) (Credential, error) {
	sig := OperationSignal(ctx)

	s.mu.Lock()
	s.ensureInit()
	prev := s.chains[providerId]
	next := make(chan struct{})
	s.chains[providerId] = next
	s.mu.Unlock()

	release := func() {
		close(next)
		s.mu.Lock()
		if s.chains[providerId] == next {
			delete(s.chains, providerId)
		}
		s.mu.Unlock()
	}

	if prev != nil {
		select {
		case <-prev:
		case <-sig.Done():
			go func() {
				<-prev
				release()
			}()
			return nil, uaAbortReason(sig)
		}
	}

	defer release()
	if sig.Err() != nil {
		return nil, uaAbortReason(sig)
	}
	return RaceWithAbortSignal(sig, task)
}

func (s *InMemoryCredentialStore) Read(ctx context.Context, providerId string, _ *AuthOperationOptions) (Credential, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, uaAbortReason(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credentials == nil {
		return nil, nil
	}
	cred, _ := s.credentials.Get(providerId)
	return acsCloneCredential(cred), nil
}

func (s *InMemoryCredentialStore) List(ctx context.Context, _ *AuthOperationOptions) ([]CredentialInfo, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, uaAbortReason(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credentials == nil {
		return []CredentialInfo{}, nil
	}
	out := make([]CredentialInfo, 0, s.credentials.Len())
	for providerId, cred := range s.credentials.All() {
		if cred == nil {
			continue
		}
		out = append(out, CredentialInfo{
			ProviderId: providerId,
			Type:       cred.CredentialType(),
		})
	}
	return out, nil
}

func (s *InMemoryCredentialStore) Modify(
	ctx context.Context,
	providerId string,
	fn func(current Credential) (Credential, error),
	_ *AuthOperationOptions,
) (Credential, error) {
	sig := OperationSignal(ctx)
	return s.enqueue(sig, providerId, func() (Credential, error) {
		s.mu.Lock()
		current, _ := s.credentials.Get(providerId)
		s.mu.Unlock()

		next, err := fn(acsCloneCredential(current))
		if err != nil {
			return nil, err
		}
		if sig.Err() != nil {
			return nil, uaAbortReason(sig)
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		if next != nil {
			stored := acsCloneCredential(next)
			s.credentials.Set(providerId, stored)
			return acsCloneCredential(stored), nil
		}
		updated, _ := s.credentials.Get(providerId)
		return acsCloneCredential(updated), nil
	})
}

func (s *InMemoryCredentialStore) Delete(ctx context.Context, providerId string, _ *AuthOperationOptions) error {
	_, err := s.enqueue(ctx, providerId, func() (Credential, error) {
		s.mu.Lock()
		if s.credentials != nil {
			s.credentials.Delete(providerId)
		}
		s.mu.Unlock()
		return nil, nil
	})
	return err
}

var _ CredentialStore = (*InMemoryCredentialStore)(nil)
