package kea

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ParseService converts a user-facing service name to a Kea service identifier.
func ParseService(raw string) (Service, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "agent", "control-agent", "control_agent":
		return Services.Agent, nil
	case "dhcp4":
		return Services.DHCP4, nil
	case "dhcp6":
		return Services.DHCP6, nil
	case "d2", "ddns":
		return Services.DDNS, nil
	default:
		return "", fmt.Errorf("unknown kea service %q", raw)
	}
}

// Name returns a stable display name for the service.
func (s Service) Name() string {
	switch s {
	case Services.Agent:
		return "agent"
	case Services.DHCP4:
		return "dhcp4"
	case Services.DHCP6:
		return "dhcp6"
	case Services.DDNS:
		return "d2"
	default:
		return string(s)
	}
}

// CallCommandWithArguments sends a command with arguments to one or more services.
func CallCommandWithArguments(ctx context.Context, c *Client, cmd string, arguments any, services ...Service) ([]CommandResponse, error) {
	req := CommandRequest{
		Command:   cmd,
		Arguments: arguments,
	}
	if len(services) > 0 && (len(services) != 1 || services[0] != Services.Agent) {
		req.Service = services
	}

	var res []CommandResponse
	if err := c.Call(ctx, req, &res); err != nil {
		var responseErr *ResponseError
		if errors.As(err, &responseErr) {
			return responseErr.Responses, fmt.Errorf("%s failed: %w", cmd, err)
		}
		return nil, fmt.Errorf("%s failed: %w", cmd, err)
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("%s returned empty response", cmd)
	}
	for _, r := range res {
		if err := r.Result.ResultError(r.Text); err != nil {
			return res, err
		}
	}
	return res, nil
}

// ListCommands fetches the list of supported commands for a service.
func ListCommands(ctx context.Context, c *Client, service Service) ([]string, error) {
	return decodeFirst[[]string](ctx, c, "list-commands", service)
}

func callCommand(ctx context.Context, c *Client, cmd string, services ...Service) ([]CommandResponse, error) {
	req := CommandRequest{Command: cmd}
	if len(services) > 0 && (len(services) != 1 || services[0] != Services.Agent) {
		req.Service = services
	}

	var res []CommandResponse
	if err := c.Call(ctx, req, &res); err != nil {
		var responseErr *ResponseError
		if errors.As(err, &responseErr) {
			return responseErr.Responses, fmt.Errorf("%s failed: %w", cmd, err)
		}
		return nil, fmt.Errorf("%s failed: %w", cmd, err)
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("%s returned empty response", cmd)
	}
	for _, r := range res {
		if err := r.Result.ResultError(r.Text); err != nil {
			return res, err
		}
	}
	return res, nil
}

func decodeFirst[T any](ctx context.Context, c *Client, cmd string, service Service) (T, error) {
	responses, err := callCommand(ctx, c, cmd, service)
	if err != nil {
		var zero T
		return zero, err
	}
	var decoded T
	if err := json.Unmarshal(responses[0].Arguments, &decoded); err != nil {
		return decoded, fmt.Errorf("decode %s arguments: %w", cmd, err)
	}
	return decoded, nil
}
