package config

import (
	"strings"
	"testing"
)

func TestPick(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		flag     string
		flagSet  bool
		env      string
		file     string
		fallback string
		want     string
	}{
		{name: "fallback", fallback: ":8080", want: ":8080"},
		{name: "file", file: ":1", fallback: ":8080", want: ":1"},
		{name: "env over file", env: ":2", file: ":1", fallback: ":8080", want: ":2"},
		{name: "flag over env", flag: ":3", flagSet: true, env: ":2", file: ":1", fallback: ":8080", want: ":3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Pick(tc.flag, tc.flagSet, tc.env, tc.file, tc.fallback)
			if got != tc.want {
				t.Fatalf("Pick() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	const doc = `
listen: ":9090"
upstream: http://echo:8081/mcp
auth:
  api_keys:
    - id: demo
      key: dev-demo-key
  jwt:
    hmac_secret: dev-hmac
    issuer: mcp-gateway
    audience: mcp-gateway
policy:
  clients:
    demo:
      tools: [echo, time]
`
	file, err := decode(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if file.Listen != ":9090" || file.Upstream != "http://echo:8081/mcp" {
		t.Fatalf("listen/upstream = %q %q", file.Listen, file.Upstream)
	}
	if file.Auth == nil || len(file.Auth.APIKeys) != 1 || file.Auth.APIKeys[0].ID != "demo" {
		t.Fatalf("auth = %+v", file.Auth)
	}
	if file.Auth.JWT == nil || file.Auth.JWT.ClientIDClaimOrDefault() != "sub" {
		t.Fatalf("jwt = %+v", file.Auth.JWT)
	}
	if got := file.Policy.Clients["demo"].Tools; len(got) != 2 || got[0] != "echo" {
		t.Fatalf("tools = %#v", got)
	}

	if _, err := Load(""); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		yaml string
		want string
	}{
		{name: "policy only", yaml: "policy:\n  clients: {}\n", want: "policy requires auth"},
		{name: "auth only", yaml: "auth:\n  api_keys:\n    - {id: demo, key: k}\n", want: "auth requires policy"},
		{name: "empty auth", yaml: "auth: {}\npolicy:\n  clients: {}\n", want: "api key or jwt"},
		{name: "duplicate key", yaml: "auth:\n  api_keys:\n    - {id: a, key: same}\n    - {id: b, key: same}\npolicy:\n  clients: {}\n", want: "duplicate api key"},
		{name: "two rsa sources", yaml: "auth:\n  jwt:\n    public_key: pem\n    jwks_url: https://example.test/jwks\npolicy:\n  clients: {}\n", want: "one RS256 source"},
		{name: "empty jwt", yaml: "auth:\n  jwt: {}\npolicy:\n  clients: {}\n", want: "hmac_secret"},
		{name: "empty tool", yaml: "auth:\n  api_keys:\n    - {id: demo, key: k}\npolicy:\n  clients:\n    demo:\n      tools: ['']\n", want: "empty tool"},
		{name: "duplicate tool", yaml: "auth:\n  api_keys:\n    - {id: demo, key: k}\npolicy:\n  clients:\n    demo:\n      tools: [echo, echo]\n", want: "more than once"},
		{name: "unknown field", yaml: "listen: :8080\nnope: 1\n", want: "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := decode(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("error = nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
