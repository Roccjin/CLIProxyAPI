package helps

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

// ObserveClientReasoning records whether a client-facing chunk contains
// non-empty reasoning text. reasoning_tokens are intentionally ignored:
// xAI can spend them without ever sending reasoning_content.
func (r *UsageReporter) ObserveClientReasoning(payloads ...[]byte) {
	if r == nil || r.reasoningVisible {
		return
	}
	for _, payload := range payloads {
		if clientPayloadHasReasoningText(payload) {
			r.reasoningVisible = true
			r.reasoningVisibleSet = true
			return
		}
	}
}

// SealReasoningVisibility marks the response as fully scanned.
// A scan that never saw reasoning text is an explicit false, not "unknown".
func (r *UsageReporter) SealReasoningVisibility() {
	if r == nil {
		return
	}
	r.reasoningVisibleSet = true
}

func (r *UsageReporter) reasoningVisibleFor(model string) *bool {
	if r == nil || !r.reasoningVisibleSet || model != r.model {
		return nil
	}
	value := r.reasoningVisible
	return &value
}

func clientPayloadHasReasoningText(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
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
		if jsonValueHasReasoningText(gjson.ParseBytes(line)) {
			return true
		}
	}
	return false
}

func jsonValueHasReasoningText(v gjson.Result) bool {
	switch {
	case v.IsArray():
		for _, el := range v.Array() {
			if jsonValueHasReasoningText(el) {
				return true
			}
		}
		return false
	case v.IsObject():
		typ := strings.ToLower(v.Get("type").String())
		if strings.Contains(typ, "reasoning") || strings.Contains(typ, "thinking") {
			if strings.TrimSpace(v.Get("delta").String()) != "" || strings.TrimSpace(v.Get("text").String()) != "" {
				return true
			}
		}
		found := false
		v.ForEach(func(key, val gjson.Result) bool {
			switch key.String() {
			case "reasoning_content", "thinking_content", "thinking":
				if val.Type == gjson.String && strings.TrimSpace(val.String()) != "" {
					found = true
					return false
				}
			}
			if jsonValueHasReasoningText(val) {
				found = true
				return false
			}
			return true
		})
		return found
	default:
		return false
	}
}
