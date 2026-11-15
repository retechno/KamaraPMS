package auth

import "context"

// OperatorPrincipal is the authenticated caller of the platform console: a person of the company that runs KamaraPMS, not a user of any tenant
// (docs/architecture/39-platform-console.md). It lives here, next to Principal, so that every module can tell who changed a subscription without importing the operator module.
type OperatorPrincipal struct {
	OperatorID int64
	SessionID  int64
}

type operatorKey struct{}

// WithOperator stores p in ctx.
func WithOperator(ctx context.Context, p OperatorPrincipal) context.Context {
	return context.WithValue(ctx, operatorKey{}, p)
}

// OperatorFrom returns the operator who makes the call, if there is one (a call of pms-admin or of the system has none).
func OperatorFrom(ctx context.Context) (OperatorPrincipal, bool) {
	p, ok := ctx.Value(operatorKey{}).(OperatorPrincipal)
	return p, ok
}
