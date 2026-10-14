package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rannday/kea-mcp/config"
	"github.com/rannday/kea-mcp/kea"
)

func TestMultipleEndpointsAuthentication(t *testing.T) {
	for _, source := range []string{"password", "password_env", "password_file"} {
		t.Run(source, func(t *testing.T) {
			var counts [3]atomic.Int64
			endpoints := make([]*httptest.Server, 3)
			for i := range endpoints {
				endpoints[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					counts[i].Add(1)
					user, password, basic := r.BasicAuth()
					switch i {
					case 0:
						if !basic || user != "shared" || password != "shared-password" {
							t.Error("DHCP4 did not use shared credentials")
						}
					case 1:
						if !basic || user != "six" || password != "six-password" {
							t.Error("DHCP6 did not use its own credentials")
						}
					case 2:
						if _, exists := r.Header["Authorization"]; exists {
							t.Error("DDNS received Authorization despite auth=none")
						}
					}
					var request map[string]any
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if _, exists := request["service"]; exists {
						t.Error("direct request included service routing")
					}
					arguments := any(map[string]any{"endpoint": i})
					if request["command"] == "list-commands" {
						arguments = []string{"status-get", "config-set"}
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"result": 0, "arguments": arguments}})
				}))
				t.Cleanup(endpoints[i].Close)
			}
			dir := t.TempDir()
			secretSetting := "password='six-password'"
			switch source {
			case "password_env":
				t.Setenv("KEA_SIX_PASSWORD", "six-password")
				secretSetting = "password_env='KEA_SIX_PASSWORD'"
			case "password_file":
				if err := os.WriteFile(filepath.Join(dir, "password.txt"), []byte("six-password\n"), 0600); err != nil {
					t.Fatal(err)
				}
				secretSetting = "password_file='password.txt'"
			}
			body := fmt.Sprintf("[api]\nmode='direct'\nread_only=true\nusername='shared'\npassword='shared-password'\n[api.endpoints.dhcp4]\nurl='%s'\n[api.endpoints.dhcp6]\nurl='%s'\nusername='six'\n%s\n[api.endpoints.d2]\nurl='%s'\nauth='none'\n[tools]\nraw_command=true\n", endpoints[0].URL, endpoints[1].URL, secretSetting, endpoints[2].URL)
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			srv, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			cs := connectTestClient(t, srv)
			discoverTestTools(t, cs)
			for i, service := range []string{"dhcp4", "dhcp6", "d2"} {
				before := [3]int64{counts[0].Load(), counts[1].Load(), counts[2].Load()}
				result, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "kea_status_get", Arguments: map[string]any{"service": service}})
				if err != nil || result.IsError {
					t.Fatalf("%s call failed: %v %v", service, result, err)
				}
				for j := range counts {
					want := before[j]
					if i == j {
						want += 2 // Availability probe and actual command.
					}
					if counts[j].Load() != want {
						t.Errorf("%s used incorrect endpoint: %d", service, j)
					}
				}
				beforeWrites := counts[i].Load()
				result, err = cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "kea_raw_command", Arguments: map[string]any{"service": service, "command": "config-set"}})
				if err != nil || !result.IsError || counts[i].Load() != beforeWrites {
					t.Fatalf("%s read-only write was not blocked before HTTP", service)
				}
			}
		})
	}
}

func TestUnauthenticatedSingleEndpoint(t *testing.T) {
	for _, mode := range []string{"control-agent", "direct"} {
		t.Run(mode, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, exists := r.Header["Authorization"]; exists {
					t.Error("unauthenticated request included Authorization")
				}
				var request map[string]any
				json.NewDecoder(r.Body).Decode(&request)
				_, routed := request["service"]
				if routed != (mode == "control-agent") {
					t.Error("incorrect wire routing")
				}
				json.NewEncoder(w).Encode([]any{map[string]any{"result": 0, "arguments": []string{"status-get"}}})
			}))
			defer endpoint.Close()
			cfg := config.Config{APIMode: mode, APIURL: endpoint.URL}
			if mode == "direct" {
				cfg.APIService = "dhcp4"
			} else {
				cfg.APIServices = []string{"dhcp4"}
			}
			srv, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			discoverTestTools(t, connectTestClient(t, srv))
		})
	}
}

func TestServiceTransportRejectsInvalidRoutes(t *testing.T) {
	routes := serviceTransport{}
	for _, services := range [][]kea.Service{nil, {kea.Services.DHCP4, kea.Services.DHCP6}, {kea.Services.DDNS}} {
		if err := routes.Call(context.Background(), kea.CommandRequest{Command: "status-get", Service: services}, nil); err == nil {
			t.Error("invalid route accepted")
		}
	}
}
