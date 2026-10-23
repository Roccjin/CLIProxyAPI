package executor

import (
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/clienterror"
	"github.com/tidwall/gjson"
)

func TestXAIStatusErr_ExplicitRequestRefusalIsSanitizedRequestFault(t *testing.T) {
	bodies := []string{
		`{"code":"permission-denied","error":"I'm sorry, I can't help with that request."}`,
		`{"code":"permission_denied","error":{"message":"I'm sorry, I can't help with that request."}}`,
		`{"code":"permission-denied","error":"I\u2019m sorry, I can\u2019t help with that request."}`,
		`{"code":"permission-denied","message":"I\u2018m sorry, I cannot help with that request."}`,
	}
	for _, body := range bodies {
		err := xaiStatusErr(http.StatusForbidden, []byte(body))
		if err.StatusCode() != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 for %s", err.StatusCode(), body)
		}
		if err.Error() != xaiExplicitRefusalClientBody {
			t.Fatalf("client body = %s, want sanitized refusal", err.Error())
		}
		if strings.Contains(strings.ToLower(err.Error()), "sorry") {
			t.Fatalf("upstream apology leaked to client: %s", err.Error())
		}
		if !clienterror.IsRequestFault(err.StatusCode(), err) {
			t.Fatalf("explicit refusal should be a request fault: %s", body)
		}
	}
}

func TestXAIStatusErr_ExplicitRefusalDoesNotHideAccountFailure(t *testing.T) {
	bodies := []string{
		`{"code":"permission_denied","error":"model access is not allowed for this account"}`,
		`{"code":"account_suspended","error":"I'm sorry, I can't help with that request."}`,
		`{"code":"subscription_required","error":"I'm sorry, I can't help with that request."}`,
		`{"code":"permission-denied","error":"I'm sorry, I can't help with that request. subscription required"}`,
		`{"code":"permission-denied","debug_note":"I'm sorry, I can't help with that request.","error":"Access to the chat endpoint is denied"}`,
		`{"code":"permission-denied","error":"Access to the chat endpoint is denied","detail":{"debug_note":"I'm sorry, I can't help with that request."}}`,
		`{"error":{"code":"account_suspended","message":"I'm sorry, I can't help with that request."}}`,
		`{"code":"account_suspended","code":"permission-denied","error":"I'm sorry, I can't help with that request."}`,
		`{"code":"permission-denied","error":"I can't help"}`,
		`I'm sorry, I can't help with that request.`,
	}
	for _, body := range bodies {
		err := xaiStatusErr(http.StatusForbidden, []byte(body))
		if err.Error() == xaiExplicitRefusalClientBody {
			t.Fatalf("account or non-refusal body was rewritten: %s", body)
		}
		if clienterror.IsRequestFault(err.StatusCode(), err) {
			t.Fatalf("non-refusal 403 was treated as a request fault: %s", body)
		}
	}
}

func TestSanitizeXAIChatCompatFields_StripsPenaltyAndNonUserNames(t *testing.T) {
	body := []byte(`{"model":"grok-4.7","presence_penalty":0,"presencePenalty":0.2,"frequency_penalty":0.3,"temperature":0.1,"stop":["END"],"messages":[{"role":"system","name":"rules","content":"be brief"},{"role":"user","name":"alice","content":"hello"},{"role":"assistant","name":"bot","content":"ok","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`)
	got := sanitizeXAIChatCompatFields(body)
	if gjson.GetBytes(got, "presence_penalty").Exists() || gjson.GetBytes(got, "presencePenalty").Exists() {
		t.Fatalf("presence penalty survived: %s", got)
	}
	if gjson.GetBytes(got, "frequency_penalty").Float() != 0.3 {
		t.Fatalf("frequency_penalty = %s", gjson.GetBytes(got, "frequency_penalty").Raw)
	}
	if gjson.GetBytes(got, "messages.0.name").Exists() || gjson.GetBytes(got, "messages.2.name").Exists() {
		t.Fatalf("non-user names survived: %s", got)
	}
	if gjson.GetBytes(got, "messages.1.name").String() != "alice" {
		t.Fatalf("user name = %q", gjson.GetBytes(got, "messages.1.name").String())
	}
	if gjson.GetBytes(got, "messages.2.tool_calls.0.function.name").String() != "lookup" {
		t.Fatalf("function name changed: %s", got)
	}
	if gjson.GetBytes(got, "tools.0.function.name").String() != "lookup" {
		t.Fatalf("tool name changed: %s", got)
	}
}

func TestSanitizeXAIChatCompatFields_LeavesResponsesInputNames(t *testing.T) {
	body := []byte(`{"model":"grok-4.7","presence_penalty":0.1,"input":[{"role":"system","name":"rules","content":"be brief"},{"role":"user","name":"alice","content":"hello"}]}`)
	got := sanitizeXAIChatCompatFields(body)
	if gjson.GetBytes(got, "presence_penalty").Exists() {
		t.Fatalf("presence_penalty survived on responses body: %s", got)
	}
	if gjson.GetBytes(got, "input.0.name").String() != "rules" || gjson.GetBytes(got, "input.1.name").String() != "alice" {
		t.Fatalf("responses input names changed: %s", got)
	}
}
