package config

import (
	"path/filepath"
	"testing"
)

func TestAuthenticationAndEndpointConfiguration(t *testing.T) {
	t.Setenv("KEA_ENDPOINT_PASSWORD", "local-password")
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, "password.txt"), "file-password\n")
	for _, tc := range []struct {
		name, body string
		fail       bool
	}{
		{"implicit no auth", "[api]\nurl='http://kea.example'", false},
		{"explicit no auth", "[api]\nurl='http://kea.example'\nauth='none'", false},
		{"direct no auth", "[api]\nmode='direct'\nservice='d2'\nurl='http://kea.example'", false},
		{"none with credentials", "[api]\nurl='http://kea.example'\nauth='none'\nusername='unused'", true},
		{"invalid auth", "[api]\nurl='http://kea.example'\nauth='bearer'", true},
		{"incomplete basic", "[api]\nurl='http://kea.example'\nauth='basic'", true},
		{"independent credentials", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\nurl='http://four.example'\nusername='four'\npassword='four-password'", false},
		{"mixed auth", "[api]\nmode='direct'\nusername='shared'\npassword='shared-password'\n[api.endpoints.dhcp4]\nurl='http://four.example/'\n[api.endpoints.dhcp6]\nurl='http://six.example'\nusername='six'\npassword_env='KEA_ENDPOINT_PASSWORD'\n[api.endpoints.d2]\nurl='http://ddns.example'\nauth='none'", false},
		{"file override", "[api]\nmode='direct'\n[api.endpoints.d2]\nurl='http://ddns.example'\nusername='ddns'\npassword_file='password.txt'", false},
		{"all unauthenticated", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\nurl='http://four.example'\n[api.endpoints.dhcp6]\nurl='http://six.example'", false},
		{"partial override", "[api]\nmode='direct'\nusername='shared'\npassword='shared-password'\n[api.endpoints.dhcp4]\nurl='http://four.example'\nusername='four'", true},
		{"multiple sources", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\nurl='http://four.example'\nusername='four'\npassword='x'\npassword_env='KEA_ENDPOINT_PASSWORD'", true},
		{"endpoint none with credentials", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\nurl='http://four.example'\nauth='none'\npassword_file='unused'", true},
		{"endpoint typo", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\nurl='http://four.example'\nauthentication='none'", true},
		{"unknown daemon", "[api]\nmode='direct'\n[api.endpoints.agent]\nurl='http://agent.example'", true},
		{"control agent endpoints", "[api]\n[api.endpoints.dhcp4]\nurl='http://four.example'", true},
		{"conflicting url", "[api]\nmode='direct'\nurl='http://agent.example'\n[api.endpoints.dhcp4]\nurl='http://four.example'", true},
		{"conflicting service", "[api]\nmode='direct'\nservice='dhcp4'\n[api.endpoints.dhcp4]\nurl='http://four.example'", true},
		{"conflicting services", "[api]\nmode='direct'\nservices=['dhcp4']\n[api.endpoints.dhcp4]\nurl='http://four.example'", true},
		{"missing endpoint url", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\nauth='none'", true},
		{"embedded credentials", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\nurl='http://user:secret@four.example'", true},
		{"empty endpoints", "[api]\nmode='direct'\n[api.endpoints]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "test.toml")
			writeConfig(t, path, tc.body)
			cfg, err := Load(path)
			if (err != nil) != tc.fail {
				t.Fatalf("error = %v, want failure %v", err, tc.fail)
			}
			if err != nil {
				return
			}
			switch tc.name {
			case "mixed auth":
				want := map[string]Endpoint{
					"dhcp4": {URL: "http://four.example", Auth: "basic", Username: "shared", Password: "shared-password"},
					"dhcp6": {URL: "http://six.example", Auth: "basic", Username: "six", Password: "local-password"},
					"d2":    {URL: "http://ddns.example", Auth: "none"},
				}
				for service, endpoint := range want {
					if cfg.APIEndpoints[service] != endpoint {
						t.Errorf("%s authentication or URL resolved incorrectly", service)
					}
				}
			case "file override":
				if cfg.APIEndpoints["d2"].Password != "file-password" {
					t.Error("endpoint password file not resolved from config directory")
				}
			case "implicit no auth", "explicit no auth", "direct no auth":
				if cfg.APIAuth != "none" {
					t.Error("expected no authentication")
				}
			}
		})
	}
}
