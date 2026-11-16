package units

import "context"

type actorKey struct{}

// WithActor returns a context whose writes are attributed to actor.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor set by WithActor, or "" if none.
func ActorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey{}).(string)
	return actor
}
