package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rannday/kea-mcp/kea"
)

func isolateConfigDiscovery(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestMissingConfiguration(t *testing.T) {
	isolateConfigDiscovery(t)
	// Old environment settings cannot replace a configuration file.
	t.Setenv("KEA_MCP_API_URL", "https://kea.example")
	t.Setenv("KEA_MCP_API_USERNAME", "noc")
	t.Setenv("KEA_MCP_API_PASSWORD", "unused")
	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), ".kea-mcp.toml") || !strings.Contains(err.Error(), "--config") {
		t.Fatalf("expected actionable missing configuration error, got %v", err)
	}
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestFilePrecedence(t *testing.T) {
	isolateConfigDiscovery(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(t.TempDir(), "custom"),
		filepath.Join(cwd, ".kea-mcp.toml"),
		filepath.Join(home, ".agents", ".kea-mcp.toml"),
		filepath.Join(home, ".codex", ".kea-mcp.toml"),
	}
	for i, path := range paths {
		writeConfig(t, path, "[api]\nurl='https://kea.example'\nusername='file'\npassword='secret'\nservice='"+[]string{"explicit", "cwd", "agents", "codex"}[i]+"'\n")
	}
	cfg, err := Load(paths[0])
	if err != nil || cfg.APIService != "explicit" {
		t.Fatalf("explicit: %#v %v", cfg.APIService, err)
	}
	for i := 1; i < len(paths); i++ {
		cfg, err := Load("")
		if err != nil || cfg.APIService != []string{"", "cwd", "agents", "codex"}[i] || cfg.APIUsername != "file" || cfg.APIURL != "https://kea.example" {
			t.Fatalf("discovery at %d: service=%q error=%v", i, cfg.APIService, err)
		}
		if err := os.Remove(paths[i]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelectedFileFailure(t *testing.T) {
	isolateConfigDiscovery(t)
	cwd, _ := os.Getwd()
	path := filepath.Join(cwd, ".kea-mcp.toml")
	if _, err := Load(path); err == nil {
		t.Fatal("missing explicit file accepted")
	}
	writeConfig(t, path, "[api]\npassword = secret-sentinel")
	if _, err := Load(""); err == nil || strings.Contains(err.Error(), "secret-sentinel") {
		t.Fatalf("broken selected file or secret protection: %v", err)
	}
}

func TestFileSettings(t *testing.T) {
	isolateConfigDiscovery(t)
	t.Setenv("KEA_TEST_PASSWORD", " referenced ")
	base := "[api]\nurl='https://kea.example/'\nusername=' file '\n"
	for _, tc := range []struct {
		name, body     string
		fail           bool
		mode, password string
	}{
		{"defaults", "password=' secret '", false, "control-agent", "secret"},
		{"reference", "password_env='KEA_TEST_PASSWORD'", false, "control-agent", "referenced"},
		{"direct dhcp4", "mode='direct'\nservice='dhcp4'\npassword='secret'", false, "direct", "secret"},
		{"direct d2", "mode='direct'\nservice='d2'\npassword='secret'", false, "direct", "secret"},
		{"direct dhcp6", "mode=' Direct '\nservice=' DHCP6 '\npassword='secret'", false, "direct", "secret"},
		{"missing password", "", true, "", ""},
		{"both passwords", "password='x'\npassword_env='KEA_TEST_PASSWORD'", true, "", ""},
		{"password and file", "password='x'\npassword_file='unused'", true, "", ""},
		{"environment and file", "password_env='KEA_TEST_PASSWORD'\npassword_file='unused'", true, "", ""},
		{"all password sources", "password='x'\npassword_env='KEA_TEST_PASSWORD'\npassword_file='unused'", true, "", ""},
		{"missing reference", "password_env='MISSING_KEA_TEST_PASSWORD'", true, "", ""},
		{"invalid reference", "password_env='bad name'", true, "", ""},
		{"invalid mode", "mode='auto'\npassword='x'", true, "", ""},
		{"missing direct service", "mode='direct'\npassword='x'", true, "", ""},
		{"invalid direct service", "mode='direct'\nservice='agent'\npassword='x'", true, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
			writeConfig(t, path, base+tc.body)
			cfg, err := Load(path)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v want failure=%v", err, tc.fail)
			}
			if err == nil && (cfg.APIMode != tc.mode || cfg.APIPassword != tc.password || cfg.APIUsername != "file" || cfg.APIURL != "https://kea.example/") {
				t.Fatal("file configuration did not normalize or isolate settings")
			}
		})
	}
	for _, body := range []string{
		"[api]\nurl='https://kea.example'\npassword='x'",
		"[api]\nurl='ftp://kea.example'\nusername='x'\npassword='x'",
		"[api]\nurl='https://user:secret@kea.example'\nusername='x'\npassword='x'",
	} {
		path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
		writeConfig(t, path, body)
		if _, err := Load(path); err == nil {
			t.Fatal("invalid file accepted")
		}
	}
}

func TestEndpointURLPreservation(t *testing.T) {
	for _, endpoint := range []string{
		"https://kea.example/",
		"https://kea.example/kea/",
		"https://kea.example/kea/?target=/",
		"https://kea.example/kea//",
	} {
		for _, tc := range []struct {
			name, prefix, service string
		}{
			{"control agent", "[api]\n", ""},
			{"direct shared", "[api]\nmode='direct'\nservice='dhcp4'\n", ""},
			{"direct dhcp4", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\n", "dhcp4"},
			{"direct dhcp6", "[api]\nmode='direct'\n[api.endpoints.dhcp6]\n", "dhcp6"},
			{"direct d2", "[api]\nmode='direct'\n[api.endpoints.d2]\n", "d2"},
		} {
			t.Run(tc.name+"/"+endpoint, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
				writeConfig(t, path, tc.prefix+"url='  "+endpoint+"  '\n")
				cfg, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				got := cfg.APIURL
				if tc.service != "" {
					got = cfg.APIEndpoints[tc.service].URL
				}
				if got != endpoint {
					t.Fatalf("URL=%q want %q", got, endpoint)
				}
			})
		}
	}
}

func TestConfiguredProxyEndpoint(t *testing.T) {
	var accepted atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/kea" {
			http.Redirect(w, r, "/kea/", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path != "/kea/" || r.Method != http.MethodPost {
			http.Error(w, "expected POST /kea/", http.StatusBadRequest)
			return
		}
		accepted.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"result":0}]`))
	}))
	defer proxy.Close()
	for _, tc := range []struct {
		name, prefix, service string
	}{
		{"shared", "[api]\n", ""},
		{"direct endpoint", "[api]\nmode='direct'\n[api.endpoints.dhcp4]\n", "dhcp4"},
	} {
		for _, suffix := range []string{"/kea/", "/kea"} {
			t.Run(tc.name+suffix, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
				writeConfig(t, path, tc.prefix+"url=' "+proxy.URL+suffix+" '\n")
				cfg, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				endpoint := cfg.APIURL
				var options []kea.HTTPOption
				req := kea.CommandRequest{Command: "status-get"}
				if tc.service != "" {
					endpoint = cfg.APIEndpoints[tc.service].URL
					options = append(options, kea.WithDirectService(kea.Service(tc.service)))
					req.Service = []kea.Service{kea.Service(tc.service)}
				}
				before := accepted.Load()
				var responses []kea.CommandResponse
				err = kea.NewHTTPTransport(endpoint, options...).Call(context.Background(), req, &responses)
				if suffix == "/kea/" {
					if err != nil || len(responses) != 1 || accepted.Load() != before+1 {
						t.Fatalf("configured path failed: responses=%v error=%v", responses, err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "status=307") || accepted.Load() != before {
					t.Fatalf("redirect was not rejected: %v", err)
				}
			})
		}
	}
}

func TestPasswordFile(t *testing.T) {
	isolateConfigDiscovery(t)
	dir := t.TempDir()
	passwordPath := filepath.Join(dir, "credentials", "password.txt")
	writeConfig(t, passwordPath, " synthetic-password \r\n")
	path := filepath.Join(dir, ".kea-mcp.toml")
	base := "[api]\nurl='https://kea.example'\nusername='file'\n"
	for _, tc := range []struct {
		name, file string
		fail       bool
	}{
		{"relative", "credentials/password.txt", false},
		{"absolute", passwordPath, false},
		{"missing", "missing-secret-sentinel", true},
		{"directory", "credentials", true},
		{"empty path", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeConfig(t, path, base+"password_file='"+tc.file+"'\n")
			cfg, err := Load(path)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v want failure=%v", err, tc.fail)
			}
			if err != nil && (strings.Contains(err.Error(), "secret-sentinel") || strings.Contains(err.Error(), dir)) {
				t.Fatal("credential error exposed a file path")
			}
			if err == nil && cfg.APIPassword != "synthetic-password" {
				t.Fatal("password file contents were not loaded and normalized")
			}
		})
	}
}

func TestReadOnlyConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, setting  string
		readOnly, fail bool
	}{
		{"default", "", false, false},
		{"enabled", "read_only=true", true, false},
		{"disabled", "read_only=false", false, false},
		{"invalid", "read_only='true'", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
			writeConfig(t, path, "[api]\nurl='https://kea.example'\nusername='test'\npassword='test'\n"+tc.setting)
			cfg, err := Load(path)
			if (err != nil) != tc.fail || (err == nil && cfg.ReadOnly != tc.readOnly) {
				t.Fatalf("read_only=%v error=%v", cfg.ReadOnly, err)
			}
		})
	}
}

func TestUnknownConfigurationKeys(t *testing.T) {
	for _, extra := range []string{
		"read_onyl=true", "servcies=['dhcp4']", "[tools]\nraw_comand=true",
		"[unknown]\nread_only=true", "[api.unknown]\nvalue='secret-sentinel'",
	} {
		path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
		writeConfig(t, path, "[api]\nurl='https://kea.example'\nusername='test'\npassword='secret-sentinel'\n"+extra)
		if _, err := Load(path); err == nil || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatalf("unknown setting accepted or credential exposed: %v", err)
		}
	}
}

func TestResponseLimitConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
		fail  bool
	}{
		{"1024", 1024, false}, {"0", 0, true}, {"-1", 0, true},
		{"9223372036854775807", 0, true}, {"'1024'", 0, true},
	} {
		path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
		writeConfig(t, path, "[api]\nurl='https://kea.example'\nusername='test'\npassword='test'\nmax_response_bytes="+tc.value)
		cfg, err := Load(path)
		if (err != nil) != tc.fail || (err == nil && cfg.MaxResponseBytes != tc.want) {
			t.Fatalf("limit %s: got %d, error %v", tc.value, cfg.MaxResponseBytes, err)
		}
	}
}

func TestReadTimeoutConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
		fail  bool
	}{
		{"", 0, false}, {"90", 90, false}, {"9223372036", 9223372036, false},
		{"0", 0, true}, {"-1", 0, true}, {"9223372037", 0, true},
		{"1.5", 0, true}, {"'90'", 0, true},
	} {
		path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
		body := "[api]\nurl='https://kea.example'\nusername='test'\npassword='test'\n"
		if tc.value != "" {
			body += "read_timeout_sec=" + tc.value
		}
		writeConfig(t, path, body)
		cfg, err := Load(path)
		if (err != nil) != tc.fail || (err == nil && cfg.ReadTimeoutSec != tc.want) {
			t.Fatalf("timeout %s: got %d, error %v", tc.value, cfg.ReadTimeoutSec, err)
		}
	}
}

func TestServiceAndToolConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		services       []string
		raw, fail      bool
	}{
		{"defaults", "", nil, false, false},
		{"restricted", "services=[' DHCP4 ', 'dhcp6']", []string{"dhcp4", "dhcp6"}, false, false},
		{"raw opt-in", "services=['dhcp4']\n[tools]\nraw_command=true", []string{"dhcp4"}, true, false},
		{"empty services", "services=[]", nil, false, true},
		{"duplicate", "services=['dhcp4',' DHCP4 ']", nil, false, true},
		{"unknown", "services=['dhcp5']", nil, false, true},
		{"direct services", "mode='direct'\nservice='dhcp4'\nservices=['dhcp4']", nil, false, true},
		{"invalid raw", "[tools]\nraw_command='true'", nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".kea-mcp.toml")
			writeConfig(t, path, "[api]\nurl='https://kea.example'\nusername='test'\npassword='test'\n"+tc.settings)
			cfg, err := Load(path)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v want failure=%v", err, tc.fail)
			}
			if err == nil && (cfg.RawCommand != tc.raw || strings.Join(cfg.APIServices, ",") != strings.Join(tc.services, ",") || (cfg.APIServices == nil) != (tc.services == nil)) {
				t.Fatalf("services=%v raw=%v", cfg.APIServices, cfg.RawCommand)
			}
		})
	}
}
