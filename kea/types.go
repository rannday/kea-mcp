package kea

import (
	"encoding/json"
	"fmt"
)

// ResultCode represents the result code returned by Kea commands.
type ResultCode int

const (
	ResultSuccess         ResultCode = 0
	ResultGeneralFailure  ResultCode = 1
	ResultUnsupported     ResultCode = 2
	ResultEmpty           ResultCode = 3
	ResultConflict        ResultCode = 4
	ResultRollbackFailure ResultCode = 5
)

// ResultError converts a ResultCode to an error.
func (r ResultCode) ResultError(text string) error {
	switch r {
	case ResultSuccess, ResultEmpty:
		// Empty means the command completed but returned or affected no data.
		return nil
	case ResultGeneralFailure:
		return fmt.Errorf("general error: %s", text)
	case ResultUnsupported:
		return fmt.Errorf("unsupported command: %s", text)
	case ResultConflict:
		return fmt.Errorf("conflict: %s", text)
	case ResultRollbackFailure:
		return fmt.Errorf("partial configuration apply; rollback failed; previous configuration was not restored: %s", text)
	default:
		return fmt.Errorf("unknown result code %d: %s", r, text)
	}
}

// ResponseError retains all valid Kea envelopes when any service reports failure.
type ResponseError struct {
	Responses []CommandResponse
	Err       error
}

func (e *ResponseError) Error() string { return e.Err.Error() }
func (e *ResponseError) Unwrap() error { return e.Err }

// OutcomeUnknownError means a dispatched write has no definitive Kea response.
type OutcomeUnknownError struct{ Err error }

func (e *OutcomeUnknownError) Error() string {
	return fmt.Sprintf("write outcome unknown; the command may have applied; reconcile server state before retrying: %v", e.Err)
}
func (e *OutcomeUnknownError) Unwrap() error { return e.Err }

// Service represents a Kea service name.
type Service string

// Known Kea services.
var Services = struct {
	Agent Service
	DHCP4 Service
	DHCP6 Service
	DDNS  Service
}{
	Agent: "",
	DHCP4: "dhcp4",
	DHCP6: "dhcp6",
	DDNS:  "d2",
}

// CommandRequest represents a Kea API command.
type CommandRequest struct {
	Command   string    `json:"command"`
	Service   []Service `json:"service,omitempty"`
	Arguments any       `json:"arguments,omitempty"`
}

// CommandResponse represents a response from Kea.
type CommandResponse struct {
	Result    ResultCode      `json:"result"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Text      string          `json:"text,omitempty"`
}

// UnmarshalJSON requires an explicit integer result on every response entry.
func (r *CommandResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		Result    *ResultCode     `json:"result"`
		Arguments json.RawMessage `json:"arguments"`
		Text      string          `json:"text"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Result == nil {
		return fmt.Errorf("kea response requires an integer result")
	}
	*r = CommandResponse{Result: *wire.Result, Arguments: wire.Arguments, Text: wire.Text}
	return nil
}
