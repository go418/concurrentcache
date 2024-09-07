package concurrentcache

import "context"

type debugKey struct{}

type debugFns struct {
	onStartedWaiting func()
}

func (d *debugFns) OnStartedWaiting() {
	if d == nil || d.onStartedWaiting == nil {
		return
	}

	d.onStartedWaiting()
}

func OnStartedWaiting(ctx context.Context, onStartedWaiting func()) context.Context {
	return context.WithValue(ctx, debugKey{}, debugFns{
		onStartedWaiting: onStartedWaiting,
	})
}

func debuggerFromContext(ctx context.Context) *debugFns {
	if v, ok := ctx.Value(debugKey{}).(debugFns); ok {
		return &v
	}
	return nil
}
