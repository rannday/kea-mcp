package kea

import (
	"net/http"
	"testing"
	"time"
)

func TestRoundTripperPreservesClient(t *testing.T) {
	client := &http.Client{Timeout: 90 * time.Second}
	roundTripper := &http.Transport{}
	transport := NewHTTPTransport("https://kea.example", WithHTTPClient(client), WithRoundTripper(roundTripper))
	if client.Transport != nil || transport.httpClient.Transport != roundTripper || transport.httpClient.Timeout != client.Timeout || transport.httpClient.CheckRedirect == nil {
		t.Fatal("transport must preserve client ownership, timeout, and redirect policy")
	}
	if NewHTTPTransport("https://kea.example", WithRoundTripper(roundTripper)).httpClient.Timeout != defaultTimeout {
		t.Fatal("TLS transport changed default timeout")
	}
}
