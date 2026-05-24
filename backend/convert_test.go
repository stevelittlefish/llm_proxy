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
