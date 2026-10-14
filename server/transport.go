package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/rannday/kea-mcp/config"
	"github.com/rannday/kea-mcp/kea"
)

// serviceTransport routes each request to exactly one configured daemon.
type serviceTransport map[kea.Service]kea.Transport

func (routes serviceTransport) Call(ctx context.Context, req kea.CommandRequest, out any) error {
	if len(req.Service) != 1 {
		return fmt.Errorf("direct endpoints require exactly one service")
	}
	transport, exists := routes[req.Service[0]]
	if !exists {
		return fmt.Errorf("service has no configured endpoint")
	}
	return transport.Call(ctx, req, out)
}

func endpointTransport(endpoint config.Endpoint, options []kea.HTTPOption) (kea.Transport, error) {
	if err := endpoint.TLS.Validate(endpoint.URL); err != nil {
		return nil, err
	}
	if endpoint.TLS != (config.TLS{}) {
		transport, err := certificateTransport(endpoint.TLS)
		if err != nil {
			return nil, err
		}
		options = append(options, kea.WithRoundTripper(transport))
	}
	auth := strings.ToLower(strings.TrimSpace(endpoint.Auth))
	if auth == "" {
		auth = "none"
		if endpoint.Username != "" || endpoint.Password != "" {
			auth = "basic"
		}
	}
	switch auth {
	case "none":
		if endpoint.Username != "" || endpoint.Password != "" {
			return nil, fmt.Errorf("auth=none cannot include credentials")
		}
	case "basic":
		if endpoint.Username == "" && endpoint.Password == "" {
			return nil, fmt.Errorf("basic auth requires non-empty credentials")
		}
		options = append(options, kea.WithAuth(&kea.BasicAuth{Username: endpoint.Username, Password: endpoint.Password}))
	default:
		return nil, fmt.Errorf("auth must be basic or none")
	}
	return kea.NewHTTPTransport(endpoint.URL, options...), nil
}
