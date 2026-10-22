package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const jwtLeeway = 30 * time.Second

// JWTOptions configures HS256, RS256, or both.
// RS256 has one key source: PublicKey or JWKSURL.
type JWTOptions struct {
	HMACSecret []byte
	PublicKey  *rsa.PublicKey
	JWKSURL    string
	// HTTPClient fetches JWKS. Nil uses a client with a 5s timeout.
	HTTPClient *http.Client
	Issuer     string
	Audience   string
	// ClientIDClaim is the claim used as the client id. Empty means "sub".
	ClientIDClaim string
}

type jwtVerifier struct {
	hmac    []byte
	static  *rsa.PublicKey
	jwks    *jwksCache
	issuer  string
	aud     string
	claim   string
	methods []string
	parser  *jwt.Parser
}

func newJWTVerifier(opts JWTOptions) (*jwtVerifier, error) {
	if len(opts.HMACSecret) == 0 && opts.PublicKey == nil && strings.TrimSpace(opts.JWKSURL) == "" {
		return nil, errors.New("auth: jwt requires a hmac secret, a public key, or a jwks url")
	}
	if opts.PublicKey != nil && strings.TrimSpace(opts.JWKSURL) != "" {
		return nil, errors.New("auth: jwt accepts either a public key or a jwks url")
	}
	claim := opts.ClientIDClaim
	if strings.TrimSpace(claim) == "" {
		claim = "sub"
	}
	v := &jwtVerifier{
		hmac:   append([]byte(nil), opts.HMACSecret...),
		static: opts.PublicKey,
		issuer: opts.Issuer,
		aud:    opts.Audience,
		claim:  claim,
	}
	if len(v.hmac) > 0 {
		v.methods = append(v.methods, jwt.SigningMethodHS256.Alg())
	}
	if v.static != nil || strings.TrimSpace(opts.JWKSURL) != "" {
		v.methods = append(v.methods, jwt.SigningMethodRS256.Alg())
	}
	if strings.TrimSpace(opts.JWKSURL) != "" {
		client := opts.HTTPClient
		if client == nil {
			client = &http.Client{Timeout: 5 * time.Second}
		}
		v.jwks = &jwksCache{url: opts.JWKSURL, client: client, ttl: 5 * time.Minute, keys: map[string]*rsa.PublicKey{}}
	}
	parserOpts := []jwt.ParserOption{
		jwt.WithValidMethods(v.methods),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(jwtLeeway),
	}
	if v.issuer != "" {
		parserOpts = append(parserOpts, jwt.WithIssuer(v.issuer))
	}
	if v.aud != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(v.aud))
	}
	v.parser = jwt.NewParser(parserOpts...)
	return v, nil
}

func (v *jwtVerifier) principal(ctx context.Context, token string) (Principal, error) {
	parsed, err := v.parser.Parse(token, func(t *jwt.Token) (any, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodHMAC:
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() || len(v.hmac) == 0 {
				return nil, errors.New("auth: unexpected hmac algorithm")
			}
			return v.hmac, nil
		case *jwt.SigningMethodRSA:
			if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
				return nil, errors.New("auth: unexpected rsa algorithm")
			}
			return v.rsaKey(ctx, kid(t))
		default:
			return nil, errors.New("auth: unsupported signing method")
		}
	})
	if err != nil || !parsed.Valid {
		return Principal{}, ErrUnauthenticated
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	id, ok := claims[v.claim].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{ID: id, Method: "jwt"}, nil
}

func kid(token *jwt.Token) string {
	raw, _ := token.Header["kid"].(string)
	return raw
}

func (v *jwtVerifier) rsaKey(ctx context.Context, id string) (*rsa.PublicKey, error) {
	if v.static != nil {
		return v.static, nil
	}
	if v.jwks == nil {
		return nil, errors.New("auth: no rsa key configured")
	}
	return v.jwks.key(ctx, id)
}

type jwksCache struct {
	url    string
	client *http.Client
	ttl    time.Duration

	mu      sync.Mutex
	fetched time.Time
	keys    map[string]*rsa.PublicKey
}

func (c *jwksCache) key(ctx context.Context, id string) (*rsa.PublicKey, error) {
	if key, ok := c.lookup(id, false); ok {
		return key, nil
	}
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}
	key, ok := c.lookup(id, true)
	if !ok {
		return nil, errors.New("auth: jwt key id not found")
	}
	return key, nil
}

func (c *jwksCache) lookup(id string, ignoreTTL bool) (*rsa.PublicKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !ignoreTTL && (c.fetched.IsZero() || time.Since(c.fetched) > c.ttl) {
		return nil, false
	}
	if id == "" && len(c.keys) == 1 {
		for _, key := range c.keys {
			return key, true
		}
	}
	key, ok := c.keys[id]
	return key, ok
}

func (c *jwksCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: jwks status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.keys = keys
	c.fetched = time.Now()
	c.mu.Unlock()
	return nil
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("auth: parse jwks: %w", err)
	}
	if len(doc.Keys) == 0 {
		return nil, errors.New("auth: jwks has no keys")
	}
	out := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, key := range doc.Keys {
		if key.Kty != "" && key.Kty != "RSA" {
			continue
		}
		if key.Alg != "" && key.Alg != "RS256" {
			continue
		}
		pub, err := rsaFromModulus(key.N, key.E)
		if err != nil {
			return nil, err
		}
		out[key.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("auth: jwks has no RS256 keys")
	}
	return out, nil
}

func rsaFromModulus(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := decodeB64URL(nB64)
	if err != nil {
		return nil, fmt.Errorf("auth: jwks modulus: %w", err)
	}
	eBytes, err := decodeB64URL(eB64)
	if err != nil {
		return nil, fmt.Errorf("auth: jwks exponent: %w", err)
	}
	if len(nBytes) < 128 || len(eBytes) == 0 || len(eBytes) > 4 {
		return nil, errors.New("auth: jwks rsa key is unusable")
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e < 3 {
		return nil, errors.New("auth: jwks exponent is unusable")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func decodeB64URL(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// ParseRSAPublicKey parses a PEM PKIX or PKCS1 RSA public key.
func ParseRSAPublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("auth: public key is not PEM")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		key, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("auth: public key is not RSA")
		}
		return key, nil
	}
	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("auth: parse public key: %w", err)
	}
	return key, nil
}
