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

	path, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		logx.Fatal("parse arguments", "error", err)
	}
	cfg, err := config.Load(path)
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

func parseFlags(args []string, output io.Writer) (string, error) {
	flags := flag.NewFlagSet("kea-mcp", flag.ContinueOnError)
	flags.SetOutput(output)
	path := flags.String("config", "", "Path to TOML configuration (default: discover .kea-mcp.toml)")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() != 0 {
		return "", errors.New("unexpected positional arguments")
	}
	var err error
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "config" && *path == "" {
			err = fmt.Errorf("--config requires a non-empty path")
		}
	})
	return *path, err
}
