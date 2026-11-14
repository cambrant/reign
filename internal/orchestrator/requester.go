package orchestrator

import "context"

// requesterKey is the context key for the requester of an operation.
type requesterKey struct{}

// Requesters for operations reign starts on its own.
const (
	RequesterStartup     = "reign (startup)"
	RequesterShutdown    = "reign (shutdown)"
	RequesterHealthCheck = "reign (health check)"
	RequesterAutoRestart = "reign (auto-restart)"
)

// WithRequester returns a context that records who asked for an operation.
// Events logged while handling the operation carry this requester.
func WithRequester(ctx context.Context, requester string) context.Context {
	return context.WithValue(ctx, requesterKey{}, requester)
}

// RequesterFrom returns the requester stored in ctx, or "" if there is none.
func RequesterFrom(ctx context.Context) string {
	r, _ := ctx.Value(requesterKey{}).(string)
	return r
}
