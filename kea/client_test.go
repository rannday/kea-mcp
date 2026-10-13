package kea

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseService(t *testing.T) {
	tests := []struct {
		input   string
		want    Service
		wantErr bool
	}{
		{input: "", want: Services.Agent},
		{input: "agent", want: Services.Agent},
		{input: "control-agent", want: Services.Agent},
		{input: "dhcp4", want: Services.DHCP4},
		{input: "dhcp6", want: Services.DHCP6},
		{input: "ddns", want: Services.DDNS},
		{input: "d2", want: Services.DDNS},
		{input: "bogus", wantErr: true},
	}

	for _, tc := range tests {
		got, err := ParseService(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseService(%q) expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseService(%q) returned error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("ParseService(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestServiceName(t *testing.T) {
	if got := Services.Agent.Name(); got != "agent" {
		t.Fatalf("agent name = %q, want %q", got, "agent")
	}
	if got := Services.DDNS.Name(); got != "d2" {
		t.Fatalf("ddns name = %q, want %q", got, "d2")
	}
}

func TestCallCommandWithArguments(t *testing.T) {
	var captured CommandRequest
	client := NewClient(captureTransport{
		callFn: func(_ context.Context, req CommandRequest, out any) error {
			captured = req
			*out.(*[]CommandResponse) = []CommandResponse{{Result: ResultSuccess}}
			return nil
		},
	})

	got, err := CallCommandWithArguments(context.Background(), client, "status-get", map[string]any{"verbose": true}, Services.DHCP4)
	if err != nil {
		t.Fatalf("CallCommandWithArguments returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("responses = %d, want 1", len(got))
	}
	if captured.Command != "status-get" {
		t.Fatalf("captured command = %q, want %q", captured.Command, "status-get")
	}
	if !reflect.DeepEqual(captured.Arguments, map[string]any{"verbose": true}) {
		t.Fatalf("captured arguments = %#v", captured.Arguments)
	}
	if !reflect.DeepEqual(captured.Service, []Service{Services.DHCP4}) {
		t.Fatalf("captured services = %#v", captured.Service)
	}
}

type captureTransport struct {
	callFn func(ctx context.Context, req CommandRequest, out any) error
}

func TestEmptyCommandResponse(t *testing.T) {
	client := NewClient(captureTransport{callFn: func(_ context.Context, _ CommandRequest, out any) error {
		*out.(*[]CommandResponse) = []CommandResponse{{Result: ResultEmpty, Text: "no data", Arguments: json.RawMessage(`{"leases":[]}`)}}
		return nil
	}})
	responses, err := callCommand(context.Background(), client, "lease4-get-all", Services.DHCP4)
	if err != nil || len(responses) != 1 || responses[0].Result != ResultEmpty {
		t.Fatalf("responses = %v, error = %v", responses, err)
	}
	// A completed no-op write also retains result 3 instead of claiming data changed.
	responses, err = CallCommandWithArguments(context.Background(), client, "option4-global-del", nil, Services.DHCP4)
	if err != nil || len(responses) != 1 || responses[0].Result != ResultEmpty {
		t.Fatalf("responses = %v, error = %v", responses, err)
	}
}

func (t captureTransport) Call(ctx context.Context, req CommandRequest, out any) error {
	return t.callFn(ctx, req, out)
}
