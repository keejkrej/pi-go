// Ported from packages/chord/src/services/loopback.ts (pi v1.0.0).

package chord

import "github.com/keejkrej/pi-go/internal/js"

type slLoopbackTransport struct {
	provider *RemoteServiceProvider
}

func (t *slLoopbackTransport) Invoke(call ServiceCall, callContext Context) (JsonValue, bool, error) {
	return t.provider.Invoke(call, callContext)
}

func (t *slLoopbackTransport) Subscribe(serviceId string, mode ServiceMode, listener func(update ServiceProviderUpdate, call Context), callContext Context) (ServiceSubscription, error) {
	sub, err := t.provider.Subscribe(serviceId, mode, func(update ServiceProviderUpdate, ctx Context) error {
		if listener != nil {
			listener(update, ctx)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &slLoopbackSubscription{sub: sub}, nil
}

type slLoopbackSubscription struct {
	sub ServiceSubscription
}

func (s *slLoopbackSubscription) Snapshot() ServiceSubscriptionSnapshot {
	return s.sub.Snapshot()
}

func (s *slLoopbackSubscription) Activate() error {
	return s.sub.Activate()
}

func (s *slLoopbackSubscription) Close(call Context) error {
	return s.sub.Close(call)
}

// CreateLoopbackServiceTransport connects a provider to a binding without changing remote service semantics.
func CreateLoopbackServiceTransport(provider *RemoteServiceProvider) RemoteServiceTransport {
	if provider == nil {
		panic(js.NewTypeError("Remote service provider must not be nil"))
	}
	return &slLoopbackTransport{provider: provider}
}
