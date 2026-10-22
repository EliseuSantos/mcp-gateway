// Package config loads the gateway YAML file.
//
// Flags and the environment are applied by [Pick]: an explicit flag wins,
// then the environment, then the file, then the fallback. Auth and policy
// are either both set or both absent. One without the other is a
// configuration error, because an authenticated client with no allowlist
// could call every tool, and an allowlist with no identity would deny
// every tool without a clear reason.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the YAML document. Nil Auth and Policy mean the pass-through.
type File struct {
	Listen   string      `yaml:"listen"`
	Upstream string      `yaml:"upstream"`
	Auth     *AuthFile   `yaml:"auth"`
	Policy   *PolicyFile `yaml:"policy"`
}

// AuthFile is the `auth` section.
type AuthFile struct {
	APIKeys []APIKey `yaml:"api_keys"`
	JWT     *JWTFile `yaml:"jwt"`
}

// APIKey maps a secret to a client id.
type APIKey struct {
	ID  string `yaml:"id"`
	Key string `yaml:"key"`
}

// JWTFile is the `auth.jwt` section.
// RS256 has one source: PublicKey, PublicKeyFile, or JWKSURL.
// HMACSecret may be set beside that source so HS256 and RS256 both work.
type JWTFile struct {
	HMACSecret    string `yaml:"hmac_secret"`
	PublicKey     string `yaml:"public_key"`
	PublicKeyFile string `yaml:"public_key_file"`
	JWKSURL       string `yaml:"jwks_url"`
	Issuer        string `yaml:"issuer"`
	Audience      string `yaml:"audience"`
	ClientIDClaim string `yaml:"client_id_claim"`
}

// PolicyFile is the `policy` section.
type PolicyFile struct {
	Clients map[string]ClientFile `yaml:"clients"`
}

// ClientFile is the allowlist for one client id.
type ClientFile struct {
	Tools []string `yaml:"tools"`
}

// Load reads path. An empty path is an empty file, which is the pass-through.
// Unknown fields are an error.
func Load(path string) (File, error) {
	if path == "" {
		return File{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return File{}, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return decode(f)
}

func decode(r io.Reader) (File, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var file File
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("config: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return File{}, errors.New("config: file has more than one document")
	} else if !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("config: %w", err)
	}
	if err := file.Validate(); err != nil {
		return File{}, err
	}
	return file, nil
}

// Validate checks the relationship between auth and policy and the shape
// of each section. It does not read PublicKeyFile; the process does that
// when it builds the verifier, so a missing file fails startup with the
// path in the error.
func (f File) Validate() error {
	switch {
	case f.Auth == nil && f.Policy == nil:
		return nil
	case f.Auth == nil:
		return errors.New("config: policy requires auth")
	case f.Policy == nil:
		return errors.New("config: auth requires policy")
	}
	if err := f.Auth.validate(); err != nil {
		return err
	}
	return f.Policy.validate()
}

func (a AuthFile) validate() error {
	if len(a.APIKeys) == 0 && a.JWT == nil {
		return errors.New("config: auth requires an api key or jwt")
	}
	seen := make(map[string]struct{}, len(a.APIKeys))
	for _, key := range a.APIKeys {
		if strings.TrimSpace(key.ID) == "" || strings.TrimSpace(key.Key) == "" {
			return errors.New("config: api keys need an id and a key")
		}
		if _, ok := seen[key.Key]; ok {
			return fmt.Errorf("config: duplicate api key for client %s", key.ID)
		}
		seen[key.Key] = struct{}{}
	}
	if a.JWT != nil {
		return a.JWT.validate()
	}
	return nil
}

func (j JWTFile) validate() error {
	rsaSources := 0
	if strings.TrimSpace(j.PublicKey) != "" {
		rsaSources++
	}
	if strings.TrimSpace(j.PublicKeyFile) != "" {
		rsaSources++
	}
	if strings.TrimSpace(j.JWKSURL) != "" {
		rsaSources++
	}
	if rsaSources > 1 {
		return errors.New("config: jwt accepts one RS256 source: public_key, public_key_file, or jwks_url")
	}
	if strings.TrimSpace(j.HMACSecret) == "" && rsaSources == 0 {
		return errors.New("config: jwt requires hmac_secret, a public key, or jwks_url")
	}
	return nil
}

func (p PolicyFile) validate() error {
	for id, client := range p.Clients {
		if strings.TrimSpace(id) == "" {
			return errors.New("config: policy client id is empty")
		}
		seen := make(map[string]struct{}, len(client.Tools))
		for _, tool := range client.Tools {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("config: client %s has an empty tool name", id)
			}
			if _, ok := seen[tool]; ok {
				return fmt.Errorf("config: client %s lists tool %s more than once", id, tool)
			}
			seen[tool] = struct{}{}
		}
	}
	return nil
}

// Pick chooses a string. An explicit flag wins, then the environment,
// then the file, then fallback.
func Pick(flagValue string, flagSet bool, envValue, fileValue, fallback string) string {
	if flagSet {
		return flagValue
	}
	if envValue != "" {
		return envValue
	}
	if fileValue != "" {
		return fileValue
	}
	return fallback
}

// ClientIDClaim returns the JWT claim used as the client id.
func (j JWTFile) ClientIDClaimOrDefault() string {
	if strings.TrimSpace(j.ClientIDClaim) == "" {
		return "sub"
	}
	return j.ClientIDClaim
}
