package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIBackendListModelsParsesContextMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"list",
			"data":[
				{"id":"from-max","object":"model","created":1782428282,"owned_by":"vllm","max_model_len":65536,"root":"/models/from-max","parent":null},
				{"id":"from-context","context_length":32768},
				{"id":"from-provider","top_provider":{"context_length":131072}}
			]
		}`))
	}))
	defer server.Close()

	backend := NewOpenAIBackend(server.URL, 5, false, false)
	resp, err := backend.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(resp.Models) != 3 {
		t.Fatalf("len(models) = %d, want 3", len(resp.Models))
	}

	tests := map[string]int{
		"from-max":      65536,
		"from-context":  32768,
		"from-provider": 131072,
	}
	for _, model := range resp.Models {
		if model.ContextLength != tests[model.Name] {
			t.Fatalf("%s ContextLength = %d, want %d", model.Name, model.ContextLength, tests[model.Name])
		}
		if model.Details.ContextLength != tests[model.Name] {
			t.Fatalf("%s details.context_length = %d, want %d", model.Name, model.Details.ContextLength, tests[model.Name])
		}
	}
	if resp.Models[0].OpenAI == nil {
		t.Fatal("OpenAI metadata was not preserved")
	}
	if resp.Models[0].OpenAI.MaxModelLen != 65536 {
		t.Fatalf("MaxModelLen = %d, want 65536", resp.Models[0].OpenAI.MaxModelLen)
	}
	if resp.Models[0].OpenAI.Root != "/models/from-max" {
		t.Fatalf("Root = %q, want /models/from-max", resp.Models[0].OpenAI.Root)
	}
}

func TestOpenAIBackendShowModelSynthesizesContextLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "gemma4-31b", "max_model_len": 65536},
			},
		})
	}))
	defer server.Close()

	backend := NewOpenAIBackend(server.URL, 5, false, false)
	resp, err := backend.ShowModel(context.Background(), "gemma4-31b")
	if err != nil {
		t.Fatalf("ShowModel() error = %v", err)
	}
	if resp.ModelInfo["context_length"] != 65536 {
		t.Fatalf("model_info.context_length = %#v, want 65536", resp.ModelInfo["context_length"])
	}
	if resp.Details.ContextLength != 65536 {
		t.Fatalf("details.context_length = %d, want 65536", resp.Details.ContextLength)
	}
}
