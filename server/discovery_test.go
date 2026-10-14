package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rannday/kea-mcp/config"
	"github.com/rannday/kea-mcp/kea"
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

func discoverTestTools(t *testing.T, cs *sdkmcp.ClientSession) {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "kea_discover", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("discovery: %v %v", result, err)
	}
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
	initial, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertTools := func() {
		t.Helper()
		result, err := cs.ListTools(ctx, nil)
		if err != nil || !reflect.DeepEqual(result.Tools, initial.Tools) {
			t.Fatalf("catalog changed: %v %v", result, err)
		}
	}
	discoverTestTools(t, cs)
	assertTools()
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
	// Recheck availability before dispatch even when the tool remains registered.
	mu.Lock()
	before = len(requests)
	mu.Unlock()
	result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_status_get", Arguments: map[string]any{"service": "dhcp4"}})
	if err != nil || !result.IsError {
		t.Fatalf("stale command succeeded: %v %v", result, err)
	}
	mu.Lock()
	if len(requests) != before+1 || requests[len(requests)-1]["command"] != "list-commands" {
		t.Error("unavailable command reached dispatch")
	}
	mu.Unlock()
	result, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_discover", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("refresh: %v %v", result, err)
	}
	assertTools()
	mu.Lock()
	commands = []string{}
	mu.Unlock()
	result, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_discover", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("empty refresh: %v %v", result, err)
	}
	assertTools()
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
	if err != nil || len(tools.Tools) != len(kea.KnownCommands())+1 {
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
	if err != nil || len(tools.Tools) != len(kea.KnownCommands())+1 {
		t.Fatalf("recovered inventory: %v %v", tools, err)
	}
}

type discoveryTestTransport func(context.Context, kea.CommandRequest, any) error

func (f discoveryTestTransport) Call(ctx context.Context, req kea.CommandRequest, out any) error {
	return f(ctx, req, out)
}

func TestConcurrentDiscoverySerializesRefreshes(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	s := &Server{
		services: []kea.Service{kea.Services.DHCP4},
		mcp:      sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil),
	}
	s.kea = kea.NewClient(discoveryTestTransport(func(_ context.Context, req kea.CommandRequest, out any) error {
		if req.Command != "list-commands" {
			t.Errorf("unexpected command %q", req.Command)
		}
		command := "config-get"
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			command = "status-get"
		}
		return json.Unmarshal([]byte(`[{"result":0,"arguments":["`+command+`"]}]`), out)
	}))
	type discoveryResult struct {
		payload map[string]any
		err     error
	}
	results := make(chan discoveryResult, 2)
	go func() { payload, err := s.discover(context.Background()); results <- discoveryResult{payload, err} }()
	<-firstStarted
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		payload, err := s.discover(context.Background())
		results <- discoveryResult{payload, err}
	}()
	<-secondStarted
	// The first refresh remains blocked while another caller starts.
	if s.discoveryMu.TryLock() {
		s.discoveryMu.Unlock()
		t.Error("active discovery does not hold its serialization lock")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("overlapping refresh reached transport: calls = %d, want 1", got)
	}
	close(releaseFirst)
	seen := make(map[string]bool)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		entries := result.payload["services"].([]map[string]any)
		seen[entries[0]["commands"].([]string)[0]] = true
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	if !reflect.DeepEqual(seen, map[string]bool{"config-get": true, "status-get": true}) {
		t.Fatalf("availability reports = %v", seen)
	}
}

func TestCancelledDiscoveryPreservesInventory(t *testing.T) {
	var refreshing atomic.Bool
	secondStarted := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req kea.CommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		command := "status-get"
		if refreshing.Load() {
			command = "config-get"
			if req.Service[0] == kea.Services.DHCP6 {
				close(secondStarted)
				<-r.Context().Done()
				return
			}
		}
		_, _ = w.Write([]byte(`[{"result":0,"arguments":["` + command + `"]}]`))
	}))
	defer endpoint.Close()
	s := &Server{
		kea:      kea.NewHTTP(endpoint.URL),
		services: []kea.Service{kea.Services.DHCP4, kea.Services.DHCP6},
		mcp:      sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.discover(ctx); err != nil {
		t.Fatal(err)
	}
	s.registerCommandTools()
	cs := connectTestClient(t, s.mcp)
	initial, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertInventory := func() {
		t.Helper()
		tools, err := cs.ListTools(ctx, nil)
		if err != nil || !reflect.DeepEqual(tools.Tools, initial.Tools) {
			t.Fatalf("inventory = %v, error = %v; want original status-get", tools, err)
		}
	}
	assertInventory()
	refreshing.Store(true)
	refreshCtx, cancelRefresh := context.WithCancel(ctx)
	defer cancelRefresh()
	done := make(chan error, 1)
	go func() { _, err := s.discover(refreshCtx); done <- err }()
	select {
	case <-secondStarted:
	case <-ctx.Done():
		t.Fatal("refresh did not reach the second service")
	}
	cancelRefresh()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled refresh error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled refresh did not finish")
	}
	// Cancellation must not change the registered catalog.
	assertInventory()
	refreshing.Store(false)
	if _, err := s.discover(ctx); err != nil {
		t.Fatalf("discovery after cancellation: %v", err)
	}
	assertInventory()
}

func TestDiscoveryKeepsToolCatalogStable(t *testing.T) {
	var mu sync.Mutex
	command := "status-get"
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if command == "" {
			_, _ = w.Write([]byte(`[{"result":0,"arguments":[]}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"result":0,"arguments":["` + command + `"]}]`))
	}))
	defer endpoint.Close()
	srv, err := New(config.Config{APIMode: "direct", APIService: "dhcp4", APIURL: endpoint.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, ct := sdkmcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	changed := make(chan struct{}, 1)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "1"}, &sdkmcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			select {
			case changed <- struct{}{}:
			default:
			}
		},
	})
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	initial, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []string{"status-get", "config-get", ""} {
		mu.Lock()
		command = next
		mu.Unlock()
		result, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "kea_discover", Arguments: map[string]any{}})
		if err != nil || result.IsError {
			t.Fatalf("discovery: %v %v", result, err)
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Services []struct {
				Commands []string          `json:"commands"`
				Tools    map[string]string `json:"tools"`
			} `json:"services"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Services) != 1 {
			t.Fatalf("services = %v", payload.Services)
		}
		wantCommands := []string{}
		wantTools := map[string]string{}
		if next != "" {
			wantCommands = append(wantCommands, next)
			wantTools[next] = commandToolName(next)
		}
		if !reflect.DeepEqual(payload.Services[0].Commands, wantCommands) || !reflect.DeepEqual(payload.Services[0].Tools, wantTools) {
			t.Fatalf("availability = %+v", payload.Services[0])
		}
		tools, err := cs.ListTools(ctx, nil)
		if err != nil || !reflect.DeepEqual(tools.Tools, initial.Tools) {
			t.Fatalf("catalog changed: %v %v", tools, err)
		}
	}
	select {
	case <-changed:
		t.Fatal("discovery sent notifications/tools/list_changed for an unchanged catalog")
	case <-time.After(100 * time.Millisecond):
	}
}
