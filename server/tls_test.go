package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rannday/kea-mcp/config"
	"github.com/rannday/kea-mcp/kea"
)

// All certificates are synthetic, generated in test-owned temporary directories.
func testTLSIdentity(t *testing.T, ip string, expires time.Time) (config.TLS, tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: expires,
		IPAddresses: []net.IP{net.ParseIP(ip)}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	dir := t.TempDir()
	settings := config.TLS{CAFile: filepath.Join(dir, "ca.crt"), CertFile: filepath.Join(dir, "client.crt"), KeyFile: filepath.Join(dir, "client.key")}
	for path, data := range map[string][]byte{settings.CAFile: certificate, settings.CertFile: certificate, settings.KeyFile: privateKey} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	identity, err := tls.X509KeyPair(certificate, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certificate)
	return settings, identity, roots
}

func testTLSEndpoint(t *testing.T, identity tls.Certificate, roots *x509.CertPool, mutual bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request kea.CommandRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		arguments := any(map[string]any{"ready": true})
		if request.Command == "list-commands" {
			arguments = []string{"status-get"}
		}
		json.NewEncoder(w).Encode([]any{map[string]any{"result": 0, "arguments": arguments}})
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{identity}, MinVersion: tls.VersionTLS12}
	if mutual {
		srv.TLS.ClientCAs = roots
		srv.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestTLSMCPBothModes(t *testing.T) {
	for _, mode := range []string{"direct", "control-agent"} {
		t.Run(mode, func(t *testing.T) {
			settings, identity, roots := testTLSIdentity(t, "127.0.0.1", time.Now().Add(time.Hour))
			endpoint := testTLSEndpoint(t, identity, roots, true)
			path := filepath.Join(t.TempDir(), "deployment.toml")
			target := "services=['dhcp4']"
			if mode == "direct" {
				target = "service='dhcp4'"
			}
			body := fmt.Sprintf("[api]\nmode='%s'\n%s\nurl='%s'\n[api.tls]\nca_file='%s'\ncert_file='%s'\nkey_file='%s'\n", mode, target, endpoint.URL, filepath.ToSlash(settings.CAFile), filepath.ToSlash(settings.CertFile), filepath.ToSlash(settings.KeyFile))
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			srv, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			client := connectTestClient(t, srv)
			discoverTestTools(t, client)
			result, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "kea_status_get", Arguments: map[string]any{"service": "dhcp4"}})
			if err != nil || result.IsError {
				t.Fatalf("TLS MCP call failed: %v %v", result, err)
			}
		})
	}
}

func TestTLSVerificationAndIsolation(t *testing.T) {
	one, identity, roots := testTLSIdentity(t, "127.0.0.1", time.Now().Add(time.Hour))
	two, otherIdentity, otherRoots := testTLSIdentity(t, "127.0.0.1", time.Now().Add(time.Hour))
	first := testTLSEndpoint(t, identity, roots, true)
	second := testTLSEndpoint(t, otherIdentity, otherRoots, true)
	routes := make(serviceTransport)
	for service, endpoint := range map[kea.Service]config.Endpoint{
		kea.Services.DHCP4: {URL: first.URL, TLS: one},
		kea.Services.DHCP6: {URL: second.URL, TLS: two},
	} {
		transport, err := endpointTransport(endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		routes[service] = transport
	}
	for service := range routes {
		var response json.RawMessage
		if err := routes.Call(context.Background(), kea.CommandRequest{Command: "status-get", Service: []kea.Service{service}}, &response); err != nil {
			t.Fatal(err)
		}
	}
	wrongName, wrongIdentity, wrongRoots := testTLSIdentity(t, "192.0.2.1", time.Now().Add(time.Hour))
	wrongServer := testTLSEndpoint(t, wrongIdentity, wrongRoots, false)
	expired, expiredIdentity, expiredRoots := testTLSIdentity(t, "127.0.0.1", time.Now().Add(-time.Minute))
	expiredServer := testTLSEndpoint(t, expiredIdentity, expiredRoots, false)
	for name, endpoint := range map[string]config.Endpoint{
		"untrusted":      {URL: first.URL},
		"missing-client": {URL: first.URL, TLS: config.TLS{CAFile: one.CAFile}},
		"wrong-client":   {URL: first.URL, TLS: config.TLS{CAFile: one.CAFile, CertFile: two.CertFile, KeyFile: two.KeyFile}},
		"wrong-server":   {URL: second.URL, TLS: one},
		"wrong-hostname": {URL: wrongServer.URL, TLS: config.TLS{CAFile: wrongName.CAFile}},
		"expired":        {URL: expiredServer.URL, TLS: config.TLS{CAFile: expired.CAFile}},
	} {
		t.Run(name, func(t *testing.T) {
			transport, err := endpointTransport(endpoint, nil)
			if err != nil {
				t.Fatal(err)
			}
			var response json.RawMessage
			if err := transport.Call(context.Background(), kea.CommandRequest{Command: "status-get"}, &response); err == nil {
				t.Fatal("invalid TLS handshake accepted")
			}
		})
	}
}

func TestCustomCAWithoutClientIdentity(t *testing.T) {
	settings, identity, roots := testTLSIdentity(t, "127.0.0.1", time.Now().Add(time.Hour))
	endpoint := testTLSEndpoint(t, identity, roots, false)
	transport, err := endpointTransport(config.Endpoint{URL: endpoint.URL, TLS: config.TLS{CAFile: settings.CAFile}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var response json.RawMessage
	if err := transport.Call(context.Background(), kea.CommandRequest{Command: "status-get"}, &response); err != nil {
		t.Fatal(err)
	}
}

func TestTLSStartupFailures(t *testing.T) {
	one, _, _ := testTLSIdentity(t, "127.0.0.1", time.Now().Add(time.Hour))
	two, _, _ := testTLSIdentity(t, "127.0.0.1", time.Now().Add(time.Hour))
	invalid := filepath.Join(t.TempDir(), "invalid.crt")
	if err := os.WriteFile(invalid, []byte("synthetic-invalid-certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, settings := range []config.TLS{
		{CAFile: filepath.Join(t.TempDir(), "missing.crt")},
		{CAFile: invalid},
		{CertFile: one.CertFile},
		{CertFile: one.CertFile, KeyFile: two.KeyFile},
		{CertFile: invalid, KeyFile: one.KeyFile},
	} {
		if _, err := New(config.Config{APIURL: "https://127.0.0.1:8000", APITLS: settings}); err == nil {
			t.Fatal("invalid certificate files accepted at startup")
		}
	}
}
