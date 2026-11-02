package executor

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// xaiThinkingGate holds client chunks until reasoning text, a tool call, or the
// prose floor. It never writes a degraded body on an attempt that is still
// allowed to switch credentials.
type xaiThinkingGate struct {
	active   bool
	gate     *helps.ClientGate
	reporter *helps.UsageReporter
	stop     func()
	retried  bool
}

func newXAIThinkingGate(ctx context.Context, model string, original, upstream []byte, opts cliproxyexecutor.Options, reporter *helps.UsageReporter, stop func()) *xaiThinkingGate {
	active, minTokens := helps.ThinkingGateActive(ctx, model, original, upstream, opts.Metadata)
	if !active {
		return &xaiThinkingGate{}
	}
	gate := helps.NewClientGate(minTokens)
	gate.RequireToolReasoning = helps.ToolCallsRequireReasoning(model, original, upstream)
	return &xaiThinkingGate{
		active:   true,
		gate:     gate,
		reporter: reporter,
		stop:     stop,
	}
}

func (g *xaiThinkingGate) emit(ctx context.Context, out chan<- cliproxyexecutor.StreamChunk, payloads [][]byte) bool {
	if g == nil || !g.active || g.retried {
		return writeStreamPayloads(ctx, out, payloads)
	}
	for _, payload := range payloads {
		action, flush := g.gate.Push(payload)
		switch action {
		case helps.GateHold:
			continue
		case helps.GateRelease:
			if !writeStreamPayloads(ctx, out, flush) {
				return false
			}
		case helps.GateRetry:
			return g.fail(ctx, out)
		}
	}
	return true
}

func (g *xaiThinkingGate) finish(ctx context.Context, out chan<- cliproxyexecutor.StreamChunk) {
	if g == nil || !g.active || g.retried || g.gate == nil {
		return
	}
	action, flush := g.gate.Finish()
	if action == helps.GateRetry {
		g.fail(ctx, out)
		return
	}
	writeStreamPayloads(ctx, out, flush)
}

func (g *xaiThinkingGate) fail(ctx context.Context, out chan<- cliproxyexecutor.StreamChunk) bool {
	if g == nil || g.retried {
		return false
	}
	g.retried = true
	if g.stop != nil {
		g.stop()
	}
	tokens := 0
	if g.gate != nil {
		tokens = g.gate.ContentTokens()
	}
	if g.reporter != nil {
		g.reporter.SealReasoningVisibility()
		g.reporter.Publish(ctx, gateUsageDetail(g.gate, tokens))
	}
	log.WithField("output_tokens", tokens).Info("xai thinking gate: reasoning text missing, switching credential")
	return writeStreamChunk(ctx, out, cliproxyexecutor.StreamChunk{Err: cliproxyexecutor.NewReasoningAbsentError()})
}

// rejectNonStream returns a retryable thinking-gate error when a completed
// body has enough prose and no reasoning text. The last allowed attempt
// returns false so the caller delivers the body.
func rejectNonStream(ctx context.Context, model string, original, upstream, body []byte, opts cliproxyexecutor.Options, reporter *helps.UsageReporter) error {
	active, minTokens := helps.ThinkingGateActive(ctx, model, original, upstream, opts.Metadata)
	if !active || len(body) == 0 {
		return nil
	}
	gate := helps.NewClientGate(minTokens)
	gate.RequireToolReasoning = helps.ToolCallsRequireReasoning(model, original, upstream)
	action, _ := gate.Push(body)
	if action == helps.GateHold {
		action, _ = gate.Finish()
	}
	if action != helps.GateRetry {
		return nil
	}
	if reporter != nil {
		reporter.ObserveClientReasoning(body)
		reporter.SealReasoningVisibility()
		reporter.Publish(ctx, gateUsageDetail(gate, gate.ContentTokens()))
	}
	log.WithField("output_tokens", gate.ContentTokens()).Info("xai thinking gate: non-stream reasoning text missing, switching credential")
	return cliproxyexecutor.NewReasoningAbsentError()
}

// gateUsageDetail keeps the tool-call marker on the aborted attempt. The
// egress guard quarantines an account from this record; without the marker a
// rejected bare tool call carries zero output and would never strike the node.
func gateUsageDetail(gate *helps.ClientGate, tokens int) usage.Detail {
	detail := usage.Detail{OutputTokens: int64(tokens)}
	if gate != nil && gate.RetriedOnTool() {
		one := int64(1)
		detail.ToolUsageDetails = &usage.ToolUsageDetails{ToolCallsRequested: &one}
	}
	return detail
}

func writeStreamPayloads(ctx context.Context, out chan<- cliproxyexecutor.StreamChunk, payloads [][]byte) bool {
	for _, payload := range payloads {
		if len(payload) == 0 {
			continue
		}
		if !writeStreamChunk(ctx, out, cliproxyexecutor.StreamChunk{Payload: payload}) {
			return false
		}
	}
	return true
}

func writeStreamChunk(ctx context.Context, out chan<- cliproxyexecutor.StreamChunk, chunk cliproxyexecutor.StreamChunk) bool {
	if ctx == nil {
		out <- chunk
		return true
	}
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}
