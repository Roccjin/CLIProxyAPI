package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// xaiExplicitRefusalClientBody is the only body returned for an explicit xAI
// request refusal. The upstream sentence stays off the client response.
const xaiExplicitRefusalClientBody = `{"error":{"message":"Request blocked by upstream content policy","type":"invalid_request_error","code":"content_policy_violation"}}`

// isXAIExplicitRequestRefusal reports HTTP 403 permission-denied whose
// structured message is exactly xAI's request refusal sentence.
// Account entitlement and suspension markers win, including when a duplicate
// key or an unknown field also carries the apology.
func isXAIExplicitRequestRefusal(status int, body []byte) bool {
	if status != http.StatusForbidden || len(body) == 0 || !json.Valid(body) {
		return false
	}
	root, ok := decodeXAIJSONObject(body)
	if !ok {
		return false
	}
	if xaiHasAccountAccessCode(root) {
		return false
	}
	if !xaiHasPermissionDeniedCode(root) {
		return false
	}
	candidates := xaiRefusalStringCandidates(root)
	if len(candidates) == 0 {
		return false
	}
	for _, candidate := range candidates {
		if xaiAccountAccessPhrase(candidate) || !isXAIExplicitRefusalSentence(candidate) {
			return false
		}
	}
	return true
}

func isXAIExplicitRefusalSentence(value string) bool {
	switch normalizeXAIRefusalSentence(value) {
	case "i'm sorry, i can't help with that request",
		"i'm sorry, i cannot help with that request":
		return true
	default:
		return false
	}
}

func normalizeXAIRefusalSentence(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "\u2019", "'")
	value = strings.ReplaceAll(value, "\u2018", "'")
	value = strings.TrimRight(value, ".")
	return strings.TrimSpace(value)
}

func normalizeXAIErrorKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func isXAIAccountAccessCode(value string) bool {
	switch normalizeXAIErrorKey(value) {
	case "account_suspended",
		"account_disabled",
		"user_suspended",
		"user_disabled",
		"subscription_required",
		"entitlement_required",
		"not_entitled",
		"plan_required":
		return true
	default:
		return false
	}
}

func xaiAccountAccessPhrase(value string) bool {
	lower := strings.ToLower(value)
	for _, phrase := range []string{
		"account suspended",
		"account has been suspended",
		"account disabled",
		"account has been disabled",
		"user suspended",
		"user has been suspended",
		"subscription required",
		"entitlement required",
		"not entitled",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

type xaiJSONField struct {
	key string
	val any
}

type xaiJSONObject struct {
	fields []xaiJSONField
}

func decodeXAIJSONObject(body []byte) (xaiJSONObject, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	value, err := decodeXAIJSONValue(dec)
	if err != nil {
		return xaiJSONObject{}, false
	}
	obj, ok := value.(xaiJSONObject)
	return obj, ok
}

func decodeXAIJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		var obj xaiJSONObject
		for dec.More() {
			keyTok, errKey := dec.Token()
			if errKey != nil {
				return nil, errKey
			}
			key, _ := keyTok.(string)
			child, errChild := decodeXAIJSONValue(dec)
			if errChild != nil {
				return nil, errChild
			}
			obj.fields = append(obj.fields, xaiJSONField{key: key, val: child})
		}
		if _, errEnd := dec.Token(); errEnd != nil {
			return nil, errEnd
		}
		return obj, nil
	case '[':
		var items []any
		for dec.More() {
			child, errChild := decodeXAIJSONValue(dec)
			if errChild != nil {
				return nil, errChild
			}
			items = append(items, child)
		}
		if _, errEnd := dec.Token(); errEnd != nil {
			return nil, errEnd
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unexpected json delimiter %q", delim)
	}
}

func xaiRefusalStringCandidates(obj xaiJSONObject) []string {
	var candidates []string
	addString := func(value any) {
		text, ok := value.(string)
		if !ok {
			return
		}
		text = strings.TrimSpace(text)
		if text != "" {
			candidates = append(candidates, text)
		}
	}
	for _, field := range obj.fields {
		switch normalizeXAIErrorKey(field.key) {
		case "error":
			if _, ok := field.val.(string); ok {
				addString(field.val)
				continue
			}
			child, ok := field.val.(xaiJSONObject)
			if !ok {
				continue
			}
			for _, nested := range child.fields {
				switch normalizeXAIErrorKey(nested.key) {
				case "message", "error":
					addString(nested.val)
				}
			}
		case "message", "detail":
			addString(field.val)
		}
	}
	return candidates
}

func xaiHasAccountAccessCode(value any) bool {
	return xaiHasMarkerCode(value, isXAIAccountAccessCode)
}

func xaiHasPermissionDeniedCode(value any) bool {
	return xaiHasMarkerCode(value, func(code string) bool {
		return normalizeXAIErrorKey(code) == "permission_denied"
	})
}

func xaiHasMarkerCode(value any, match func(string) bool) bool {
	switch node := value.(type) {
	case xaiJSONObject:
		for _, field := range node.fields {
			switch normalizeXAIErrorKey(field.key) {
			case "code", "error_code", "type", "category", "reason":
				if text, ok := field.val.(string); ok && match(text) {
					return true
				}
			}
			if xaiHasMarkerCode(field.val, match) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if xaiHasMarkerCode(child, match) {
				return true
			}
		}
	}
	return false
}

// sanitizeXAIChatCompatFields drops fields xAI rejects on a Chat Completions
// body: presence_penalty and a name on any non-user message. User names,
// function names, and Responses input items are left alone. Other providers
// never call this.
func sanitizeXAIChatCompatFields(body []byte) []byte {
	if len(body) == 0 || !json.Valid(body) {
		return body
	}
	out := body
	for _, field := range []string{"presence_penalty", "presencePenalty"} {
		if gjson.GetBytes(out, field).Exists() {
			next, err := sjson.DeleteBytes(out, field)
			if err == nil {
				out = next
			}
		}
	}
	messages := gjson.GetBytes(out, "messages")
	if !messages.IsArray() {
		return out
	}
	messages.ForEach(func(key, message gjson.Result) bool {
		role := strings.ToLower(strings.TrimSpace(message.Get("role").String()))
		if role == "user" || !message.Get("name").Exists() {
			return true
		}
		next, err := sjson.DeleteBytes(out, "messages."+key.String()+".name")
		if err == nil {
			out = next
		}
		return true
	})
	return out
}
