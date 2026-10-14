package kea

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPRouting(t *testing.T) {
	for _, direct := range []bool{false, true} {
		name := "control-agent"
		if direct {
			name = "direct"
		}
		t.Run(name, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.Method != http.MethodPost {
					t.Errorf("method = %s", r.Method)
				}
				if string(body["command"]) != `"status-get"` {
					t.Errorf("command = %s", body["command"])
				}
				service, present := body["service"]
				if direct && present {
					t.Error("direct request contains service routing")
				}
				if !direct && string(service) != `["dhcp4"]` {
					t.Errorf("service = %s", service)
				}
				user, pass, ok := r.BasicAuth()
				if !ok || user != "test-user" || pass != "test-password" {
					t.Error("missing authentication")
				}
				_, _ = w.Write([]byte(`[{"result":0,"text":"running"}]`))
			}))
			defer endpoint.Close()
			opts := []HTTPOption{WithAuth(&BasicAuth{Username: "test-user", Password: "test-password"})}
			if direct {
				opts = append(opts, WithDirectService(Services.DHCP4))
			}
			client := NewHTTP(endpoint.URL, opts...)
			responses, err := CallCommandWithArguments(context.Background(), client, "status-get", nil, Services.DHCP4)
			if err != nil || len(responses) != 1 {
				t.Fatalf("responses = %v, error = %v", responses, err)
			}
		})
	}
}

func TestTimeoutWriteOutcome(t *testing.T) {
	for _, command := range []string{"config-set", "status-get"} {
		t.Run(command, func(t *testing.T) {
			var calls atomic.Int32
			release := make(chan struct{})
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer endpoint.Close()
			defer close(release)
			client := NewHTTP(endpoint.URL, WithHTTPClient(&http.Client{Timeout: 100 * time.Millisecond}))
			responses, err := CallCommandWithArguments(context.Background(), client, command, nil, Services.DHCP4)
			var unknown *OutcomeUnknownError
			if err == nil || responses != nil || !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &unknown) != (command == "config-set") {
				t.Fatalf("responses=%v error=%v", responses, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("requests=%d; expected no retry", calls.Load())
			}
		})
	}
}

func TestMixedServiceFailureRetainsResponses(t *testing.T) {
	const body = `[{"result":0,"text":"applied","arguments":{"id":9007199254740993}},{"result":5,"text":"rollback rejected","arguments":{"failed-leases":[{"ip-address":"2001:db8::1"}]}}]`
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
	defer endpoint.Close()
	responses, err := CallCommandWithArguments(context.Background(), NewHTTP(endpoint.URL), "config-set", nil, Services.DHCP4, Services.DHCP6)
	if err == nil || len(responses) != 2 || !strings.Contains(err.Error(), "previous configuration was not restored") {
		t.Fatalf("responses=%v error=%v", responses, err)
	}
	var unknown *OutcomeUnknownError
	if errors.As(err, &unknown) {
		t.Fatal("explicit Kea failure classified as missing reply")
	}
	if responses[0].Result != ResultSuccess || responses[0].Text != "applied" || string(responses[0].Arguments) != `{"id":9007199254740993}` || responses[1].Result != ResultRollbackFailure || responses[1].Text != "rollback rejected" || string(responses[1].Arguments) != `{"failed-leases":[{"ip-address":"2001:db8::1"}]}` {
		t.Fatalf("envelopes changed: %+v", responses)
	}
}

func TestHTTPRejectsRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, custom := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/custom=%t", status, custom), func(t *testing.T) {
				var redirected atomic.Int32
				var callbacks atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					redirected.Add(1)
					_, _ = w.Write([]byte(`[{"result":0}]`))
				}))
				defer target.Close()
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("method = %s", r.Method)
					}
					w.Header().Set("Location", target.URL)
					w.WriteHeader(status)
				}))
				defer endpoint.Close()
				client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
					callbacks.Add(1)
					return nil
				}}
				var opts []HTTPOption
				if custom {
					opts = append(opts, WithHTTPClient(client))
				}
				_, err := CallCommandWithArguments(context.Background(), NewHTTP(endpoint.URL, opts...), "config-set", map[string]any{}, Services.DHCP4)
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("status=%d", status)) {
					t.Fatalf("redirect error = %v", err)
				}
				if redirected.Load() != 0 || callbacks.Load() != 0 {
					t.Fatal("redirect was followed or custom redirect policy was invoked")
				}
				if custom {
					if err := client.CheckRedirect(nil, nil); err != nil || callbacks.Load() != 1 {
						t.Fatal("caller-owned client was modified")
					}
				}
			})
		}
	}
}

func TestHTTPCommandResults(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, code := range []ResultCode{ResultSuccess, ResultEmpty, ResultGeneralFailure, ResultUnsupported, ResultConflict, ResultRollbackFailure, 99} {
			t.Run(fmt.Sprintf("direct=%t/result=%d", direct, code), func(t *testing.T) {
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `[{"result":%d,"text":"test result","arguments":{"leases":[]}}]`, code)
				}))
				defer endpoint.Close()
				var opts []HTTPOption
				if direct {
					opts = append(opts, WithDirectService(Services.DHCP4))
				}
				responses, err := CallCommandWithArguments(context.Background(), NewHTTP(endpoint.URL, opts...), "lease4-get-all", nil, Services.DHCP4)
				if code != ResultSuccess && code != ResultEmpty {
					if err == nil {
						t.Fatal("failure result accepted")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if len(responses) != 1 {
					t.Fatalf("responses = %v, error = %v", responses, err)
				}
				if responses[0].Result != code || responses[0].Text != "test result" || string(responses[0].Arguments) != `{"leases":[]}` {
					t.Fatalf("response envelope changed: %+v", responses[0])
				}
			})
		}
	}
}

func TestDirectRejectsOtherServices(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("rejected request reached endpoint") }))
	defer endpoint.Close()
	transport := NewHTTPTransport(endpoint.URL, WithDirectService(Services.DHCP4))
	for _, services := range [][]Service{nil, {Services.DHCP6}, {Services.DHCP4, Services.DHCP6}} {
		var out []CommandResponse
		if err := transport.Call(context.Background(), CommandRequest{Command: "status-get", Service: services}, &out); err == nil {
			t.Fatalf("accepted services %v", services)
		}
	}
}

func TestHTTPRejectsMalformedResponses(t *testing.T) {
	for _, body := range []string{`[{}]`, `[null]`, `[{"result":null}]`, `[{"result":"0"}]`, `[{"result":0.5}]`, `[{"result":0},{}]`, `[{"result":0}] []`, `[{"result":0}] garbage`, `[]`, `null`} {
		t.Run(body, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer endpoint.Close()
			if responses, err := CallCommandWithArguments(context.Background(), NewHTTP(endpoint.URL), "status-get", nil, Services.DHCP4); err == nil || responses != nil {
				t.Fatal("malformed response reported success")
			}
		})
	}
}

func TestHTTPResponseSizeLimit(t *testing.T) {
	const body = `[{"result":0,"arguments":{"leases":[]}}]`
	for _, streaming := range []bool{false, true} {
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if streaming {
				w.(http.Flusher).Flush() // No Content-Length; limit actual bytes read.
			}
			_, _ = w.Write([]byte(body))
		}))
		for _, limit := range []int64{int64(len(body)) - 1, int64(len(body)), int64(len(body)) + 1} {
			_, err := CallCommandWithArguments(context.Background(), NewHTTP(endpoint.URL, WithMaxResponseBytes(limit)), "status-get", nil, Services.DHCP4)
			if limit < int64(len(body)) {
				if err == nil || !strings.Contains(err.Error(), "byte limit") {
					t.Fatalf("oversized response accepted: %v", err)
				}
			} else if err != nil {
				t.Fatalf("response within limit rejected: %v", err)
			}
		}
		endpoint.Close()
	}
}
