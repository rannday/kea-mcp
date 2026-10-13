package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rannday/kea-mcp/kea"
)

func commandToolName(command string) string {
	return "kea_" + strings.ReplaceAll(command, "-", "_")
}

func (s *Server) allowedCommands(commands []string) []string {
	allowed := make([]string, 0, len(commands))
	for _, command := range commands {
		if !s.readOnly || kea.IsReadOnlyCommand(command) {
			allowed = append(allowed, command)
		}
	}
	return allowed
}

func (s *Server) discoverTool(ctx context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, any, error) {
	payload, err := s.discover(ctx)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, payload, nil
}

// discover publishes the union of reviewed commands available on configured
// services. A failed probe removes that service's capabilities until recovery.
func (s *Server) discover(ctx context.Context) (map[string]any, error) {
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	services := make([]map[string]any, 0, len(s.services))
	available := make(map[string]bool)
	for _, service := range s.services {
		entry := map[string]any{"service": service.Name()}
		commands, err := kea.ListCommands(ctx, s.kea, service)
		if err != nil {
			entry["error"] = err.Error()
		} else {
			commands = s.allowedCommands(commands)
			entry["commands"] = commands
			tools := make(map[string]string)
			for _, command := range commands {
				if kea.IsKnownCommand(command) {
					available[command] = true
					tools[command] = commandToolName(command)
				}
			}
			entry["tools"] = tools
		}
		services = append(services, entry)
	}
	// A cancelled refresh must not publish an incomplete tool inventory.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for command := range s.commandTools {
		if !available[command] {
			s.mcp.RemoveTools(commandToolName(command))
			delete(s.commandTools, command)
		}
	}
	commands := make([]string, 0, len(available))
	for command := range available {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	for _, command := range commands {
		if s.commandTools[command] {
			continue
		}
		access := "May change Kea state; obtain approval before use."
		if kea.IsReadOnlyCommand(command) {
			access = "Read command; returned data may be sensitive."
		}
		sdkmcp.AddTool(s.mcp, &sdkmcp.Tool{
			Name:        commandToolName(command),
			Description: fmt.Sprintf("Run Kea %s against one configured service. %s Use arguments from the deployed version's API documentation: https://kea.readthedocs.io/en/latest/api.html#%s", command, access, command),
			Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: kea.IsReadOnlyCommand(command)},
		}, func(ctx context.Context, req *sdkmcp.CallToolRequest, input commandInput) (*sdkmcp.CallToolResult, any, error) {
			arguments, err := originalArguments(req)
			if err != nil {
				return toolError(err), nil, nil
			}
			return s.commandTool(ctx, input.Service, command, arguments)
		})
		s.commandTools[command] = true
	}
	return map[string]any{
		"read_only": s.readOnly,
		"doc_path":  "https://kea.readthedocs.io/en/latest/api.html#list-commands",
		"services":  services,
	}, nil
}
