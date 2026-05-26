package tools

import "context"

type userIDKey struct{}
type threadIDKey struct{}
type agentIDKey struct{}

// WithUserID returns a derived context that carries the authenticated user_id
// for the current run. Tools that need user identity (memory_*, plan_*) MUST
// read it via UserIDFromContext rather than accepting it as a model-supplied
// parameter — otherwise the model could spoof another user's identity.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID)
}

// UserIDFromContext returns the user_id stored on ctx by WithUserID, or "" if
// no user identity has been bound to the context.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey{}).(string)
	return v
}

// WithRunMetadata binds the current thread and agent identity onto ctx so tools
// can default scheduled work back into the originating conversation.
func WithRunMetadata(ctx context.Context, threadID, agentID string) context.Context {
	ctx = context.WithValue(ctx, threadIDKey{}, threadID)
	ctx = context.WithValue(ctx, agentIDKey{}, agentID)
	return ctx
}

func ThreadIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(threadIDKey{}).(string)
	return v
}

func AgentIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(agentIDKey{}).(string)
	return v
}
