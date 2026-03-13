package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

type JWTClaims struct {
	Audience []string `json:"aud"`
	Expires  int64    `json:"exp"`
	Scope    []string `json:"scope"`
}

type JWKSAuth struct {
	jwksURL         string
	refreshInterval time.Duration
	requiredScopes  []string
	client          *http.Client

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	lastRefresh time.Time
}

func NewJWKSAuth(jwksURL string, refreshInterval time.Duration, requiredScopes []string) (*JWKSAuth, error) {
	auth := &JWKSAuth{
		jwksURL:         jwksURL,
		refreshInterval: refreshInterval,
		requiredScopes:  requiredScopes,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		keys: make(map[string]*rsa.PublicKey),
	}
	if jwksURL != "" {
		if err := auth.refresh(context.Background()); err != nil {
			return nil, err
		}
	}
	return auth, nil
}

func (a *JWKSAuth) Wrap(next http.Handler) http.Handler {
	if strings.TrimSpace(a.jwksURL) == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if time.Since(a.lastRefresh) > a.refreshInterval {
			_ = a.refresh(r.Context())
		}

		header := r.Header.Get("Authorization")
		if header == "" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			http.Error(w, "invalid authorization header", http.StatusUnauthorized)
			return
		}
		claims, err := a.verify(parts[1])
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		for _, scope := range a.requiredScopes {
			if !contains(claims.Scope, scope) {
				http.Error(w, "insufficient scope", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *JWKSAuth) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("JWKS request failed with status %d", resp.StatusCode)
	}

	var payload struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	keys := make(map[string]*rsa.PublicKey, len(payload.Keys))
	for _, key := range payload.Keys {
		if key.Kty != "RSA" || key.Kid == "" || key.N == "" || key.E == "" {
			continue
		}
		pub, err := rsaPublicKey(key.N, key.E)
		if err != nil {
			continue
		}
		keys[key.Kid] = pub
	}

	a.mu.Lock()
	a.keys = keys
	a.lastRefresh = time.Now()
	a.mu.Unlock()
	return nil
}

func (a *JWKSAuth) verify(raw string) (JWTClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return JWTClaims{}, fmt.Errorf("invalid JWT")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return JWTClaims{}, err
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return JWTClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return JWTClaims{}, err
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return JWTClaims{}, err
	}
	if header.Alg != "RS256" {
		return JWTClaims{}, fmt.Errorf("unsupported JWT algorithm %q", header.Alg)
	}

	a.mu.RLock()
	key := a.keys[header.Kid]
	a.mu.RUnlock()
	if key == nil {
		return JWTClaims{}, fmt.Errorf("JWT key not found")
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], signature); err != nil {
		return JWTClaims{}, err
	}

	var claims JWTClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return JWTClaims{}, err
	}
	if claims.Expires > 0 && time.Now().Unix() >= claims.Expires {
		return JWTClaims{}, fmt.Errorf("token expired")
	}
	return claims, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func rsaPublicKey(modulus, exponent string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("invalid exponent")
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}
