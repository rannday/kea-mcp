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
	"time"

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
	APIAuth          string
	APITLS           TLS
	APIEndpoints     map[string]Endpoint
	MaxResponseBytes int64
	ReadTimeoutSec   int64
}

// Endpoint is a direct daemon API with resolved authentication settings.
type Endpoint struct {
	URL      string
	Auth     string
	Username string
	Password string
	TLS      TLS
}

type fileEndpoint struct {
	URL          string   `toml:"url"`
	Auth         string   `toml:"auth"`
	Username     *string  `toml:"username"`
	Password     *string  `toml:"password"`
	PasswordEnv  *string  `toml:"password_env"`
	PasswordFile *string  `toml:"password_file"`
	TLS          *fileTLS `toml:"tls"`
}

// ValidateReadTimeoutSec ensures seconds fit in a positive Go duration.
func ValidateReadTimeoutSec(seconds int64) error {
	if seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
		return errors.New("read_timeout_sec must be a positive integer no greater than 9223372036")
	}
	return nil
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
			ReadOnly         bool                    `toml:"read_only"`
			Mode             string                  `toml:"mode"`
			Service          string                  `toml:"service"`
			Services         []string                `toml:"services"`
			URL              string                  `toml:"url"`
			Auth             string                  `toml:"auth"`
			TLS              *fileTLS                `toml:"tls"`
			Endpoints        map[string]fileEndpoint `toml:"endpoints"`
			Username         *string                 `toml:"username"`
			Password         *string                 `toml:"password"`
			PasswordEnv      *string                 `toml:"password_env"`
			PasswordFile     *string                 `toml:"password_file"`
			MaxResponseBytes *int64                  `toml:"max_response_bytes"`
			ReadTimeoutSec   *int64                  `toml:"read_timeout_sec"`
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
		"api":   {"read_only", "mode", "service", "services", "url", "auth", "tls", "endpoints", "username", "password", "password_env", "password_file", "max_response_bytes", "read_timeout_sec"},
		"tools": {"raw_command"},
	}
	if err := validateTLSKeys(keys["api"]); err != nil {
		return Config{}, err
	}
	if raw, exists := keys["api"]["endpoints"]; exists {
		endpoints, ok := raw.(map[string]any)
		if !ok || len(endpoints) == 0 {
			return Config{}, errors.New("api.endpoints requires daemon endpoint tables")
		}
		for name, rawEndpoint := range endpoints {
			if name != "dhcp4" && name != "dhcp6" && name != "d2" {
				return Config{}, errors.New("api.endpoints supports only dhcp4, dhcp6, and d2")
			}
			fields, ok := rawEndpoint.(map[string]any)
			if !ok {
				return Config{}, errors.New("invalid api.endpoints table")
			}
			if err := validateTLSKeys(fields); err != nil {
				return Config{}, err
			}
			for key := range fields {
				if !slices.Contains([]string{"url", "auth", "tls", "username", "password", "password_env", "password_file"}, key) {
					return Config{}, errors.New("unknown endpoint key: use documented URL and authentication settings")
				}
			}
		}
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
	var readTimeoutSec int64
	if a.ReadTimeoutSec != nil {
		readTimeoutSec = *a.ReadTimeoutSec
		if err := ValidateReadTimeoutSec(readTimeoutSec); err != nil {
			return Config{}, fmt.Errorf("api.%w", err)
		}
	}
	var maxResponseBytes int64
	if a.MaxResponseBytes != nil {
		maxResponseBytes = *a.MaxResponseBytes
		if maxResponseBytes <= 0 || maxResponseBytes == math.MaxInt64 {
			return Config{}, errors.New("api.max_response_bytes must be a positive integer below 9223372036854775807")
		}
	}
	defaults, err := resolveCredentials(fileEndpoint{Auth: a.Auth, Username: a.Username, Password: a.Password, PasswordEnv: a.PasswordEnv, PasswordFile: a.PasswordFile}, path)
	if err != nil {
		return Config{}, fmt.Errorf("api: %w", err)
	}
	cfg := Config{ReadOnly: a.ReadOnly, RawCommand: file.Tools.RawCommand, APIMode: a.Mode, APIService: a.Service, APIServices: a.Services, APIURL: a.URL, APIAuth: defaults.Auth, APIUsername: defaults.Username, APIPassword: defaults.Password, MaxResponseBytes: maxResponseBytes, ReadTimeoutSec: readTimeoutSec}
	cfg.APITLS, err = resolveTLS(a.TLS, path)
	if err != nil {
		return Config{}, fmt.Errorf("api.tls: %w", err)
	}
	if a.Endpoints != nil {
		cfg.APIEndpoints = make(map[string]Endpoint, len(a.Endpoints))
		for name, settings := range a.Endpoints {
			endpoint := defaults
			if settings.Auth != "" || hasCredentials(settings) {
				endpoint, err = resolveCredentials(settings, path)
				if err != nil {
					return Config{}, fmt.Errorf("api.endpoints.%s: %w", name, err)
				}
			}
			endpoint.URL, err = validateURL(settings.URL)
			if err != nil {
				return Config{}, fmt.Errorf("api.endpoints.%s: %w", name, err)
			}
			endpoint.TLS = cfg.APITLS
			if settings.TLS != nil {
				endpoint.TLS, err = resolveTLS(settings.TLS, path)
				if err != nil {
					return Config{}, fmt.Errorf("api.endpoints.%s.tls: %w", name, err)
				}
			}
			if err := endpoint.TLS.Validate(endpoint.URL); err != nil {
				return Config{}, fmt.Errorf("api.endpoints.%s: %w", name, err)
			}
			cfg.APIEndpoints[name] = endpoint
		}
	} else {
		cfg.APIURL, err = validateURL(a.URL)
		if err != nil {
			return Config{}, fmt.Errorf("api: %w", err)
		}
		if err := cfg.APITLS.Validate(cfg.APIURL); err != nil {
			return Config{}, err
		}
	}
	return normalize(cfg)
}

func hasCredentials(a fileEndpoint) bool {
	return a.Username != nil || a.Password != nil || a.PasswordEnv != nil || a.PasswordFile != nil
}

func resolveCredentials(a fileEndpoint, path string) (Endpoint, error) {
	auth := strings.ToLower(strings.TrimSpace(a.Auth))
	if auth == "" {
		auth = "none"
		if hasCredentials(a) {
			auth = "basic"
		}
	}
	if auth == "none" {
		if hasCredentials(a) {
			return Endpoint{}, errors.New("auth=none cannot include credentials")
		}
		return Endpoint{Auth: "none"}, nil
	}
	if auth != "basic" {
		return Endpoint{}, errors.New("auth must be basic or none")
	}
	if a.Username == nil {
		return Endpoint{}, errors.New("missing username for Basic auth")
	}
	sources := 0
	for _, source := range []*string{a.Password, a.PasswordEnv, a.PasswordFile} {
		if source != nil {
			sources++
		}
	}
	if sources != 1 {
		return Endpoint{}, errors.New("set exactly one of password, password_env, or password_file for Basic auth")
	}
	password := ""
	if a.Password != nil {
		password = *a.Password
	} else if a.PasswordEnv != nil {
		name := strings.TrimSpace(*a.PasswordEnv)
		if !envReferenceNamePattern.MatchString(name) {
			return Endpoint{}, errors.New("invalid password_env: expected environment variable name")
		}
		var ok bool
		password, ok = os.LookupEnv(name)
		if !ok {
			return Endpoint{}, errors.New("password_env references an unset environment variable")
		}
	} else {
		passwordPath := *a.PasswordFile
		if strings.TrimSpace(passwordPath) == "" {
			return Endpoint{}, errors.New("invalid password_file: expected a non-empty path")
		}
		if !filepath.IsAbs(passwordPath) {
			passwordPath = filepath.Join(filepath.Dir(path), passwordPath)
		}
		data, err := os.ReadFile(passwordPath)
		if err != nil {
			// Do not expose file contents or paths in credential errors.
			return Endpoint{}, errors.New("cannot read password_file")
		}
		password = string(data)
	}
	username := strings.TrimSpace(*a.Username)
	password = strings.TrimSpace(password)
	if username == "" && password == "" {
		return Endpoint{}, errors.New("basic auth requires non-empty credentials")
	}
	return Endpoint{Auth: "basic", Username: username, Password: password}, nil
}

func validateURL(raw string) (string, error) {
	endpoint := strings.TrimSpace(raw)
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", errors.New("invalid url: expected HTTP/HTTPS URL without embedded credentials")
	}
	return endpoint, nil
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
	if cfg.APIEndpoints != nil && (cfg.APIMode != "direct" || cfg.APIService != "" || strings.TrimSpace(cfg.APIURL) != "" || len(cfg.APIEndpoints) == 0) {
		return Config{}, errors.New("api.endpoints requires direct mode and cannot be combined with api.url or api.service")
	}
	if cfg.APIMode == "direct" && cfg.APIEndpoints == nil && cfg.APIService != "dhcp4" && cfg.APIService != "dhcp6" && cfg.APIService != "d2" {
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
