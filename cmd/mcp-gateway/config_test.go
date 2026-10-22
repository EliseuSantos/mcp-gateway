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
		name    string
		args    []string
		env     map[string]string
		want    flagInput
		wantErr bool
		help    bool
	}{
		{name: "defaults"},
		{
			name: "config from env",
			env:  map[string]string{"MCP_GATEWAY_CONFIG": "gateway.yaml"},
			want: flagInput{ConfigPath: "gateway.yaml"},
		},
		{
			name: "flags",
			args: []string{"-listen", ":7", "-upstream", "http://flags.test/mcp", "-config", "other.yaml"},
			env:  map[string]string{"MCP_GATEWAY_CONFIG": "gateway.yaml"},
			want: flagInput{Listen: ":7", ListenSet: true, Upstream: "http://flags.test/mcp", UpstreamSet: true, ConfigPath: "other.yaml"},
		},
		{name: "unknown flag", args: []string{"-nope"}, wantErr: true},
		{name: "extra arg", args: []string{"extra"}, wantErr: true},
		{name: "help", args: []string{"-h"}, help: true},
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
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
