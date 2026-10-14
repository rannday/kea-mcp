package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/rannday/go-kit/modules/logx"

	"github.com/rannday/kea-mcp/config"
	"github.com/rannday/kea-mcp/server"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	// Console logging writes to stderr; stdout belongs to the MCP transport.
	if err := logx.Configure(logx.Config{Console: true}); err != nil {
		log.Fatalf("configure logging: %v", err)
	}

	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		logx.Fatal("parse arguments", "error", err)
	}
	cfg, err := loadConfig(opts)
	if err != nil {
		logx.Fatal("load kea-mcp config", "error", err)
	}

	mcpServer, err := server.New(cfg)
	if err != nil {
		logx.Fatal("build kea-mcp server", "error", err)
	}
	if err := mcpServer.Run(context.Background(), &sdkmcp.StdioTransport{}); err != nil {
		logx.Fatal("run kea-mcp server", "error", err)
	}
}

type launchOptions struct {
	configPath     string
	readTimeoutSec int64
}

func loadConfig(opts launchOptions) (config.Config, error) {
	cfg, err := config.Load(opts.configPath)
	if err == nil && opts.readTimeoutSec != 0 {
		cfg.ReadTimeoutSec = opts.readTimeoutSec
	}
	return cfg, err
}

func parseFlags(args []string, output io.Writer) (launchOptions, error) {
	flags := flag.NewFlagSet("kea-mcp", flag.ContinueOnError)
	flags.SetOutput(output)
	path := flags.String("config", "", "Path to TOML configuration (default: discover .kea-mcp.toml)")
	timeout := flags.Int64("read-timeout-sec", 0, "Kea HTTP request timeout in seconds (overrides api.read_timeout_sec)")
	if err := flags.Parse(args); err != nil {
		return launchOptions{}, err
	}
	if flags.NArg() != 0 {
		return launchOptions{}, errors.New("unexpected positional arguments")
	}
	var err error
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "config" && *path == "" {
			err = fmt.Errorf("--config requires a non-empty path")
		}
		if f.Name == "read-timeout-sec" {
			err = errors.Join(err, config.ValidateReadTimeoutSec(*timeout))
		}
	})
	return launchOptions{configPath: *path, readTimeoutSec: *timeout}, err
}
