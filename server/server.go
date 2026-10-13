// Package server wires the Kea HTTP client into an MCP server.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/rannday/kea-mcp/config"
	"github.com/rannday/kea-mcp/kea"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

var controlAgentServices = []kea.Service{
	kea.Services.Agent,
	kea.Services.DHCP4,
	kea.Services.DHCP6,
	kea.Services.DDNS,
}

// Server wires the Kea client stack into an MCP server.
type Server struct {
	kea          *kea.Client
	services     []kea.Service
	readOnly     bool
	mcp          *sdkmcp.Server
	discoveryMu  sync.Mutex
	commandTools map[string]bool
}

type commandInput struct {
	Service   string `json:"service" jsonschema:"Kea service name: agent, dhcp4, dhcp6, or d2"`
	Arguments any    `json:"arguments,omitempty" jsonschema:"Optional Kea command arguments; use the JSON shape required by the deployed command"`
}

type rawCommandInput struct {
	Service   string `json:"service" jsonschema:"Kea service name: agent, dhcp4, dhcp6, or d2"`
	Command   string `json:"command" jsonschema:"Kea command name to execute"`
	Arguments any    `json:"arguments,omitempty" jsonschema:"Optional Kea command arguments; use the JSON shape required by the deployed command"`
}

// New builds a STDIO-ready MCP server for a Kea API endpoint.
func New(cfg config.Config) (*sdkmcp.Server, error) {
	options := []kea.HTTPOption{kea.WithAuth(&kea.BasicAuth{
		Username: cfg.APIUsername,
		Password: cfg.APIPassword,
	})}
	if cfg.MaxResponseBytes != 0 {
		options = append(options, kea.WithMaxResponseBytes(cfg.MaxResponseBytes))
	}
	services := controlAgentServices
	switch cfg.APIMode {
	case "", "control-agent":
		if cfg.APIServices != nil {
			if len(cfg.APIServices) == 0 {
				return nil, fmt.Errorf("api.services must not be empty")
			}
			services = nil
			for _, name := range cfg.APIServices {
				service, err := kea.ParseService(name)
				if err != nil || strings.TrimSpace(name) == "" || slices.Contains(services, service) {
					return nil, fmt.Errorf("invalid or duplicate api.services entry")
				}
				services = append(services, service)
			}
		}
	case "direct":
		if cfg.APIServices != nil {
			return nil, fmt.Errorf("api.services is only supported in control-agent mode")
		}
		service, err := kea.ParseService(cfg.APIService)
		if err != nil || service == kea.Services.Agent {
			return nil, fmt.Errorf("direct mode requires dhcp4, dhcp6, or d2")
		}
		options = append(options, kea.WithDirectService(service))
		services = []kea.Service{service}
	default:
		return nil, fmt.Errorf("unsupported API mode %q", cfg.APIMode)
	}
	srv := &Server{
		kea:          kea.NewClient(kea.NewHTTPTransport(cfg.APIURL, options...), kea.WithReadOnly(cfg.ReadOnly)),
		services:     services,
		readOnly:     cfg.ReadOnly,
		commandTools: make(map[string]bool),
	}
	instructions := "Use command-specific tools against configured Kea services. Run kea_discover to refresh availability after daemon or hook changes. Write tools can modify configuration, leases, and service state."
	if cfg.ReadOnly {
		instructions += " Read-only mode is enabled: write and unknown commands are rejected."
	}

	mcpServer := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "kea-mcp",
		Version: version,
	}, &sdkmcp.ServerOptions{
		Instructions: instructions,
	})

	srv.mcp = mcpServer
	sdkmcp.AddTool(mcpServer, &sdkmcp.Tool{
		Name:        "kea_discover",
		Description: "Refresh live command availability and command-specific tools. Reports unavailable services without discarding successful results. Only sends read commands.",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
	}, srv.discoverTool)
	if cfg.RawCommand {
		sdkmcp.AddTool(mcpServer, &sdkmcp.Tool{
			Name:        "kea_raw_command",
			Description: "Run an arbitrary live Kea command, including custom hooks. Approval covers every command permitted by read-only policy, including writes when read-only mode is off.",
			Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: cfg.ReadOnly},
		}, srv.rawCommand)
	}
	mcpServer.AddResource(&sdkmcp.Resource{
		Name:        "kea-api-commands",
		Title:       "Kea API Commands",
		Description: "Live command lists for configured services, filtered to reviewed read commands in read-only mode.",
		MIMEType:    "application/json",
		URI:         "kea://api/commands",
	}, srv.readCommandsResource)
	mcpServer.AddResource(&sdkmcp.Resource{
		Name:        "kea-status-get-example",
		Title:       "Kea status-get examples",
		Description: "Live status-get responses and per-service errors for configured Kea services.",
		MIMEType:    "application/json",
		URI:         "kea://api/examples/status-get",
	}, srv.readStatusExamplesResource)
	// An unavailable endpoint does not prevent MCP startup: discovery remains
	// available so callers can recover without restarting the process.
	_, _ = srv.discover(context.Background())
	return mcpServer, nil
}

func (s *Server) rawCommand(ctx context.Context, req *sdkmcp.CallToolRequest, input rawCommandInput) (*sdkmcp.CallToolResult, any, error) {
	arguments, err := originalArguments(req)
	if err != nil {
		return toolError(err), nil, nil
	}
	return s.commandTool(ctx, input.Service, input.Command, arguments)
}

// Typed SDK decoding validates routing fields but converts numbers in any to
// float64. Forward arguments from the original request to preserve JSON values.
func originalArguments(req *sdkmcp.CallToolRequest) (any, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Arguments, &fields); err != nil {
		return nil, fmt.Errorf("invalid command arguments: %w", err)
	}
	arguments, present := fields["arguments"]
	if !present {
		return nil, nil
	}
	return arguments, nil
}

func (s *Server) readCommandsResource(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	payload, err := s.discover(ctx)
	if err != nil {
		return nil, err
	}
	return jsonResource(req.Params.URI, payload)
}

func (s *Server) readStatusExamplesResource(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
	payload, err := s.examplePayload(ctx, "status-get", "https://kea.readthedocs.io/en/latest/api.html#status-get")
	if err != nil {
		return nil, err
	}
	return jsonResource(req.Params.URI, payload)
}

func (s *Server) commandTool(ctx context.Context, rawService, command string, arguments any) (*sdkmcp.CallToolResult, any, error) {
	service, err := kea.ParseService(rawService)
	if err != nil {
		return toolError(err), nil, nil
	}
	if strings.TrimSpace(command) == "" {
		return toolError(fmt.Errorf("command is required")), nil, nil
	}
	if !slices.Contains(s.services, service) {
		return toolError(fmt.Errorf("service %q is not configured", service.Name())), nil, nil
	}
	if s.readOnly && !kea.IsReadOnlyCommand(command) {
		return toolError(fmt.Errorf("read-only mode denies command %q", command)), nil, nil
	}
	// Recheck this service before dispatch, including after hook changes. This
	// only probes with a read and never retries the requested operation.
	commands, err := kea.ListCommands(ctx, s.kea, service)
	if err != nil {
		return toolError(err), nil, nil
	}
	if !slices.Contains(commands, command) {
		return toolError(fmt.Errorf("command %q is unavailable on service %q; run kea_discover to refresh tools", command, service.Name())), nil, nil
	}

	responses, err := kea.CallCommandWithArguments(ctx, s.kea, command, arguments, service)
	if err != nil {
		return toolError(err), nil, nil
	}
	if s.readOnly && command == "list-commands" {
		for i := range responses {
			var commands []string
			if err := json.Unmarshal(responses[i].Arguments, &commands); err != nil {
				return toolError(err), nil, nil
			}
			responses[i].Arguments, _ = json.Marshal(s.allowedCommands(commands))
		}
	}
	return nil, map[string]any{
		"service":   service.Name(),
		"command":   command,
		"arguments": arguments,
		"responses": responses,
	}, nil
}

func (s *Server) examplePayload(ctx context.Context, command, docPath string) (map[string]any, error) {
	examples := make([]map[string]any, 0, len(s.services))
	for _, service := range s.services {
		responses, err := kea.CallCommandWithArguments(ctx, s.kea, command, nil, service)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			examples = append(examples, map[string]any{"service": service.Name(), "error": err.Error()})
			continue
		}
		examples = append(examples, map[string]any{
			"service":   service.Name(),
			"responses": responses,
		})
	}
	return map[string]any{
		"command":  command,
		"doc_path": docPath,
		"examples": examples,
	}, nil
}

func jsonResource(uri string, payload any) (*sdkmcp.ReadResourceResult, error) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	return &sdkmcp.ReadResourceResult{
		Contents: []*sdkmcp.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(data),
		}},
	}, nil
}

func toolError(err error) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: err.Error()},
		},
	}
}
