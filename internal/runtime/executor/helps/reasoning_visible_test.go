package helps

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestClientPayloadReasoningTextIgnoresTokenOnlyUsage(t *testing.T) {
	hidden := []byte(`data: {"choices":[{"delta":{"content":"391"}}],"usage":{"completion_tokens":27,"completion_tokens_details":{"reasoning_tokens":867}}}`)
	if clientPayloadHasReasoningText(hidden) {
		t.Fatal("reasoning_tokens without reasoning_content must not count")
	}
	visible := []byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"17*20=340\"}}]}")
	if !clientPayloadHasReasoningText(visible) {
		t.Fatal("reasoning_content text must count")
	}
	empty := []byte(`{"choices":[{"message":{"reasoning_content":"  "}}]}`)
	if clientPayloadHasReasoningText(empty) {
		t.Fatal("blank reasoning_content must not count")
	}
	event := []byte(`{"type":"response.reasoning_summary_text.delta","delta":"step"}`)
	if !clientPayloadHasReasoningText(event) {
		t.Fatal("reasoning delta event must count")
	}
}

func TestSealReasoningVisibilityIsExplicitFalse(t *testing.T) {
	reporter := NewUsageReporter(context.Background(), "xai", "grok-4.6", nil)
	reporter.ObserveClientReasoning([]byte(`{"choices":[{"delta":{"content":"391"}}]}`))
	if got := reporter.reasoningVisibleFor("grok-4.6"); got != nil {
		t.Fatalf("unsealed scan = %v, want nil", *got)
	}
	reporter.SealReasoningVisibility()
	got := reporter.reasoningVisibleFor("grok-4.6")
	if got == nil || *got {
		t.Fatal("sealed scan without reasoning text must be false")
	}
	other := NewUsageReporter(context.Background(), "xai", "grok-4.6", nil)
	other.ObserveClientReasoning([]byte(`{"choices":[{"delta":{"reasoning_content":"work"}}]}`))
	got = other.reasoningVisibleFor("grok-4.6")
	if got == nil || !*got {
		t.Fatal("reasoning_content must be visible before an explicit seal call")
	}
	rec := other.buildRecordForModel("side-image", usage.Detail{}, false, usage.Failure{})
	if rec.ReasoningVisible != nil {
		t.Fatal("side-model records must not inherit reasoning visibility")
	}
	primary := other.buildRecordForModel("grok-4.6", usage.Detail{}, false, usage.Failure{})
	if primary.ReasoningVisible == nil || !*primary.ReasoningVisible {
		t.Fatal("primary record must carry reasoning_visible=true")
	}
}
