package main

import (
	"errors"
	"flag"
	"io"
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
			if (err != nil) != tc.fail || path != tc.path {
				t.Fatalf("path=%q error=%v", path, err)
			}
		})
	}
	if _, err := parseFlags([]string{"--help"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help error = %v", err)
	}
}
