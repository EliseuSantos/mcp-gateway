package main

import (
	"flag"
	"fmt"
	"io"
)

// settings is the process configuration that exists before the YAML file.
// Flags win over the environment, and the environment wins over defaults.
type settings struct {
	Listen   string
	Upstream string
}

func parseArgs(args []string, getenv func(string) string, errOut io.Writer) (settings, error) {
	fs := flag.NewFlagSet("mcp-gateway", flag.ContinueOnError)
	fs.SetOutput(errOut)
	listen := fs.String("listen", firstNonEmpty(getenv("MCP_GATEWAY_LISTEN"), ":8080"), "downstream listen address")
	upstream := fs.String("upstream", firstNonEmpty(getenv("MCP_GATEWAY_UPSTREAM"), "http://127.0.0.1:8081/mcp"), "upstream MCP endpoint")
	if err := fs.Parse(args); err != nil {
		return settings{}, err
	}
	if fs.NArg() != 0 {
		return settings{}, fmt.Errorf("unexpected arguments: %s", fs.Args())
	}
	return settings{Listen: *listen, Upstream: *upstream}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
