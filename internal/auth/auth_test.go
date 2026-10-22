package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAPIKeys(t *testing.T) {
	t.Parallel()

	creds := mustCreds(t, []Key{{ID: "demo", Secret: "dev-demo-key"}, {ID: "admin", Secret: "dev-admin-key"}}, nil)
	jwtCreds := mustCreds(t, []Key{{ID: "demo", Secret: "a.b.c"}}, &JWTOptions{HMACSecret: []byte("hmac-secret-at-least-long")})

	cases := []struct {
		name    string
		creds   *Credentials
		header  http.Header
		wantID  string
		wantErr bool
	}{
		{name: "bearer", creds: creds, header: bearerHeader("dev-demo-key"), wantID: "demo"},
		{name: "x-api-key", creds: creds, header: header("X-Api-Key", "dev-admin-key"), wantID: "admin"},
		{name: "bearer wins over x-api-key", creds: creds, header: both("dev-demo-key", "dev-admin-key"), wantID: "demo"},
		{name: "unknown", creds: creds, header: bearerHeader("nope"), wantErr: true},
		{name: "missing", creds: creds, header: http.Header{}, wantErr: true},
		{name: "basic", creds: creds, header: header("Authorization", "Basic abc"), wantErr: true},
		{name: "empty bearer", creds: creds, header: header("Authorization", "Bearer"), wantErr: true},
		{name: "dotted key via x-api-key", creds: jwtCreds, header: header("X-Api-Key", "a.b.c"), wantID: "demo"},
		{name: "dotted bearer is not a key when jwt is configured", creds: jwtCreds, header: bearerHeader("a.b.c"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.creds.Authenticate(context.Background(), tc.header)
			if tc.wantErr {
				if err == nil {
					t.Fatal("error = nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tc.wantID || got.Method != "api_key" {
				t.Fatalf("principal = %+v", got)
			}
		})
	}
}

func TestJWT(t *testing.T) {
	t.Parallel()

	secret := []byte("hmac-secret-at-least-long")
	priv := mustRSA(t)
	jwks := jwksServer(t, priv.PublicKey, "test")
	opts := JWTOptions{
		HMACSecret: secret,
		Issuer:     "mcp-gateway",
		Audience:   "mcp-gateway",
	}
	rsaOpts := opts
	rsaOpts.HMACSecret = nil
	rsaOpts.PublicKey = &priv.PublicKey
	jwksOpts := opts
	jwksOpts.HMACSecret = nil
	jwksOpts.JWKSURL = jwks.URL

	hmacCreds := mustCreds(t, nil, &opts)
	rsaCreds := mustCreds(t, nil, &rsaOpts)
	jwksCreds := mustCreds(t, nil, &jwksOpts)
	claimCreds := mustCreds(t, nil, &JWTOptions{HMACSecret: secret, ClientIDClaim: "client_id"})

	now := time.Now()
	cases := []struct {
		name    string
		creds   *Credentials
		token   string
		wantID  string
		wantErr bool
	}{
		{name: "hs256", creds: hmacCreds, token: signHS(t, secret, claims("demo", "mcp-gateway", "mcp-gateway", now.Add(time.Hour))), wantID: "demo"},
		{name: "expired", creds: hmacCreds, token: signHS(t, secret, claims("demo", "mcp-gateway", "mcp-gateway", now.Add(-time.Hour))), wantErr: true},
		{name: "no exp", creds: hmacCreds, token: signHS(t, secret, jwt.MapClaims{"sub": "demo", "iss": "mcp-gateway", "aud": "mcp-gateway"}), wantErr: true},
		{name: "wrong iss", creds: hmacCreds, token: signHS(t, secret, claims("demo", "other", "mcp-gateway", now.Add(time.Hour))), wantErr: true},
		{name: "wrong aud", creds: hmacCreds, token: signHS(t, secret, claims("demo", "mcp-gateway", "other", now.Add(time.Hour))), wantErr: true},
		{name: "wrong secret", creds: hmacCreds, token: signHS(t, []byte("other-secret-at-least-long"), claims("demo", "mcp-gateway", "mcp-gateway", now.Add(time.Hour))), wantErr: true},
		{name: "none alg", creds: hmacCreds, token: signNone(t, claims("demo", "mcp-gateway", "mcp-gateway", now.Add(time.Hour))), wantErr: true},
		{name: "rs256 static", creds: rsaCreds, token: signRS(t, priv, "", claims("demo", "mcp-gateway", "mcp-gateway", now.Add(time.Hour))), wantID: "demo"},
		{name: "rs256 wrong key", creds: rsaCreds, token: signRS(t, mustRSA(t), "", claims("demo", "mcp-gateway", "mcp-gateway", now.Add(time.Hour))), wantErr: true},
		{name: "rs256 jwks", creds: jwksCreds, token: signRS(t, priv, "test", claims("demo", "mcp-gateway", "mcp-gateway", now.Add(time.Hour))), wantID: "demo"},
		{name: "rs256 unknown kid", creds: jwksCreds, token: signRS(t, priv, "other", claims("demo", "mcp-gateway", "mcp-gateway", now.Add(time.Hour))), wantErr: true},
		{name: "claim", creds: claimCreds, token: signHS(t, secret, jwt.MapClaims{"client_id": "demo", "exp": now.Add(time.Hour).Unix()}), wantID: "demo"},
		{name: "empty claim", creds: claimCreds, token: signHS(t, secret, jwt.MapClaims{"client_id": "", "exp": now.Add(time.Hour).Unix()}), wantErr: true},
		{name: "hs256 rejected when only rsa", creds: rsaCreds, token: signHS(t, secret, claims("demo", "mcp-gateway", "mcp-gateway", now.Add(time.Hour))), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.creds.Authenticate(context.Background(), bearerHeader(tc.token))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("error = nil, principal %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tc.wantID || got.Method != "jwt" {
				t.Fatalf("principal = %+v", got)
			}
		})
	}
}

func TestNewCredentialsRejectsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := NewCredentials(nil, nil); err == nil {
		t.Fatal("error = nil")
	}
}

func mustCreds(t *testing.T, keys []Key, opts *JWTOptions) *Credentials {
	t.Helper()
	creds, err := NewCredentials(keys, opts)
	if err != nil {
		t.Fatal(err)
	}
	return creds
}

func bearerHeader(token string) http.Header {
	return header("Authorization", "Bearer "+token)
}

func header(key, value string) http.Header {
	h := make(http.Header)
	h.Set(key, value)
	return h
}

func both(bearer, apiKey string) http.Header {
	h := bearerHeader(bearer)
	h.Set("X-Api-Key", apiKey)
	return h
}

func claims(sub, iss, aud string, exp time.Time) jwt.MapClaims {
	return jwt.MapClaims{"sub": sub, "iss": iss, "aud": aud, "exp": exp.Unix()}
}

func signHS(t *testing.T, secret []byte, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func signRS(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func signNone(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func mustRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func jwksServer(t *testing.T, pub rsa.PublicKey, kid string) *httptest.Server {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	body, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": kid,
			"alg": "RS256",
			"n":   n,
			"e":   e,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Keep PEM parsing covered without a fixture file.
func TestParseRSAPublicKey(t *testing.T) {
	t.Parallel()
	key := mustRSA(t)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	got, err := ParseRSAPublicKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got.N.Cmp(key.N) != 0 || got.E != key.E {
		t.Fatal("parsed key does not match")
	}
	if _, err := ParseRSAPublicKey([]byte("nope")); err == nil {
		t.Fatal("error = nil")
	}
}
