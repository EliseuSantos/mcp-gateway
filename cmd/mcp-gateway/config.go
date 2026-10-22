package main

import (
	"flag"
	"fmt"
	"io"
)

// flagInput is what the command line set. Empty strings are not defaults:
// [config.Pick] applies the environment, the YAML file, and the fallback.
type flagInput struct {
	Listen      string
	ListenSet   bool
	Upstream    string
	UpstreamSet bool
	ConfigPath  string
}

func parseArgs(args []string, getenv func(string) string, errOut io.Writer) (flagInput, error) {
	fs := flag.NewFlagSet("mcp-gateway", flag.ContinueOnError)
	fs.SetOutput(errOut)
	listen := fs.String("listen", "", "downstream listen address (default :8080, or MCP_GATEWAY_LISTEN)")
	upstream := fs.String("upstream", "", "upstream MCP endpoint (default http://127.0.0.1:8081/mcp, or MCP_GATEWAY_UPSTREAM)")
	configPath := fs.String("config", getenv("MCP_GATEWAY_CONFIG"), "YAML config file (or MCP_GATEWAY_CONFIG)")
	if err := fs.Parse(args); err != nil {
		return flagInput{}, err
	}
	if fs.NArg() != 0 {
		return flagInput{}, fmt.Errorf("unexpected arguments: %s", fs.Args())
	}
	in := flagInput{Listen: *listen, Upstream: *upstream, ConfigPath: *configPath}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "listen":
			in.ListenSet = true
		case "upstream":
			in.UpstreamSet = true
		}
	})
	return in, nil
}
