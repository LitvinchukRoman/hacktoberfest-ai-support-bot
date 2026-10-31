package httpapi

import "context"

// Viewer stands in for fantasm's real session/auth system, which this
// standalone repo doesn't have. A future real auth middleware would inject
// one into the request context the same way; everything downstream only
// ever asks "is there a Viewer here", not how it got there.
type Viewer struct {
	ID   string
	Role string // USER | MODERATOR | ADMIN
}

func (v Viewer) IsStaff() bool { return v.Role == "MODERATOR" || v.Role == "ADMIN" }
func (v Viewer) IsAdmin() bool { return v.Role == "ADMIN" }

type viewerKey struct{}

func WithViewer(ctx context.Context, v Viewer) context.Context {
	return context.WithValue(ctx, viewerKey{}, v)
}

func ViewerFromContext(ctx context.Context) (Viewer, bool) {
	v, ok := ctx.Value(viewerKey{}).(Viewer)
	return v, ok
}
