package helps

import (
	"context"
	"strings"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestClientGateReleasesOnReasoningBeforeContent(t *testing.T) {
	gate := NewClientGate(32)
	action, flush := gate.Push([]byte(`data: {"choices":[{"delta":{"reasoning_content":"17*20"}}]}`))
	if action != GateRelease || len(flush) != 1 {
		t.Fatalf("reasoning chunk action=%v flush=%d", action, len(flush))
	}
}

func TestClientGateRetriesAfterProseFloor(t *testing.T) {
	gate := NewClientGate(8)
	prose := strings.Repeat("abcd", 8) // 32 ASCII chars -> 8 tokens
	action, flush := gate.Push([]byte(`{"choices":[{"delta":{"content":"` + prose + `"}}]}`))
	if action != GateRetry || flush != nil {
		t.Fatalf("prose floor action=%v flush=%v tokens=%d", action, flush, gate.ContentTokens())
	}
	if gate.ContentTokens() < 8 {
		t.Fatalf("tokens=%d", gate.ContentTokens())
	}
}

func TestClientGateHoldsThenReleasesShortReply(t *testing.T) {
	gate := NewClientGate(32)
	action, flush := gate.Push([]byte(`{"choices":[{"delta":{"content":"ok"}}]}`))
	if action != GateHold || flush != nil {
		t.Fatalf("short chunk action=%v flush=%v", action, flush)
	}
	action, flush = gate.Finish()
	if action != GateRelease || len(flush) != 1 {
		t.Fatalf("finish action=%v flush=%d", action, len(flush))
	}
}

func TestClientGateReleasesToolCallWithoutCountingArguments(t *testing.T) {
	gate := NewClientGate(4)
	payload := []byte(`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"` + strings.Repeat("x", 80) + `"}}]}}]}`)
	action, flush := gate.Push(payload)
	if action != GateRelease || len(flush) != 1 {
		t.Fatalf("tool call action=%v flush=%d", action, len(flush))
	}
}

func TestThinkingGateActiveSkipsLastAttemptAndThinkingOff(t *testing.T) {
	meta := map[string]any{
		cliproxyexecutor.MetadataThinkingRetry:           3,
		cliproxyexecutor.MetadataThinkingMinOutputTokens: 32,
	}
	body := []byte(`{"model":"grok-4.7","input":"hi"}`)
	active, minTokens := ThinkingGateActive(context.Background(), "grok-4.7", body, body, meta)
	if !active || minTokens != 32 {
		t.Fatalf("first attempt active=%v min=%d", active, minTokens)
	}
	ctx := cliproxyexecutor.WithThinkingFailures(context.Background(), 2)
	active, _ = ThinkingGateActive(ctx, "grok-4.7", body, body, meta)
	if active {
		t.Fatal("third attempt must pass through")
	}
	off := []byte(`{"model":"grok-4.7","reasoning":{"effort":"none"}}`)
	active, _ = ThinkingGateActive(context.Background(), "grok-4.7", off, off, meta)
	if active {
		t.Fatal("effort none must not latch")
	}
	active, _ = ThinkingGateActive(context.Background(), "grok-composer-2.5-fast", body, body, meta)
	if active {
		t.Fatal("composer without an explicit effort must not latch")
	}
	meta[cliproxyexecutor.MetadataThinkingRetry] = 0
	active, _ = ThinkingGateActive(context.Background(), "grok-4.7", body, body, meta)
	if active {
		t.Fatal("retry 0 must not latch")
	}
}

func TestEstimateTextTokens(t *testing.T) {
	if got := EstimateTextTokens("abcd"); got != 1 {
		t.Fatalf("ascii=%d", got)
	}
	if got := EstimateTextTokens("思考"); got != 2 {
		t.Fatalf("cjk=%d", got)
	}
}
