package main

import "context"

// responseGate is owned by conversationMu. Context identity also rejects late
// results from transports/providers that return success after cancellation.
type responseGate struct {
	ctx  context.Context
	stop context.CancelFunc
}

func (g *responseGate) begin() context.Context {
	g.cancel()
	g.ctx, g.stop = context.WithCancel(context.Background())
	return g.ctx
}

func (g *responseGate) cancel() {
	if g.stop != nil {
		g.stop()
	}
	g.ctx, g.stop = nil, nil
}

func (g *responseGate) active() bool { return g.ctx != nil }

func (g *responseGate) finish(ctx context.Context) bool {
	if g.ctx != ctx || ctx.Err() != nil {
		return false
	}
	g.cancel()
	return true
}
