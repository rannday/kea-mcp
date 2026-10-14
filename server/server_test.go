package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rannday/kea-mcp/config"
)

func TestAPIOnlyMCP(t *testing.T) {
	for _, mode := range []string{"control-agent", "direct"} {
		for _, readOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/read-only=%v", mode, readOnly), func(t *testing.T) {
				var requests []map[string]json.RawMessage
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					requests = append(requests, body)
					if string(body["command"]) == `"list-commands"` {
						_, _ = w.Write([]byte(`[{"result":0,"arguments":["list-commands","status-get","config-get","lease4-get","config-set","shutdown","perfmon-get-all-durations","custom-get"]}]`))
					} else {
						_, _ = w.Write([]byte(`[{"result":0,"text":"ok"}]`))
					}
				}))
				defer endpoint.Close()
				server, err := New(config.Config{RawCommand: true, ReadOnly: readOnly, APIMode: mode, APIService: "dhcp4", APIURL: endpoint.URL, APIUsername: "test", APIPassword: "test"})
				if err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				st, ct := sdkmcp.NewInMemoryTransports()
				ss, err := server.Connect(ctx, st, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer ss.Close()
				client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil)
				cs, err := client.Connect(ctx, ct, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer cs.Close()
				discoverTestTools(t, cs)
				tools, err := cs.ListTools(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				wantTools := map[string]bool{"kea_discover": true, "kea_list_commands": true, "kea_lease4_get": true, "kea_status_get": true, "kea_config_get": true, "kea_raw_command": true}
				if !readOnly {
					for _, name := range []string{"kea_config_set", "kea_shutdown", "kea_perfmon_get_all_durations"} {
						wantTools[name] = true
					}
				}
				if len(tools.Tools) != len(wantTools) {
					t.Fatalf("tools = %v", tools.Tools)
				}
				for _, tool := range tools.Tools {
					if !wantTools[tool.Name] {
						t.Fatalf("unexpected tool %s", tool.Name)
					}
				}
				resources, err := cs.ListResources(ctx, nil)
				if err != nil || len(resources.Resources) != 2 {
					t.Fatalf("resources = %v, error = %v", resources, err)
				}
				templates, err := cs.ListResourceTemplates(ctx, nil)
				if err != nil || len(templates.ResourceTemplates) != 0 {
					t.Fatalf("templates = %v, error = %v", templates, err)
				}
				result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_status_get", Arguments: map[string]any{"service": "dhcp4"}})
				if err != nil || result.IsError {
					t.Fatalf("tool result = %v, error = %v", result, err)
				}
				before := len(requests)
				discovery, err := cs.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "kea://api/commands"})
				if err != nil {
					t.Fatal(err)
				}
				wantCalls := 4
				if mode == "direct" {
					wantCalls = 1
				}
				if len(requests)-before != wantCalls {
					t.Fatalf("command discovery made %d calls, want %d", len(requests)-before, wantCalls)
				}
				var payload struct {
					ReadOnly bool `json:"read_only"`
					Services []struct {
						Commands []string `json:"commands"`
					} `json:"services"`
				}
				if err := json.Unmarshal([]byte(discovery.Contents[0].Text), &payload); err != nil {
					t.Fatal(err)
				}
				wantCommands := 8
				if readOnly {
					wantCommands = 4
				}
				if payload.ReadOnly != readOnly || len(payload.Services) != wantCalls {
					t.Fatalf("unexpected discovery policy: %+v", payload)
				}
				for _, service := range payload.Services {
					if len(service.Commands) != wantCommands {
						t.Fatalf("unexpected discovered commands: %v", service.Commands)
					}
					if readOnly {
						for i, want := range []string{"list-commands", "status-get", "config-get", "lease4-get"} {
							if service.Commands[i] != want {
								t.Fatalf("unallowed discovery: %v", service.Commands)
							}
						}
					}
				}
				for _, command := range []string{"lease4-get", "config-set", "shutdown", "custom-get", "perfmon-get-all-durations"} {
					before = len(requests)
					result, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_raw_command", Arguments: map[string]any{
						"service": "dhcp4", "command": command, "arguments": map[string]any{"ip-address": "192.0.2.1"},
					}})
					denied := readOnly && command != "lease4-get"
					if err != nil || result.IsError != denied {
						t.Fatalf("%s: result=%v error=%v", command, result, err)
					}
					want := 2
					if denied {
						want = 0
					}
					if len(requests)-before != want {
						t.Fatalf("%s made %d HTTP calls, want %d", command, len(requests)-before, want)
					}
				}
				if _, err := cs.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "kea://api/examples/status-get"}); err != nil {
					t.Fatalf("read status resource: %v", err)
				}
				if mode == "direct" {
					for _, req := range requests {
						if _, ok := req["service"]; ok {
							t.Fatal("direct request included routing")
						}
					}
					before = len(requests)
					result, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_status_get", Arguments: map[string]any{"service": "dhcp6"}})
					if err != nil || !result.IsError || len(requests) != before {
						t.Fatalf("wrong daemon was not rejected: result=%v error=%v", result, err)
					}
				}
			})
		}
	}
}

func TestStartupDoesNotContactKea(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		<-release
	}))
	defer endpoint.Close()
	defer close(release)
	type built struct {
		server *sdkmcp.Server
		err    error
	}
	ready := make(chan built, 1)
	go func() {
		srv, err := New(config.Config{APIURL: endpoint.URL, APIUsername: "test", APIPassword: "test", ReadTimeoutSec: 90})
		ready <- built{srv, err}
	}()
	select {
	case result := <-ready:
		if result.err != nil {
			t.Fatal(result.err)
		}
		cs := connectTestClient(t, result.server)
		tools, err := cs.ListTools(context.Background(), nil)
		if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "kea_discover" {
			t.Fatalf("initial tools=%v error=%v", tools, err)
		}
		if calls.Load() != 0 {
			t.Fatalf("startup sent %d Kea requests", calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("MCP startup waited for blocked Kea endpoint")
	}
}

func TestMCPStructuredFailures(t *testing.T) {
	for _, mode := range []string{"control-agent", "direct"} {
		for _, code := range []int{1, 2, 4, 5, 99} {
			t.Run(fmt.Sprintf("%s/result=%d", mode, code), func(t *testing.T) {
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request map[string]json.RawMessage
					json.NewDecoder(r.Body).Decode(&request)
					if string(request["command"]) == `"list-commands"` {
						w.Write([]byte(`[{"result":0,"arguments":["config-set"]}]`))
						return
					}
					fmt.Fprintf(w, `[{"result":0,"text":"first succeeded"},{"result":%d,"text":"failed","arguments":{"failed-leases":[{"ip-address":"2001:db8::1"}],"id":9007199254740993}}]`, code)
				}))
				defer endpoint.Close()
				srv, err := New(config.Config{APIMode: mode, APIService: "dhcp4", APIURL: endpoint.URL, APIUsername: "test", APIPassword: "test"})
				if err != nil {
					t.Fatal(err)
				}
				cs := connectTestClient(t, srv)
				discoverTestTools(t, cs)
				result, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "kea_config_set", Arguments: map[string]any{"service": "dhcp4"}})
				if err != nil || !result.IsError || result.StructuredContent == nil {
					t.Fatalf("result=%v error=%v", result, err)
				}
				text := result.Content[0].(*sdkmcp.TextContent).Text
				var payload struct {
					Responses []json.RawMessage `json:"responses"`
					Error     string            `json:"error"`
				}
				if err := json.Unmarshal([]byte(text), &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload.Responses) != 2 || !strings.Contains(text, "failed-leases") || !strings.Contains(text, "9007199254740993") {
					t.Fatalf("failure envelopes lost: %s", text)
				}
				if code == 5 && !strings.Contains(payload.Error, "previous configuration was not restored") {
					t.Fatalf("rollback meaning missing: %s", text)
				}
			})
		}
	}
}

func TestConfiguredReadTimeout(t *testing.T) {
	var writes atomic.Int32
	release := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&request)
		if string(request["command"]) == `"list-commands"` {
			w.Write([]byte(`[{"result":0,"arguments":["config-set"]}]`))
			return
		}
		writes.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer endpoint.Close()
	defer close(release)
	srv, err := New(config.Config{APIMode: "direct", APIService: "dhcp4", APIURL: endpoint.URL, APIUsername: "test", APIPassword: "test", ReadTimeoutSec: 1})
	if err != nil {
		t.Fatal(err)
	}
	cs := connectTestClient(t, srv)
	discoverTestTools(t, cs)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_config_set", Arguments: map[string]any{"service": "dhcp4"}})
	if err != nil || !result.IsError || ctx.Err() != nil {
		t.Fatalf("configured timeout did not complete before caller deadline: %v %v", result, err)
	}
	text := result.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, `"outcome_unknown":true`) || !strings.Contains(text, "before retrying") || writes.Load() != 1 {
		t.Fatalf("timeout response=%s writes=%d", text, writes.Load())
	}
}

func TestMCPPreservesArgumentJSON(t *testing.T) {
	for _, mode := range []string{"control-agent", "direct"} {
		t.Run(mode, func(t *testing.T) {
			captured := make(chan json.RawMessage, 1)
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if string(request["command"]) == `"list-commands"` {
					_, _ = w.Write([]byte(`[{"result":0,"arguments":["config-set"]}]`))
					return
				}
				captured <- request["arguments"]
				_, _ = w.Write([]byte(`[{"result":0}]`))
			}))
			defer endpoint.Close()
			srv, err := New(config.Config{RawCommand: true, APIMode: mode, APIService: "dhcp4", APIServices: nil, APIURL: endpoint.URL, APIUsername: "test", APIPassword: "test"})
			if err != nil {
				t.Fatal(err)
			}
			cs := connectTestClient(t, srv)
			discoverTestTools(t, cs)
			for _, tool := range []string{"kea_config_set", "kea_raw_command"} {
				for _, arguments := range []string{`{"nested":{"id":9007199254740993,"max":9223372036854775807}}`, `[9007199254740993,-9007199254740993]`, `9007199254740993`, `null`, ""} {
					input := `{"service":"dhcp4"`
					if tool == "kea_raw_command" {
						input += `,"command":"config-set"`
					}
					if arguments != "" {
						input += `,"arguments":` + arguments
					}
					input += `}`
					result, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: tool, Arguments: json.RawMessage(input)})
					if err != nil || result.IsError {
						t.Fatalf("%s: %v %v", tool, result, err)
					}
					if got := string(<-captured); got != arguments {
						t.Fatalf("%s arguments changed: got %s, want %s", tool, got, arguments)
					}
				}
			}
		})
	}
}

func TestConfiguredResponseSizeLimit(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"result":0,"arguments":["status-get"]}]`))
	}))
	defer endpoint.Close()
	srv, err := New(config.Config{APIMode: "direct", APIService: "dhcp4", APIURL: endpoint.URL, APIUsername: "test", APIPassword: "test", MaxResponseBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	result, err := connectTestClient(t, srv).CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "kea_discover", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("discovery: %v %v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), "byte limit") {
		t.Fatalf("configured limit did not reach transport: %s %v", encoded, err)
	}
}
