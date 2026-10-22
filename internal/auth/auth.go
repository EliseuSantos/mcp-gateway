// Package auth maps an HTTP request to a client id.
//
// API keys and JWTs are the two verifiers. [Middleware] is the proxy HTTP
// middleware: it runs before the JSON-RPC body is parsed and stores the
// client id with [proxy.WithPrincipal]. A request that does not verify is
// HTTP 401 and does not reach the upstream.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

// ErrUnauthenticated is returned when the request has no usable credential.
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// CodeUnauthenticated is the JSON-RPC code written in the 401 body.
// The HTTP status is what clients branch on. The body is for a person
// reading the response.
const CodeUnauthenticated int64 = -32000

// Principal is the caller the rest of the gateway sees.
type Principal struct {
	// ID is the client id used by the allowlist and the audit log.
	ID string
	// Method is "api_key" or "jwt".
	Method string
}

// Authenticator checks request headers.
type Authenticator interface {
	Authenticate(ctx context.Context, headers http.Header) (Principal, error)
}

// Key is one configured API key.
type Key struct {
	ID     string
	Secret string
}

type hashedKey struct {
	id  string
	sum [sha256.Size]byte
}

// Credentials verifies API keys and, when configured, JWTs.
type Credentials struct {
	keys []hashedKey
	jwt  *jwtVerifier
}

// NewCredentials builds a verifier. keys and jwt may each be empty, but not
// both: a verifier that accepts nothing is a configuration error.
func NewCredentials(keys []Key, jwtOpts *JWTOptions) (*Credentials, error) {
	c := &Credentials{}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key.ID) == "" || key.Secret == "" {
			return nil, errors.New("auth: api keys need an id and a secret")
		}
		if _, ok := seen[key.Secret]; ok {
			return nil, errors.New("auth: duplicate api key")
		}
		seen[key.Secret] = struct{}{}
		c.keys = append(c.keys, hashedKey{id: key.ID, sum: sha256.Sum256([]byte(key.Secret))})
	}
	if jwtOpts != nil {
		verifier, err := newJWTVerifier(*jwtOpts)
		if err != nil {
			return nil, err
		}
		c.jwt = verifier
	}
	if len(c.keys) == 0 && c.jwt == nil {
		return nil, errors.New("auth: no api keys or jwt configured")
	}
	return c, nil
}

// Authenticate reports the client id for headers.
//
// Authorization is checked first when it is present. Bearer is an API key
// unless the token contains two dots and a JWT verifier is configured, in
// which case only JWT verification runs. A failed JWT is not compared to
// the API keys. X-Api-Key is used when Authorization is absent, and it is
// always an API key.
func (c *Credentials) Authenticate(ctx context.Context, headers http.Header) (Principal, error) {
	if c == nil {
		return Principal{}, ErrUnauthenticated
	}
	if token, ok := bearer(headers); ok {
		if c.jwt != nil && looksLikeJWT(token) {
			return c.jwt.principal(ctx, token)
		}
		return c.principalFromKey(token)
	}
	if headers != nil {
		if key := strings.TrimSpace(headers.Get("X-Api-Key")); key != "" {
			return c.principalFromKey(key)
		}
	}
	return Principal{}, ErrUnauthenticated
}

func (c *Credentials) principalFromKey(token string) (Principal, error) {
	sum := sha256.Sum256([]byte(token))
	var id string
	matched := 0
	for _, key := range c.keys {
		eq := subtle.ConstantTimeCompare(sum[:], key.sum[:])
		if eq == 1 {
			id = key.id
			matched = 1
		}
	}
	if matched != 1 {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{ID: id, Method: "api_key"}, nil
}

func bearer(headers http.Header) (string, bool) {
	if headers == nil {
		return "", false
	}
	value := strings.TrimSpace(headers.Get("Authorization"))
	if value == "" {
		return "", false
	}
	scheme, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	return token, true
}

func looksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}
