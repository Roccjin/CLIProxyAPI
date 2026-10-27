package helps

import (
	"bytes"
	"context"
	"strings"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

// GateAction is what the thinking latch should do with the chunks held so far.
type GateAction int

const (
	// GateHold keeps the chunk off the client until reasoning, a tool call, or
	// enough prose arrives.
	GateHold GateAction = iota
	// GateRelease flushes held chunks and stops latching.
	GateRelease
	// GateRetry drops held chunks. The caller cancels upstream and switches
	// credentials. The client has not been written yet.
	GateRetry
)

// ClientGate buffers client-facing chunks until it can tell a healthy thinking
// stream from prose that never carried reasoning text.
type ClientGate struct {
	MinOutputTokens int
	contentTokens   int
	released        bool
	retried         bool
	held            [][]byte
}

// NewClientGate starts a latch. minOutputTokens <= 0 uses 32, matching the
// egress guard's short-reply floor.
func NewClientGate(minOutputTokens int) *ClientGate {
	if minOutputTokens <= 0 {
		minOutputTokens = 32
	}
	return &ClientGate{MinOutputTokens: minOutputTokens}
}

// ContentTokens is the prose estimate accumulated so far.
func (g *ClientGate) ContentTokens() int {
	if g == nil {
		return 0
	}
	return g.contentTokens
}

// Push inspects one client chunk. Release and retry return every chunk that
// should be written now; hold returns nothing.
func (g *ClientGate) Push(payload []byte) (GateAction, [][]byte) {
	if g == nil || g.released {
		if len(payload) == 0 {
			return GateRelease, nil
		}
		return GateRelease, [][]byte{payload}
	}
	if g.retried {
		return GateRetry, nil
	}
	reasoning, tool, tokens := ScanClientChunk(payload)
	if reasoning || tool {
		g.released = true
		flush := append(g.held, payload)
		g.held = nil
		return GateRelease, flush
	}
	g.contentTokens += tokens
	if g.contentTokens >= g.MinOutputTokens {
		g.retried = true
		g.held = nil
		return GateRetry, nil
	}
	if len(payload) > 0 {
		g.held = append(g.held, payload)
	}
	return GateHold, nil
}

// Finish releases a stream that ended before the prose floor. A retry decision
// already made stays a retry and flushes nothing.
func (g *ClientGate) Finish() (GateAction, [][]byte) {
	if g == nil || g.released || g.retried {
		if g != nil && g.retried {
			return GateRetry, nil
		}
		return GateRelease, nil
	}
	g.released = true
	flush := g.held
	g.held = nil
	return GateRelease, flush
}

// ThinkingGateActive reports whether this attempt should latch. The last
// allowed attempt, an explicit thinking-off request, and non-Grok models pass
// through so the client still receives a body.
func ThinkingGateActive(ctx context.Context, model string, original, upstream []byte, metadata map[string]any) (bool, int) {
	budget := metadataInt(metadata, cliproxyexecutor.MetadataThinkingRetry)
	if budget <= 0 {
		return false, 0
	}
	failures := cliproxyexecutor.ThinkingFailures(ctx)
	if failures >= cliproxyexecutor.ThinkingFailuresRelease || failures+1 >= budget {
		return false, 0
	}
	if !modelExpectsVisibleThinking(model, original, upstream) {
		return false, 0
	}
	minTokens := metadataInt(metadata, cliproxyexecutor.MetadataThinkingMinOutputTokens)
	if minTokens <= 0 {
		minTokens = 32
	}
	return true, minTokens
}

func modelExpectsVisibleThinking(model string, original, upstream []byte) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" || !strings.Contains(name, "grok") {
		return false
	}
	effort := thinking.ExtractTranslatedReasoningEffort(upstream, "xai")
	if effort == "" {
		effort = thinking.ExtractReasoningEffort(original, "xai", model)
	}
	if strings.EqualFold(strings.TrimSpace(effort), "none") {
		return false
	}
	// Composer does not promise visible reasoning unless the client asked.
	if strings.Contains(name, "composer") && strings.TrimSpace(effort) == "" {
		return false
	}
	return true
}

func metadataInt(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	switch v := metadata[key].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}

// ScanClientChunk reports reasoning text, a tool call, and estimated prose
// tokens. Reasoning token counts are ignored: a degraded account can spend
// them without ever sending reasoning text.
func ScanClientChunk(payload []byte) (reasoning bool, tool bool, contentTokens int) {
	if len(payload) == 0 {
		return false, false, 0
	}
	reasoning = clientPayloadHasReasoningText(payload)
	for _, line := range bytes.Split(payload, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		}
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) || (line[0] != '{' && line[0] != '[') {
			continue
		}
		if !gjson.ValidBytes(line) {
			continue
		}
		toolSeen, tokens := walkClientJSON(gjson.ParseBytes(line), "")
		if toolSeen {
			tool = true
		}
		contentTokens += tokens
	}
	return reasoning, tool, contentTokens
}

func walkClientJSON(v gjson.Result, eventType string) (tool bool, contentTokens int) {
	switch {
	case v.IsArray():
		for _, el := range v.Array() {
			childTool, childTokens := walkClientJSON(el, eventType)
			if childTool {
				tool = true
			}
			contentTokens += childTokens
		}
	case v.IsObject():
		typ := strings.ToLower(v.Get("type").String())
		if typ == "" {
			typ = eventType
		}
		if isToolEventType(typ) {
			tool = true
		}
		v.ForEach(func(key, val gjson.Result) bool {
			k := key.String()
			switch k {
			case "tool_calls", "tool_call", "function_call":
				if jsonPresent(val) {
					tool = true
				}
			case "content":
				if val.Type == gjson.String && !isReasoningEventType(typ) {
					contentTokens += EstimateTextTokens(val.String())
				}
			case "delta", "text":
				if val.Type == gjson.String && strings.Contains(typ, "output_text") && !isReasoningEventType(typ) {
					contentTokens += EstimateTextTokens(val.String())
				}
			}
			childTool, childTokens := walkClientJSON(val, typ)
			if childTool {
				tool = true
			}
			contentTokens += childTokens
			return true
		})
	}
	return tool, contentTokens
}

func jsonPresent(v gjson.Result) bool {
	if !v.Exists() || v.Type == gjson.Null {
		return false
	}
	raw := strings.TrimSpace(v.Raw)
	return raw != "" && raw != "[]" && raw != "{}" && raw != `""`
}

func isToolEventType(typ string) bool {
	switch typ {
	case "function_call", "tool_call", "tool_use":
		return true
	default:
		return strings.Contains(typ, "function_call")
	}
}

func isReasoningEventType(typ string) bool {
	return strings.Contains(typ, "reasoning") || strings.Contains(typ, "thinking")
}

// EstimateTextTokens approximates output tokens without a model tokenizer.
// ASCII is about four characters per token; other scripts count as one each.
func EstimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	ascii := 0
	other := 0
	for _, r := range text {
		if r < 128 {
			ascii++
			continue
		}
		other++
	}
	tokens := ascii/4 + other
	if tokens == 0 && (ascii > 0 || other > 0) {
		return 1
	}
	if !utf8.ValidString(text) && tokens == 0 {
		return 1
	}
	return tokens
}
