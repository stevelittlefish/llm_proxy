package backend

import (
	"encoding/json"
	"testing"

	"llm_proxy/models"
)

func TestConvertMessagesToOpenAI_ToolCallGetsID(t *testing.T) {
	// Simulates what Home Assistant sends: assistant message with tool_calls but no id
	input := []models.Message{
		{
			Role: "assistant",
			ToolCalls: []interface{}{
				map[string]interface{}{
					"function": map[string]interface{}{
						"name":      "HassTurnOff",
						"arguments": map[string]interface{}{"name": "Office Lights"},
					},
				},
			},
		},
	}

	result := convertMessagesToOpenAI(input)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if len(result[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result[0].ToolCalls))
	}

	tc, ok := result[0].ToolCalls[0].(map[string]interface{})
	if !ok {
		t.Fatal("tool call is not a map")
	}

	id, hasID := tc["id"].(string)
	if !hasID || id == "" {
		t.Errorf("expected non-empty id on tool call, got: %v", tc["id"])
	}

	tcType, _ := tc["type"].(string)
	if tcType != "function" {
		t.Errorf("expected type=function, got %q", tcType)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	t.Logf("converted messages:\n%s", out)
}

func TestConvertMessagesToOpenAI_ToolCallIDPropagatedToResult(t *testing.T) {
	// Simulates HA sending a tool call (no id) followed by a tool result (no tool_call_id).
	// The proxy must assign matching IDs so vLLM's template can correlate them.
	input := []models.Message{
		{
			Role: "assistant",
			ToolCalls: []interface{}{
				map[string]interface{}{
					"function": map[string]interface{}{
						"name":      "HassTurnOn",
						"arguments": map[string]interface{}{"area": "Office"},
					},
				},
			},
		},
		{
			Role:    "tool",
			Content: `{"success":true}`,
		},
	}

	result := convertMessagesToOpenAI(input)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}

	tc, ok := result[0].ToolCalls[0].(map[string]interface{})
	if !ok {
		t.Fatal("tool call is not a map")
	}
	callID, _ := tc["id"].(string)
	if callID == "" {
		t.Fatal("expected tool call to have an id")
	}

	if result[1].ToolCallID == "" {
		t.Error("expected tool result message to have tool_call_id set")
	}
	if result[1].ToolCallID != callID {
		t.Errorf("tool_call_id mismatch: call id=%q, result tool_call_id=%q", callID, result[1].ToolCallID)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	t.Logf("converted messages:\n%s", out)
}
