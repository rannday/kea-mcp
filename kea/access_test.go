package kea

import (
	"context"
	"testing"
)

func TestReadOnlyCommandPolicy(t *testing.T) {
	for command, access := range commandAccessPolicy {
		t.Run(command, func(t *testing.T) {
			called := false
			transport := captureTransport{callFn: func(context.Context, CommandRequest, any) error {
				called = true
				return nil
			}}
			req := CommandRequest{Command: command}
			client := NewClient(transport, WithReadOnly(true))
			err := client.Call(context.Background(), req, nil)
			wantRead := access == accessRead
			if called != wantRead || (err == nil) != wantRead {
				t.Fatalf("read-only: called=%v error=%v access=%v", called, err, access)
			}
			called = false
			if err := NewClient(transport).Call(context.Background(), req, nil); err != nil || !called {
				t.Fatalf("full access: called=%v error=%v", called, err)
			}
		})
	}
}

func TestReadOnlyExactAllowlist(t *testing.T) {
	// Pin important semantic cases independently of the inventory table.
	for _, command := range []string{"status-get", "config-get", "list-commands", "lease4-get", "lease6-get", "statistic-get-all", "subnet4-select-test"} {
		if !IsReadOnlyCommand(command) {
			t.Errorf("expected read command %q", command)
		}
	}
	for _, command := range []string{"config-set", "shutdown", "config-test", "ha-heartbeat", "interface-redetect", "perfmon-get-all-durations", "custom-get", "STATUS-GET", "status-get ", ""} {
		t.Run(command, func(t *testing.T) {
			called := false
			transport := captureTransport{callFn: func(context.Context, CommandRequest, any) error {
				called = true
				return nil
			}}
			req := CommandRequest{Command: command, Arguments: map[string]any{"command": "status-get"}}
			if err := NewClient(transport, WithReadOnly(true)).Call(context.Background(), req, nil); err == nil || called {
				t.Fatalf("denied command reached transport: called=%v error=%v", called, err)
			}
			if command == "custom-get" {
				if err := NewClient(transport, WithReadOnly(false)).Call(context.Background(), req, nil); err != nil || !called {
					t.Fatalf("unknown command blocked in full-access mode: %v", err)
				}
			}
		})
	}
}
