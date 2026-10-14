package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTLSPathsAndInheritance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "arbitrary-name.toml")
	abs := filepath.Join(t.TempDir(), "six-ca.crt")
	writeConfig(t, path, fmt.Sprintf(`[api]
mode='direct'
username='shared'
password='synthetic'
[api.tls]
ca_file='certs/shared.crt'
cert_file='certs/client.crt'
key_file='certs/client.key'
[api.endpoints.dhcp4]
url='https://127.0.0.1:8000'
auth='none'
[api.endpoints.dhcp6]
url='https://127.0.0.1:8001'
[api.endpoints.dhcp6.tls]
ca_file='%s'
[api.endpoints.d2]
url='http://127.0.0.1:8002'
[api.endpoints.d2.tls]
`, filepath.ToSlash(abs)))
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := TLS{CAFile: filepath.Join(dir, "certs", "shared.crt"), CertFile: filepath.Join(dir, "certs", "client.crt"), KeyFile: filepath.Join(dir, "certs", "client.key")}
	if cfg.APITLS != want || cfg.APIEndpoints["dhcp4"].TLS != want {
		t.Fatal("relative paths or inheritance incorrect")
	}
	if cfg.APIEndpoints["dhcp6"].TLS != (TLS{CAFile: abs}) || cfg.APIEndpoints["d2"].TLS != (TLS{}) {
		t.Fatal("TLS table must replace shared settings, including an empty table")
	}
	if cfg.APIEndpoints["dhcp4"].Auth != "none" || cfg.APIEndpoints["dhcp6"].Username != "shared" {
		t.Fatal("TLS inheritance changed authentication")
	}
}

func TestInvalidTLSConfiguration(t *testing.T) {
	for _, extra := range []string{
		"[api.tls]\ncert_file='client.crt'",
		"[api.tls]\nkey_file='client.key'",
		"[api.tls]\nca_file=''",
		"[api.tls]\ninsecure_skip_verify=true",
		"[api.tls]\nca_file='ca.crt'", // HTTP must not silently ignore TLS.
		"[api.tls.nested]\nca_file='ca.crt'",
	} {
		t.Run(extra, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "custom.toml")
			writeConfig(t, path, "[api]\nurl='http://127.0.0.1:8000'\n"+extra)
			if _, err := Load(path); err == nil {
				t.Fatal("invalid TLS settings accepted")
			}
		})
	}
}

func TestDiscoveryIgnoresCustomNames(t *testing.T) {
	isolateConfigDiscovery(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".", filepath.Join(home, ".agents"), filepath.Join(home, ".codex")} {
		path := filepath.Join(dir, "kea-custom.toml")
		writeConfig(t, path, "[api]\nurl='http://127.0.0.1:8000'\n")
		if _, err := Load(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(""); err == nil {
		t.Fatal("automatic discovery selected a custom filename")
	}
}
