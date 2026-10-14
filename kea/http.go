package kea

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
)

const maxHTTPErrorDrain = 64 << 10

// DefaultMaxResponseBytes bounds API response body bytes.
const DefaultMaxResponseBytes int64 = 64 << 20

// AuthProvider applies authentication to HTTP requests.
type AuthProvider interface {
	Apply(req *http.Request) error
}

// BasicAuth provides HTTP Basic Authentication.
type BasicAuth struct {
	Username string
	Password string
}

// Apply adds an Authorization header using base64-encoded credentials.
func (a *BasicAuth) Apply(req *http.Request) error {
	if req == nil {
		return fmt.Errorf("auth apply: nil request")
	}
	if a.Username == "" && a.Password == "" {
		return fmt.Errorf("auth apply: empty credentials")
	}

	creds := a.Username + ":" + a.Password
	encoded := base64.StdEncoding.EncodeToString([]byte(creds))
	req.Header.Set("Authorization", "Basic "+encoded)
	return nil
}

// HTTPTransport sends commands to Kea over HTTP.
type HTTPTransport struct {
	endpoint         string
	httpClient       *http.Client
	auth             AuthProvider
	directService    Service
	maxResponseBytes int64
}

// HTTPOption configures an HTTPTransport.
type HTTPOption func(*HTTPTransport)

// WithMaxResponseBytes sets a positive API response body limit.
func WithMaxResponseBytes(size int64) HTTPOption {
	return func(t *HTTPTransport) { t.maxResponseBytes = size }
}

// WithHTTPClient sets a custom http.Client.
func WithHTTPClient(hc *http.Client) HTTPOption {
	return func(t *HTTPTransport) {
		t.httpClient = hc
	}
}

// WithAuth sets a request auth provider.
func WithAuth(auth AuthProvider) HTTPOption {
	return func(t *HTTPTransport) {
		t.auth = auth
	}
}

// WithDirectService binds requests to one daemon and omits Control Agent routing.
func WithDirectService(service Service) HTTPOption {
	return func(t *HTTPTransport) {
		t.directService = service
	}
}

// NewHTTPTransport returns a configured HTTPTransport.
func NewHTTPTransport(endpoint string, opts ...HTTPOption) *HTTPTransport {
	t := &HTTPTransport{
		endpoint:         endpoint,
		httpClient:       &http.Client{Timeout: defaultTimeout},
		maxResponseBytes: DefaultMaxResponseBytes,
	}
	for _, opt := range opts {
		opt(t)
	}
	if t.httpClient != nil {
		// Preserve caller-owned clients while forbidding command replay or
		// method changes at a redirected endpoint, including for custom clients.
		client := *t.httpClient
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
		t.httpClient = &client
	}
	return t
}

// Call implements the Transport interface for HTTP.
func (t *HTTPTransport) Call(ctx context.Context, req CommandRequest, out any) error {
	if t == nil {
		return errors.New("kea http transport is nil")
	}
	if t.httpClient == nil {
		return errors.New("kea http client is nil")
	}
	if strings.TrimSpace(t.endpoint) == "" {
		return errors.New("kea endpoint is empty")
	}
	if ctx == nil {
		return errors.New("kea context is nil")
	}
	if t.maxResponseBytes <= 0 || t.maxResponseBytes == math.MaxInt64 {
		return errors.New("invalid Kea response size limit")
	}

	if t.directService != "" {
		if len(req.Service) != 1 || req.Service[0] != t.directService {
			return fmt.Errorf("direct endpoint only supports service %s", t.directService.Name())
		}
		req.Service = nil
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if t.auth != nil {
		if err := t.auth.Apply(httpReq); err != nil {
			return fmt.Errorf("apply auth: %w", err)
		}
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return uncertainOutcome(req, fmt.Errorf("send request: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxHTTPErrorDrain))
		return uncertainOutcome(req, fmt.Errorf("kea error: status=%d", resp.StatusCode))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, t.maxResponseBytes+1))
	if err != nil {
		return uncertainOutcome(req, fmt.Errorf("read response: %w", err))
	}
	if int64(len(data)) > t.maxResponseBytes {
		return uncertainOutcome(req, fmt.Errorf("kea response exceeds %d byte limit", t.maxResponseBytes))
	}
	// Unmarshal consumes the whole body, rejecting trailing JSON or garbage.
	if err := json.Unmarshal(data, out); err != nil {
		return uncertainOutcome(req, fmt.Errorf("unmarshal response: %w", err))
	}
	err = validateCommandResponses(out)
	var responseErr *ResponseError
	if err != nil && !errors.As(err, &responseErr) {
		return uncertainOutcome(req, err)
	}
	return err
}

func uncertainOutcome(req CommandRequest, err error) error {
	if !IsReadOnlyCommand(req.Command) {
		return &OutcomeUnknownError{Err: err}
	}
	return err
}

func validateCommandResponses(out any) error {
	responses, ok := out.(*[]CommandResponse)
	if !ok {
		return nil
	}
	if len(*responses) == 0 {
		return fmt.Errorf("empty response from Kea")
	}
	for _, r := range *responses {
		if err := r.Result.ResultError(r.Text); err != nil {
			return &ResponseError{Responses: *responses, Err: err}
		}
	}
	return nil
}
