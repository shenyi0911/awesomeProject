// Package reqctx provides request-scoped context values, such as the request ID,
// which middleware writes and the application/runtime layers propagate.
package reqctx

import "context"

type ctxKey struct{}

type operatorKey struct{}

// WithID stores the request ID in the context.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// IDFrom returns the request ID from the context, or an empty string if absent.
func IDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// WithOperator stores the authenticated operator name in the context.
func WithOperator(ctx context.Context, operator string) context.Context {
	return context.WithValue(ctx, operatorKey{}, operator)
}

// OperatorFrom returns the authenticated operator name, or an empty string if absent.
func OperatorFrom(ctx context.Context) string {
	op, _ := ctx.Value(operatorKey{}).(string)
	return op
}
