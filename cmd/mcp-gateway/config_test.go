package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		args     []string
		env      map[string]string
		listen   string
		upstream string
		wantErr  bool
		help     bool
	}{
		{
			name:     "defaults",
			listen:   ":8080",
			upstream: "http://127.0.0.1:8081/mcp",
		},
		{
			name:     "env",
			env:      map[string]string{"MCP_GATEWAY_LISTEN": ":9090", "MCP_GATEWAY_UPSTREAM": "http://echo:8081/mcp"},
			listen:   ":9090",
			upstream: "http://echo:8081/mcp",
		},
		{
			name:     "flags win",
			args:     []string{"-listen", ":7", "-upstream", "http://flags.test/mcp"},
			env:      map[string]string{"MCP_GATEWAY_LISTEN": ":9090", "MCP_GATEWAY_UPSTREAM": "http://env.test/mcp"},
			listen:   ":7",
			upstream: "http://flags.test/mcp",
		},
		{
			name:    "unknown flag",
			args:    []string{"-nope"},
			wantErr: true,
		},
		{
			name:    "extra arg",
			args:    []string{"extra"},
			wantErr: true,
		},
		{
			name: "help",
			args: []string{"-h"},
			help: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			got, err := parseArgs(tc.args, func(key string) string { return tc.env[key] }, &buf)
			if tc.help {
				if !errors.Is(err, flag.ErrHelp) {
					t.Fatalf("error = %v, want flag.ErrHelp", err)
				}
				if !strings.Contains(buf.String(), "listen") {
					t.Fatalf("help = %q", buf.String())
				}
				return
			}
			if tc.wantErr {
				if err == nil {
					t.Fatal("error = nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Listen != tc.listen || got.Upstream != tc.upstream {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
