package config

import (
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

// TLS holds resolved certificate paths. An empty value uses system trust.
type TLS struct {
	CAFile   string
	CertFile string
	KeyFile  string
}

type fileTLS struct {
	CAFile   *string `toml:"ca_file"`
	CertFile *string `toml:"cert_file"`
	KeyFile  *string `toml:"key_file"`
}

// Validate rejects incomplete client identities and TLS settings on HTTP.
func (settings TLS) Validate(endpoint string) error {
	if (settings.CertFile == "") != (settings.KeyFile == "") {
		return errors.New("TLS requires both cert_file and key_file")
	}
	if settings != (TLS{}) {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" {
			return errors.New("TLS certificate settings require an HTTPS URL")
		}
	}
	return nil
}

func validateTLSKeys(fields map[string]any) error {
	raw, exists := fields["tls"]
	if !exists {
		return nil
	}
	keys, ok := raw.(map[string]any)
	if !ok {
		return errors.New("tls must be a table")
	}
	for key := range keys {
		if !slices.Contains([]string{"ca_file", "cert_file", "key_file"}, key) {
			return errors.New("unknown TLS key: expected ca_file, cert_file, or key_file")
		}
	}
	return nil
}

func resolveTLS(settings *fileTLS, configPath string) (TLS, error) {
	var resolved TLS
	if settings == nil {
		return resolved, nil
	}
	for _, field := range []struct {
		input  *string
		output *string
	}{{settings.CAFile, &resolved.CAFile}, {settings.CertFile, &resolved.CertFile}, {settings.KeyFile, &resolved.KeyFile}} {
		if field.input == nil {
			continue
		}
		if strings.TrimSpace(*field.input) == "" {
			return TLS{}, errors.New("TLS certificate paths must not be empty")
		}
		path := *field.input
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(configPath), path)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return TLS{}, errors.New("cannot resolve TLS certificate path")
		}
		*field.output = absolute
	}
	return resolved, resolved.Validate("https://localhost")
}
