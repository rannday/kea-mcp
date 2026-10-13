package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rannday/kea-mcp/config"
)

func connectTestClient(t *testing.T, server *sdkmcp.Server) *sdkmcp.ClientSession {
	t.Helper()
	st, ct := sdkmcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestPartialDeploymentAndToolRefresh(t *testing.T) {
	var mu sync.Mutex
	commands := []string{"status-get", "config-set", "custom-get"}
	var requests []map[string]any
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		service := "agent"
		if route, ok := body["service"].([]any); ok {
			service = route[0].(string)
		}
		if service != "dhcp4" {
			json.NewEncoder(w).Encode([]any{map[string]any{"result": 1, "text": "daemon unavailable"}})
			return
		}
		response := map[string]any{"result": 0, "text": "ok"}
		if body["command"] == "list-commands" {
			response["arguments"] = commands
		}
		json.NewEncoder(w).Encode([]any{response})
	}))
	defer endpoint.Close()
	srv, err := New(config.Config{APIMode: "control-agent", APIServices: []string{"dhcp4", "dhcp6"}, APIURL: endpoint.URL, APIUsername: "test", APIPassword: "test"})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectTestClient(t, srv)
	ctx := context.Background()
	assertTools := func(want ...string) {
		t.Helper()
		result, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := make(map[string]bool)
		for _, tool := range result.Tools {
			got[tool.Name] = true
		}
		if len(got) != len(want) {
			t.Fatalf("tools = %v, want %v", got, want)
		}
		for _, name := range want {
			if !got[name] {
				t.Fatalf("missing %s: %v", name, got)
			}
		}
	}
	assertTools("kea_discover", "kea_status_get", "kea_config_set")
	// Partial failures preserve successful resource responses.
	for _, uri := range []string{"kea://api/commands", "kea://api/examples/status-get"} {
		resource, err := cs.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Services []map[string]any `json:"services"`
			Examples []map[string]any `json:"examples"`
		}
		if err := json.Unmarshal([]byte(resource.Contents[0].Text), &payload); err != nil {
			t.Fatal(err)
		}
		entries := payload.Services
		if entries == nil {
			entries = payload.Examples
		}
		if len(entries) != 2 || entries[0]["error"] != nil || entries[1]["error"] == nil {
			t.Fatalf("partial resource %s: %+v", uri, payload)
		}
	}
	// An extra command field cannot turn an approved read tool into a write.
	if result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_status_get", Arguments: map[string]any{"service": "dhcp4", "command": "config-set"}}); err == nil && !result.IsError {
		t.Fatal("command override accepted")
	}
	for _, arguments := range []any{map[string]any{"x": "y"}, []any{"x", "y"}, "x"} {
		result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_status_get", Arguments: map[string]any{"service": "dhcp4", "arguments": arguments}})
		if err != nil || result.IsError {
			t.Fatalf("result=%v error=%v", result, err)
		}
		mu.Lock()
		last := requests[len(requests)-1]
		if last["command"] != "status-get" || !reflect.DeepEqual(last["arguments"], arguments) {
			t.Errorf("request=%v", last)
		}
		mu.Unlock()
	}
	mu.Lock()
	before := len(requests)
	mu.Unlock()
	for _, call := range []*sdkmcp.CallToolParams{
		{Name: "kea_raw_command", Arguments: map[string]any{"service": "dhcp4", "command": "config-set"}},
		{Name: "kea_status_get", Arguments: map[string]any{"service": "d2"}},
	} {
		result, err := cs.CallTool(ctx, call)
		if err == nil && !result.IsError {
			t.Fatalf("unexpected success: %v", call)
		}
	}
	mu.Lock()
	if len(requests) != before {
		t.Error("disabled tool or unconfigured service reached transport")
	}
	commands = []string{"config-get"}
	mu.Unlock()
	// Recheck availability even before a stale tool is removed by refresh.
	result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_status_get", Arguments: map[string]any{"service": "dhcp4"}})
	if err != nil || !result.IsError {
		t.Fatalf("stale command succeeded: %v %v", result, err)
	}
	result, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_discover", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("refresh: %v %v", result, err)
	}
	assertTools("kea_discover", "kea_config_get")
	mu.Lock()
	commands = []string{}
	mu.Unlock()
	result, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_discover", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("empty refresh: %v %v", result, err)
	}
	assertTools("kea_discover")
}

func TestDiscoveryRecoveryFromUnavailableEndpoint(t *testing.T) {
	var mu sync.Mutex
	available := false
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !available {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`[{"result":0,"arguments":["status-get"]}]`))
	}))
	defer endpoint.Close()
	srv, err := New(config.Config{APIMode: "direct", APIService: "dhcp4", APIURL: endpoint.URL, APIUsername: "test", APIPassword: "test"})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectTestClient(t, srv)
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "kea_discover" {
		t.Fatalf("offline inventory: %v %v", tools, err)
	}
	mu.Lock()
	available = true
	mu.Unlock()
	result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_discover", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("recovery: %v %v", result, err)
	}
	tools, err = cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 2 {
		t.Fatalf("recovered inventory: %v %v", tools, err)
	}
}
