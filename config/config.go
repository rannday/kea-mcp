// Package config loads TOML configuration.
package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rannday/go-kit/modules/confx"
)

var envReferenceNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Config is the runtime configuration for the kea-mcp server.
type Config struct {
	ReadOnly         bool
	RawCommand       bool
	APIMode          string
	APIService       string
	APIServices      []string
	APIURL           string
	APIUsername      string
	APIPassword      string
	MaxResponseBytes int64
}

// Load selects an explicit file, then cwd, ~/.agents, and ~/.codex in order.
// A configuration file is required.
func Load(path string) (Config, error) {
	if path != "" {
		return loadFile(path)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	for _, dir := range []string{cwd, filepath.Join(home, ".agents"), filepath.Join(home, ".codex")} {
		path := filepath.Join(dir, ".kea-mcp.toml")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return Config{}, fmt.Errorf("inspect configuration %s: %w", path, err)
		}
		return loadFile(path)
	}
	return Config{}, errors.New("no .kea-mcp.toml found: create one in the working directory, ~/.agents, or ~/.codex, or use --config PATH")
}

func loadFile(path string) (Config, error) {
	var file struct {
		API struct {
			ReadOnly         bool     `toml:"read_only"`
			Mode             string   `toml:"mode"`
			Service          string   `toml:"service"`
			Services         []string `toml:"services"`
			URL              string   `toml:"url"`
			Username         *string  `toml:"username"`
			Password         *string  `toml:"password"`
			PasswordEnv      *string  `toml:"password_env"`
			PasswordFile     *string  `toml:"password_file"`
			MaxResponseBytes *int64   `toml:"max_response_bytes"`
		} `toml:"api"`
		Tools struct {
			RawCommand bool `toml:"raw_command"`
		} `toml:"tools"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	// Always decode TOML, even when --config names a file without an extension.
	// Parser errors may quote credentials; do not include their contents in logs.
	if err := confx.DecodeInto(data, ".kea-mcp.toml", &file); err != nil {
		return Config{}, fmt.Errorf("invalid TOML configuration in %s", path)
	}
	// confx permits unknown keys; security settings must never silently default
	// because of a typo or an unexpected TOML table.
	var keys map[string]map[string]any
	if err := confx.DecodeInto(data, ".kea-mcp.toml", &keys); err != nil {
		return Config{}, errors.New("invalid configuration tables: expected api and tools")
	}
	allowed := map[string][]string{
		"api":   {"read_only", "mode", "service", "services", "url", "username", "password", "password_env", "password_file", "max_response_bytes"},
		"tools": {"raw_command"},
	}
	for table, fields := range keys {
		names, ok := allowed[table]
		if !ok {
			return Config{}, errors.New("unknown configuration table: expected api and tools")
		}
		for key := range fields {
			if !slices.Contains(names, key) {
				return Config{}, errors.New("unknown configuration key: use documented api and tools settings")
			}
		}
	}
	a := file.API
	var maxResponseBytes int64
	if a.MaxResponseBytes != nil {
		maxResponseBytes = *a.MaxResponseBytes
		if maxResponseBytes <= 0 || maxResponseBytes == math.MaxInt64 {
			return Config{}, errors.New("api.max_response_bytes must be a positive integer below 9223372036854775807")
		}
	}
	if a.Username == nil {
		return Config{}, errors.New("missing api.username")
	}
	sources := 0
	for _, source := range []*string{a.Password, a.PasswordEnv, a.PasswordFile} {
		if source != nil {
			sources++
		}
	}
	if sources != 1 {
		return Config{}, errors.New("set exactly one of api.password, api.password_env, or api.password_file")
	}
	password := ""
	if a.Password != nil {
		password = *a.Password
	} else if a.PasswordEnv != nil {
		name := strings.TrimSpace(*a.PasswordEnv)
		if !envReferenceNamePattern.MatchString(name) {
			return Config{}, errors.New("invalid api.password_env: expected environment variable name")
		}
		var ok bool
		password, ok = os.LookupEnv(name)
		if !ok {
			return Config{}, errors.New("api.password_env references an unset environment variable")
		}
	} else {
		passwordPath := *a.PasswordFile
		if strings.TrimSpace(passwordPath) == "" {
			return Config{}, errors.New("invalid api.password_file: expected a non-empty path")
		}
		if !filepath.IsAbs(passwordPath) {
			passwordPath = filepath.Join(filepath.Dir(path), passwordPath)
		}
		data, err := os.ReadFile(passwordPath)
		if err != nil {
			// Do not expose file contents or paths in credential errors.
			return Config{}, errors.New("cannot read api.password_file")
		}
		password = string(data)
	}
	endpoint := strings.TrimSpace(a.URL)
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return Config{}, errors.New("invalid api.url: expected HTTP/HTTPS URL without embedded credentials")
	}
	return normalize(Config{ReadOnly: a.ReadOnly, RawCommand: file.Tools.RawCommand, APIMode: a.Mode, APIService: a.Service, APIServices: a.Services, APIURL: strings.TrimRight(endpoint, "/"), APIUsername: *a.Username, APIPassword: password, MaxResponseBytes: maxResponseBytes})
}

func normalize(cfg Config) (Config, error) {
	cfg.APIMode = strings.ToLower(strings.TrimSpace(cfg.APIMode))
	cfg.APIService = strings.ToLower(strings.TrimSpace(cfg.APIService))
	cfg.APIUsername = strings.TrimSpace(cfg.APIUsername)
	cfg.APIPassword = strings.TrimSpace(cfg.APIPassword)
	if cfg.APIMode == "" {
		cfg.APIMode = "control-agent"
	}
	if cfg.APIMode != "control-agent" && cfg.APIMode != "direct" {
		return Config{}, errors.New("invalid API mode: expected control-agent or direct")
	}
	if cfg.APIMode == "direct" && cfg.APIService != "dhcp4" && cfg.APIService != "dhcp6" && cfg.APIService != "d2" {
		return Config{}, errors.New("invalid API service: direct mode requires dhcp4, dhcp6, or d2")
	}
	if cfg.APIServices != nil {
		if cfg.APIMode == "direct" || len(cfg.APIServices) == 0 {
			return Config{}, errors.New("api.services requires a non-empty list in control-agent mode")
		}
		seen := make(map[string]bool)
		for i, service := range cfg.APIServices {
			service = strings.ToLower(strings.TrimSpace(service))
			if service != "agent" && service != "dhcp4" && service != "dhcp6" && service != "d2" {
				return Config{}, errors.New("invalid api.services: expected agent, dhcp4, dhcp6, or d2")
			}
			if seen[service] {
				return Config{}, errors.New("api.services contains duplicate services")
			}
			seen[service] = true
			cfg.APIServices[i] = service
		}
	}
	return cfg, nil
}
