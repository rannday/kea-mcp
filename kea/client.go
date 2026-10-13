package kea

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const defaultTimeout = 5 * time.Second

// Transport defines how to send a CommandRequest and receive a response.
type Transport interface {
	Call(ctx context.Context, req CommandRequest, out any) error
}

// Client routes Kea API calls to the underlying transport.
type Client struct {
	transport Transport
	readOnly  bool
}

// ClientOption configures a Kea client.
type ClientOption func(*Client)

// WithReadOnly restricts calls to the reviewed read command allowlist.
func WithReadOnly(enabled bool) ClientOption {
	return func(c *Client) { c.readOnly = enabled }
}

// NewClient returns a new Kea client using the provided transport.
func NewClient(t Transport, opts ...ClientOption) *Client {
	c := &Client{transport: t}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewHTTP returns a Kea client using HTTP transport.
func NewHTTP(endpoint string, opts ...HTTPOption) *Client {
	return NewClient(NewHTTPTransport(endpoint, opts...))
}

// Call sends the request using the configured transport.
func (c *Client) Call(ctx context.Context, req CommandRequest, out any) error {
	if c == nil {
		return errors.New("kea client is nil")
	}
	if c.transport == nil {
		return errors.New("kea transport is nil")
	}
	if ctx == nil {
		return errors.New("kea context is nil")
	}
	if c.readOnly && !IsReadOnlyCommand(req.Command) {
		return fmt.Errorf("read-only mode denies command %q: only reviewed read commands are allowed", req.Command)
	}
	return c.transport.Call(ctx, req, out)
}
