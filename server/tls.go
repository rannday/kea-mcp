package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"

	"github.com/rannday/kea-mcp/config"
)

func certificateTransport(settings config.TLS) (*http.Transport, error) {
	security := &tls.Config{MinVersion: tls.VersionTLS12}
	if settings.CAFile != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, errors.New("cannot load system certificate trust")
		}
		data, err := os.ReadFile(settings.CAFile)
		if err != nil {
			return nil, errors.New("cannot read TLS ca_file")
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, errors.New("TLS ca_file contains no usable certificates")
		}
		security.RootCAs = roots
	}
	if settings.CertFile != "" {
		identity, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile)
		if err != nil {
			return nil, errors.New("cannot load TLS client certificate and key")
		}
		security.Certificates = []tls.Certificate{identity}
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport does not support TLS configuration")
	}
	transport := base.Clone()
	transport.TLSClientConfig = security
	return transport, nil
}
