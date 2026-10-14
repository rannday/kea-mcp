//go:build integration

package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type basicCredentials struct{ user, password string }

type securityFixture struct {
	profile, mode, configDir string
	roots                    *x509.CertPool
	client                   tls.Certificate
}

func (s *securityFixture) credentials(service string) basicCredentials {
	if s.mode == "control-agent" {
		service = "agent"
	}
	return basicCredentials{"integration-" + service, "public-lab-password-" + service}
}

func (s *securityFixture) scheme() string {
	if s.profile == "tls" || s.profile == "mtls" {
		return "https"
	}
	return "http"
}

func (s *securityFixture) httpClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if s.scheme() == "https" {
		transport.TLSClientConfig = &tls.Config{RootCAs: s.roots, MinVersion: tls.VersionTLS12}
		if s.profile == "mtls" {
			transport.TLSClientConfig.Certificates = []tls.Certificate{s.client}
		}
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func (s *securityFixture) clientSettings(service, table string) string {
	if s.profile == "plain" {
		return "auth = \"none\"\n"
	}
	auth := s.credentials(service)
	result := fmt.Sprintf("auth = \"basic\"\nusername = %q\npassword = %q\n", auth.user, auth.password)
	if s.scheme() == "https" {
		result += fmt.Sprintf("[%s.tls]\nca_file = %q\n", table, filepath.Join(s.configDir, "ca.crt"))
		if s.profile == "mtls" {
			result += fmt.Sprintf("cert_file = %q\nkey_file = %q\n", filepath.Join(s.configDir, "client.crt"), filepath.Join(s.configDir, "client.key"))
		}
	}
	return result
}

// All credentials are public test values; certificates and keys exist only in
// the test's temporary directory. Kea itself terminates TLS and checks Basic auth.
func prepareSecurity(t *testing.T, mode string) *securityFixture {
	t.Helper()
	profile := os.Getenv("KEA_INTEGRATION_SECURITY")
	if profile == "" {
		profile = "plain"
	}
	if profile != "plain" && profile != "basic" && profile != "tls" && profile != "mtls" {
		t.Fatalf("invalid KEA_INTEGRATION_SECURITY %q", profile)
	}
	s := &securityFixture{profile: profile, mode: mode}
	if profile == "plain" {
		return s
	}
	s.configDir = t.TempDir()
	for _, name := range []string{"kea-entrypoint.sh", "kea-dhcp4.conf", "kea-dhcp6.conf", "kea-dhcp-ddns.conf", "kea-ctrl-agent.conf"} {
		data, err := os.ReadFile(filepath.Join("docker", "config", name))
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, s.configDir, name, data)
	}
	if s.scheme() == "https" {
		ca, key, der := makeCA(t)
		writeFixture(t, s.configDir, "ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		s.roots = x509.NewCertPool()
		s.roots.AddCert(ca)
		cert, identity := issueCertificate(t, ca, key, false)
		writeFixture(t, s.configDir, "server.crt", cert)
		writeFixture(t, s.configDir, "server.key", identity)
		cert, identity = issueCertificate(t, ca, key, true)
		writeFixture(t, s.configDir, "client.crt", cert)
		writeFixture(t, s.configDir, "client.key", identity)
		var err error
		s.client, err = tls.X509KeyPair(cert, identity)
		if err != nil {
			t.Fatal(err)
		}
		otherCA, otherKey, otherDER := makeCA(t)
		writeFixture(t, s.configDir, "untrusted-ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherDER}))
		cert, identity = issueCertificate(t, otherCA, otherKey, true)
		writeFixture(t, s.configDir, "untrusted-client.crt", cert)
		writeFixture(t, s.configDir, "untrusted-client.key", identity)
	}
	if mode == "control-agent" {
		s.configureListener(t, "kea-ctrl-agent.conf", "Control-agent", "agent")
	} else {
		for _, daemon := range []struct{ file, root, service string }{
			{"kea-dhcp4.conf", "Dhcp4", "dhcp4"}, {"kea-dhcp6.conf", "Dhcp6", "dhcp6"}, {"kea-dhcp-ddns.conf", "DhcpDdns", "d2"},
		} {
			s.configureListener(t, daemon.file, daemon.root, daemon.service)
		}
	}
	return s
}

func (s *securityFixture) configureListener(t *testing.T, file, root, service string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.configDir, file))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	listener := document[root].(map[string]any)
	if s.mode == "direct" {
		listener = listener["control-sockets"].([]any)[0].(map[string]any)
	}
	auth := s.credentials(service)
	listener["authentication"] = map[string]any{"type": "basic", "realm": "kea-integration", "clients": []any{map[string]any{"user": auth.user, "password": auth.password}}}
	if s.scheme() == "https" {
		if s.mode == "direct" {
			listener["socket-type"] = "https"
		}
		listener["trust-anchor"] = "/config/ca.crt"
		listener["cert-file"] = "/config/server.crt"
		listener["key-file"] = "/config/server.key"
		listener["cert-required"] = s.profile == "mtls"
	}
	data, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, s.configDir, file, append(data, '\n'))
}

func writeFixture(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := certificateTemplate(t)
	template.Subject = pkix.Name{CommonName: "ephemeral Kea integration CA"}
	template.IsCA, template.BasicConstraintsValid = true, true
	template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return ca, key, der
}

func certificateTemplate(t *testing.T) *x509.Certificate {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	return &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
}

func issueCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, client bool) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := certificateTemplate(t)
	template.Subject = pkix.Name{CommonName: "ephemeral Kea integration identity"}
	if client {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: identity})
}

func testSecurity(t *testing.T, binary string, stack *lab) {
	t.Helper()
	cases := []struct {
		name, reason string
		adjust       func(string) string
	}{
		{"missing_basic_credentials", "401", func(config string) string {
			var lines []string
			for _, line := range strings.Split(config, "\n") {
				if strings.HasPrefix(line, "username =") || strings.HasPrefix(line, "password =") {
					continue
				}
				lines = append(lines, strings.ReplaceAll(line, "auth = \"basic\"", "auth = \"none\""))
			}
			return strings.Join(lines, "\n")
		}},
		{"wrong_basic_password", "401", func(config string) string {
			return strings.ReplaceAll(config, "public-lab-password-", "wrong-password-")
		}},
	}
	if stack.security.scheme() == "https" {
		cases = append(cases,
			struct {
				name, reason string
				adjust       func(string) string
			}{"untrusted_server_ca", "certificate", func(config string) string { return strings.ReplaceAll(config, "ca.crt", "untrusted-ca.crt") }},
			struct {
				name, reason string
				adjust       func(string) string
			}{"server_hostname_mismatch", "certificate", func(config string) string { return strings.ReplaceAll(config, "127.0.0.1", "localhost") }},
		)
	}
	if stack.security.profile == "mtls" {
		cases = append(cases,
			struct {
				name, reason string
				adjust       func(string) string
			}{"missing_client_certificate", "", func(config string) string {
				var lines []string
				for _, line := range strings.Split(config, "\n") {
					if !strings.HasPrefix(line, "cert_file =") && !strings.HasPrefix(line, "key_file =") {
						lines = append(lines, line)
					}
				}
				return strings.Join(lines, "\n")
			}},
			struct {
				name, reason string
				adjust       func(string) string
			}{"untrusted_client_certificate", "", func(config string) string {
				return strings.ReplaceAll(strings.ReplaceAll(config, "client.crt", "untrusted-client.crt"), "client.key", "untrusted-client.key")
			}},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := connect(t, binary, stack, 8<<20, tc.adjust)
			for _, service := range []string{"dhcp4", "dhcp6", "d2"} {
				// A successful independent request with the original credentials
				// distinguishes authentication rejection from a daemon outage.
				if _, err := stack.request(t.Context(), service, "status-get"); err != nil {
					t.Fatalf("secured daemon is unavailable: %v", err)
				}
				result := call(t, session, "kea_status_get", map[string]any{"service": service})
				if !result.IsError || (tc.reason != "" && !strings.Contains(resultText(result), tc.reason)) {
					t.Fatalf("%s did not reject %s: %s", service, tc.name, resultText(result))
				}
				if strings.Contains(resultText(result), stack.security.credentials(service).password) {
					t.Fatal("error exposed a password")
				}
			}
			result := call(t, session, "kea_discover", map[string]any{})
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var got discovery
			decode(t, data, &got)
			if len(got.Services) != 3 {
				t.Fatal("failed authentication lost discovery services")
			}
			for _, service := range got.Services {
				if service.Error == "" || len(service.Commands) != 0 {
					t.Fatalf("discovery accepted %s", tc.name)
				}
			}
		})
	}
	if stack.mode == "direct" {
		t.Run("endpoint_credentials_are_isolated", func(t *testing.T) {
			session := connect(t, binary, stack, 8<<20, func(config string) string {
				return strings.ReplaceAll(strings.ReplaceAll(config, "integration-dhcp6", "integration-dhcp4"), "public-lab-password-dhcp6", "public-lab-password-dhcp4")
			})
			assertDiscovery(t, session, "dhcp6")
			successfulCommand(t, session, "kea_status_get", "dhcp4")
			successfulCommand(t, session, "kea_status_get", "d2")
			result := call(t, session, "kea_status_get", map[string]any{"service": "dhcp6"})
			if !result.IsError || !strings.Contains(resultText(result), "401") {
				t.Fatal("DHCP6 accepted DHCP4 credentials")
			}
		})
	}
}
