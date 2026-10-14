package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestParseFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		path string
		fail bool
	}{
		{"discovery", nil, "", false},
		{"explicit", []string{"--config", "custom.toml"}, "custom.toml", false},
		{"empty", []string{"--config="}, "", true},
		{"missing", []string{"--config"}, "", true},
		{"unknown", []string{"--unknown"}, "", true},
		{"positional", []string{"mcp"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := parseFlags(tc.args, io.Discard)
			if (err != nil) != tc.fail || path.configPath != tc.path {
				t.Fatalf("options=%+v error=%v", path, err)
			}
		})
	}
	if _, err := parseFlags([]string{"--help"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help error = %v", err)
	}
}

func TestReadTimeoutFlag(t *testing.T) {
	for _, value := range []string{"90", "0", "-1", "9223372037", "1.5", "invalid"} {
		opts, err := parseFlags([]string{"--read-timeout-sec", value}, io.Discard)
		if value == "90" {
			if err != nil || opts.readTimeoutSec != 90 {
				t.Fatalf("options=%+v error=%v", opts, err)
			}
		} else if err == nil {
			t.Fatalf("invalid timeout %s accepted", value)
		}
	}
}

func TestTimeoutLaunchPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[api]\nurl='https://kea.example'\nusername='test'\npassword='test'\nread_timeout_sec=30\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, override := range []int64{0, 90} {
		cfg, err := loadConfig(launchOptions{configPath: path, readTimeoutSec: override})
		want := int64(30)
		if override != 0 {
			want = override
		}
		if err != nil || cfg.ReadTimeoutSec != want {
			t.Fatalf("timeout=%d error=%v", cfg.ReadTimeoutSec, err)
		}
	}
	if _, err := parseFlags([]string{"--config=", "--read-timeout-sec=90"}, io.Discard); err == nil {
		t.Fatal("timeout hid invalid config flag")
	}
}
