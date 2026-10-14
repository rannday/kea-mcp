//go:build integration

// Package integration exercises the packaged STDIO executable against real Kea containers.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type envelope struct {
	Result    int             `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
	Text      string          `json:"text"`
}

type commandResult struct {
	Service   string     `json:"service"`
	Command   string     `json:"command"`
	Responses []envelope `json:"responses"`
}

type discovery struct {
	ReadOnly bool `json:"read_only"`
	Services []struct {
		Service  string            `json:"service"`
		Error    string            `json:"error"`
		Commands []string          `json:"commands"`
		Tools    map[string]string `json:"tools"`
	} `json:"services"`
}

type lab struct {
	security *securityFixture
	mode     string
	compose  string
	prefix   []string
	env      []string
	urls     map[string]string
}

func TestDockerIntegration(t *testing.T) {
	binary := os.Getenv("KEA_MCP_BINARY")
	if binary == "" {
		t.Fatal("KEA_MCP_BINARY must name the extracted buildx executable; integration tests never substitute an in-process server")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("integration executable: %v", err)
	}

	stack := startLab(t)
	session := connect(t, binary, stack, 8<<20)
	if stack.security.profile != "plain" {
		t.Run("native_authentication_and_tls", func(t *testing.T) {
			testSecurity(t, binary, stack)
		})
	}
	initialTools := toolNames(t, session)

	t.Run("startup_catalog_and_live_discovery", func(t *testing.T) {
		for _, name := range []string{"kea_discover", "kea_status_get", "kea_config_get", "kea_raw_command"} {
			if !slices.Contains(initialTools, name) {
				t.Errorf("startup catalog is missing %s", name)
			}
		}
		for _, name := range []string{"kea_config_set", "kea_shutdown", "kea_lease4_add", "kea_lease6_add"} {
			if slices.Contains(initialTools, name) {
				t.Errorf("read-only catalog exposes write tool %s", name)
			}
		}
		assertDiscovery(t, session, "")
		if !slices.Equal(initialTools, toolNames(t, session)) {
			t.Fatal("discovery changed the startup catalog")
		}
		for _, service := range []string{"dhcp4", "dhcp6", "d2"} {
			result := successfulCommand(t, session, "kea_version_get", service)
			t.Logf("%s: %s", service, result.Responses[0].Text)
			if stack.mode == "control-agent" && result.Responses[0].Text != "2.6.1" {
				t.Fatalf("legacy lab must run Kea 2.6.1, got %q", result.Responses[0].Text)
			}
			status := successfulCommand(t, session, "kea_status_get", service)
			var args map[string]any
			decode(t, status.Responses[0].Arguments, &args)
			if _, ok := args["pid"]; !ok {
				t.Fatalf("%s status response lacks a daemon pid: %s", service, status.Responses[0].Arguments)
			}
		}
	})

	t.Run("complete_large_configuration_round_trip", func(t *testing.T) {
		for _, service := range []string{"dhcp4", "dhcp6", "d2"} {
			t.Run(service, func(t *testing.T) {
				direct, err := stack.request(t.Context(), service, "config-get")
				if err != nil {
					t.Fatal(err)
				}
				got := successfulCommand(t, session, "kea_config_get", service)
				if !equalJSON(t, direct[0].Arguments, got.Responses[0].Arguments) {
					t.Fatal("MCP configuration differs from the live daemon's complete configuration")
				}
				if service != "d2" {
					assertLargeFixture(t, service, got.Responses[0].Arguments)
				}
				t.Logf("verified %d-byte live configuration", len(got.Responses[0].Arguments))
			})
		}
	})

	t.Run("real_lease_hook_and_empty_results", func(t *testing.T) {
		for _, family := range []string{"4", "6"} {
			result := successfulCommand(t, session, "kea_lease"+family+"_get_all", "dhcp"+family)
			var args struct {
				Leases []json.RawMessage `json:"leases"`
			}
			decode(t, result.Responses[0].Arguments, &args)
			if len(args.Leases) != 0 {
				t.Fatalf("fresh test volume contains %d leases", len(args.Leases))
			}
		}
	})

	t.Run("resources_use_live_daemons", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "kea://api/commands"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("resource contents: %d", len(result.Contents))
		}
		var got discovery
		decode(t, []byte(result.Contents[0].Text), &got)
		checkDiscovery(t, got, "")
	})

	t.Run("read_only_rejects_real_configuration_write", func(t *testing.T) {
		before := successfulCommand(t, session, "kea_config_hash_get", "dhcp4")
		configuration := successfulCommand(t, session, "kea_config_get", "dhcp4")
		var args map[string]any
		decode(t, configuration.Responses[0].Arguments, &args)
		args["Dhcp4"].(map[string]any)["valid-lifetime"] = 9999
		result := call(t, session, "kea_raw_command", map[string]any{
			"service": "dhcp4", "command": "config-set", "arguments": args,
		})
		if !result.IsError || !strings.Contains(resultText(result), "read-only") {
			t.Fatalf("raw configuration write was not denied: %s", resultText(result))
		}
		after := successfulCommand(t, session, "kea_config_hash_get", "dhcp4")
		if !equalJSON(t, before.Responses[0].Arguments, after.Responses[0].Arguments) {
			t.Fatal("daemon configuration changed after a denied write")
		}
	})

	t.Run("large_response_limit_is_enforced", func(t *testing.T) {
		limited := connect(t, binary, stack, 4096)
		// list-commands and status fit; the actual large config-get does not.
		successfulCommand(t, limited, "kea_status_get", "dhcp4")
		result := call(t, limited, "kea_config_get", map[string]any{"service": "dhcp4"})
		if !result.IsError || !strings.Contains(resultText(result), "4096") {
			t.Fatalf("large live response was not rejected by the configured limit: %s", resultText(result))
		}
		successfulCommand(t, limited, "kea_status_get", "dhcp4")
	})

	t.Run("daemon_outage_and_recovery", func(t *testing.T) {
		before := successfulCommand(t, session, "kea_config_hash_get", "dhcp6")
		stack.run(t, "stop", "-t", "1", "dhcp6")
		assertDiscovery(t, session, "dhcp6")
		successfulCommand(t, session, "kea_status_get", "dhcp4")
		successfulCommand(t, session, "kea_status_get", "d2")
		if !call(t, session, "kea_status_get", map[string]any{"service": "dhcp6"}).IsError {
			t.Fatal("stopped DHCP6 daemon reported success")
		}
		if !slices.Equal(initialTools, toolNames(t, session)) {
			t.Fatal("daemon outage discarded tools")
		}
		stack.run(t, "start", "dhcp6")
		stack.waitReady(t, "dhcp6")
		assertDiscovery(t, session, "")
		after := successfulCommand(t, session, "kea_config_hash_get", "dhcp6")
		if !equalJSON(t, before.Responses[0].Arguments, after.Responses[0].Arguments) {
			t.Fatal("DHCP6 configuration changed across daemon restart")
		}
		if !slices.Equal(initialTools, toolNames(t, session)) {
			t.Fatal("recovery changed the startup catalog")
		}
	})
}

func startLab(t *testing.T) *lab {
	t.Helper()
	mode := os.Getenv("KEA_INTEGRATION_MODE")
	if mode == "" {
		mode = "direct"
	}
	if mode != "direct" && mode != "control-agent" {
		t.Fatalf("invalid KEA_INTEGRATION_MODE %q", mode)
	}
	stack := &lab{mode: mode, compose: "docker", prefix: []string{"compose"}, urls: make(map[string]string)}
	stack.security = prepareSecurity(t, mode)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "compose", "version").Run(); err != nil {
		stack.compose = "docker-compose"
		stack.prefix = nil
		if err := exec.CommandContext(ctx, stack.compose, "version").Run(); err != nil {
			t.Fatalf("Docker Compose is required: %v", err)
		}
	}
	project := fmt.Sprintf("kea-mcp-integration-%d-%d", os.Getpid(), time.Now().UnixNano())
	composeFile, err := filepath.Abs(filepath.Join("docker", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	stack.prefix = append(stack.prefix, "-p", project, "-f", composeFile)
	if mode == "control-agent" {
		stack.prefix = append(stack.prefix, "-f", filepath.Join(filepath.Dir(composeFile), "compose.control-agent.yaml"))
	}
	t.Logf("isolated Docker project: %s", project)
	// Cleanup is registered before startup, including failed or partially completed pulls.
	t.Cleanup(func() {
		if t.Failed() {
			output, err := stack.command(context.Background(), "logs", "--no-color", "--tail", "60")
			t.Logf("container logs (%v):\n%s", err, output)
		}
		output, err := stack.command(context.Background(), "down", "--volumes", "--remove-orphans", "--timeout", "5")
		if err != nil {
			t.Errorf("clean up isolated Docker project: %v\n%s", err, output)
		}
	})
	// Docker reassigns ephemeral published ports on stop/start. Choose free host
	// ports once and publish them explicitly so the same MCP process can recover.
	stack.env = os.Environ()
	if stack.security.configDir != "" {
		stack.env = append(stack.env, "KEA_INTEGRATION_CONFIG_DIR="+stack.security.configDir)
	}
	var listeners []net.Listener
	for _, name := range []string{"DHCP4", "DHCP6", "D2", "AGENT"} {
		var listenConfig net.ListenConfig
		listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		listeners = append(listeners, listener)
		port := listener.Addr().(*net.TCPAddr).Port
		stack.env = append(stack.env, fmt.Sprintf("KEA_INTEGRATION_%s_PORT=%d", name, port))
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	stack.run(t, "up", "-d", "--pull", "always")
	if mode == "control-agent" {
		address := strings.TrimSpace(stack.run(t, "port", "agent", "8003"))
		if !strings.HasPrefix(address, "127.0.0.1:") || strings.ContainsAny(address, "\r\n") {
			t.Fatalf("expected one loopback agent port, got %q", address)
		}
		for _, service := range []string{"dhcp4", "dhcp6", "d2"} {
			stack.urls[service] = stack.security.scheme() + "://" + address + "/"
			stack.waitReady(t, service)
		}
		return stack
	}
	for _, endpoint := range []struct {
		service string
		daemon  string
		port    string
	}{{"dhcp4", "dhcp4", "8000"}, {"dhcp6", "dhcp6", "8001"}, {"d2", "ddns", "8002"}} {
		address := strings.TrimSpace(stack.run(t, "port", endpoint.daemon, endpoint.port))
		if !strings.HasPrefix(address, "127.0.0.1:") || strings.ContainsAny(address, "\r\n") {
			t.Fatalf("expected one loopback port for %s, got %q", endpoint.service, address)
		}
		stack.urls[endpoint.service] = stack.security.scheme() + "://" + address + "/"
		stack.waitReady(t, endpoint.service)
	}
	return stack
}

func (l *lab) command(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, l.compose, append(slices.Clone(l.prefix), args...)...)
	command.Env = l.env
	return command.CombinedOutput()
}

func (l *lab) run(t *testing.T, args ...string) string {
	t.Helper()
	output, err := l.command(t.Context(), args...)
	if err != nil {
		t.Fatalf("compose %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func (l *lab) waitReady(t *testing.T, service string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	for {
		_, err := l.request(ctx, service, "status-get")
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s did not become ready: %v", service, err)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (l *lab) request(ctx context.Context, service, command string) ([]envelope, error) {
	payload := map[string]any{"command": command}
	if l.mode == "control-agent" {
		payload["service"] = []string{service}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.urls[service], bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := l.security.httpClient()
	defer client.CloseIdleConnections()
	if l.security.profile != "plain" {
		credentials := l.security.credentials(service)
		req.SetBasicAuth(credentials.user, credentials.password)
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Kea HTTP status %d", response.StatusCode)
	}
	var result []envelope
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if len(result) != 1 || result[0].Result != 0 {
		return nil, fmt.Errorf("unexpected Kea response: %+v", result)
	}
	return result, nil
}

func connect(t *testing.T, binary string, stack *lab, responseLimit int, adjust ...func(string) string) *mcp.ClientSession {
	t.Helper()
	config := fmt.Sprintf("[api]\nmode = %q\nread_only = true\nread_timeout_sec = 5\nmax_response_bytes = %d\n", stack.mode, responseLimit)
	if stack.mode == "control-agent" {
		config += fmt.Sprintf("url = %q\nservices = [\"dhcp4\", \"dhcp6\", \"d2\"]\n", stack.urls["dhcp4"])
		config += stack.security.clientSettings("dhcp4", "api")
	} else {
		for _, service := range []string{"dhcp4", "dhcp6", "d2"} {
			config += fmt.Sprintf("[api.endpoints.%s]\nurl = %q\n", service, stack.urls[service])
			config += stack.security.clientSettings(service, "api.endpoints."+service)
		}
	}
	config += "[tools]\nraw_command = true\n"
	for _, change := range adjust {
		config = change(config)
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "integration.toml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(dir, "stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stderr.Close() })
	// t.Context is canceled before cleanups; let the SDK close STDIO and wait first.
	processContext, cancelProcess := context.WithCancel(context.WithoutCancel(t.Context()))
	t.Cleanup(cancelProcess)
	command := exec.CommandContext(processContext, binary, "--config", configPath)
	command.Stderr = stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "kea-mcp-integration", Version: "test"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		data, _ := os.ReadFile(stderr.Name())
		t.Fatalf("STDIO initialization: %v\n%s", err, data)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close STDIO session: %v", err)
		}
		if command.ProcessState == nil || !command.ProcessState.Success() {
			data, _ := os.ReadFile(stderr.Name())
			t.Errorf("MCP process did not shut down cleanly: %v\n%s", command.ProcessState, data)
		}
	})
	return session
}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func successfulCommand(t *testing.T, session *mcp.ClientSession, name, service string) commandResult {
	t.Helper()
	result := call(t, session, name, map[string]any{"service": service})
	if result.IsError {
		t.Fatalf("%s on %s: %s", name, service, resultText(result))
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var got commandResult
	decode(t, data, &got)
	if got.Service != service || got.Command != strings.ReplaceAll(strings.TrimPrefix(name, "kea_"), "_", "-") || len(got.Responses) != 1 {
		t.Fatalf("unexpected %s response: %s", name, data)
	}
	if got.Responses[0].Result != 0 && got.Responses[0].Result != 3 {
		t.Fatalf("Kea command failed: %+v", got.Responses[0])
	}
	return got
}

func resultText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text.WriteString(item.Text)
		}
	}
	return text.String()
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var names []string
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %s lacks read-only annotation", tool.Name)
		}
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func assertDiscovery(t *testing.T, session *mcp.ClientSession, unavailable string) {
	t.Helper()
	result := call(t, session, "kea_discover", map[string]any{})
	if result.IsError {
		t.Fatalf("discovery failed: %s", resultText(result))
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var got discovery
	decode(t, data, &got)
	checkDiscovery(t, got, unavailable)
}

func checkDiscovery(t *testing.T, got discovery, unavailable string) {
	t.Helper()
	if !got.ReadOnly || len(got.Services) != 3 {
		t.Fatalf("expected three services in read-only discovery: %+v", got)
	}
	seen := make(map[string]bool)
	for _, service := range got.Services {
		if seen[service.Service] || !slices.Contains([]string{"dhcp4", "dhcp6", "d2"}, service.Service) {
			t.Fatalf("unexpected discovery service %q", service.Service)
		}
		seen[service.Service] = true
		if service.Service == unavailable {
			if service.Error == "" || len(service.Commands) != 0 {
				t.Fatalf("stopped daemon is not reported unavailable: %+v", service)
			}
			continue
		}
		if service.Error != "" || !slices.Contains(service.Commands, "status-get") || service.Tools["status-get"] != "kea_status_get" {
			t.Fatalf("live daemon discovery: %+v", service)
		}
		if slices.Contains(service.Commands, "config-set") {
			t.Fatal("discovery advertised a write command in read-only mode")
		}
	}
}

func assertLargeFixture(t *testing.T, service string, data []byte) {
	t.Helper()
	family := strings.TrimPrefix(service, "dhcp")
	var root map[string]json.RawMessage
	decode(t, data, &root)
	var config struct {
		Subnets []json.RawMessage            `json:"-"`
		Shared  []map[string]json.RawMessage `json:"shared-networks"`
	}
	decode(t, root["Dhcp"+family], &config)
	var top map[string]json.RawMessage
	decode(t, root["Dhcp"+family], &top)
	decode(t, top["subnet"+family], &config.Subnets)
	for _, shared := range config.Shared {
		var subnets []json.RawMessage
		decode(t, shared["subnet"+family], &subnets)
		config.Subnets = append(config.Subnets, subnets...)
	}
	reservations, pools := 0, 0
	for _, subnet := range config.Subnets {
		var fields struct {
			Reservations []json.RawMessage `json:"reservations"`
			Pools        []json.RawMessage `json:"pools"`
		}
		decode(t, subnet, &fields)
		reservations += len(fields.Reservations)
		pools += len(fields.Pools)
	}
	if len(config.Shared) != 33 || len(config.Subnets) != 67 || reservations != 2049 || pools < 130 {
		t.Fatalf("large fixture was not loaded: shared=%d subnets=%d reservations=%d pools=%d", len(config.Shared), len(config.Subnets), reservations, pools)
	}
}

func equalJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var first, second any
	decode(t, a, &first)
	decode(t, b, &second)
	x, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x, y)
}

func decode(t *testing.T, data []byte, value any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		t.Fatalf("decode live response: %v", err)
	}
}
