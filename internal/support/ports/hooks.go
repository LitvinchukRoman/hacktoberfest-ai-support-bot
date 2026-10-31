package ports

import "context"

type hooksKey struct{}

// WithHooks installs a hooks list on ctx if one isn't already present
// (nested calls share the outermost list) and returns it alongside.
func WithHooks(ctx context.Context) (context.Context, *[]func()) {
	if hooks, ok := ctx.Value(hooksKey{}).(*[]func()); ok {
		return ctx, hooks
	}
	hooks := &[]func(){}
	return context.WithValue(ctx, hooksKey{}, hooks), hooks
}

// AfterCommit schedules fn to run once the enclosing TxManager.WithinTx
// commits. Outside any transaction, fn runs immediately.
func AfterCommit(ctx context.Context, fn func()) {
	if hooks, ok := ctx.Value(hooksKey{}).(*[]func()); ok {
		*hooks = append(*hooks, fn)
		return
	}
	fn()
}
