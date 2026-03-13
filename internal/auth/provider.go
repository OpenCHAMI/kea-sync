package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenCHAMI/kea-sync/internal/config"
)

type Provider interface {
	Authorization(ctx context.Context) (string, error)
}

type StaticBearerToken string

func (p StaticBearerToken) Authorization(_ context.Context) (string, error) {
	if strings.TrimSpace(string(p)) == "" {
		return "", nil
	}
	return "Bearer " + string(p), nil
}

type TokensmithProvider struct {
	baseURL       string
	serviceAPIKey string
	targetService string
	scopes        []string
	refreshSkew   time.Duration
	httpClient    *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func NewAuthorizationProvider(tokensmith config.TokensmithConfig, service config.HTTPService) Provider {
	if strings.TrimSpace(service.BearerToken) != "" {
		return StaticBearerToken(service.BearerToken)
	}
	if tokensmith.Enabled() {
		return &TokensmithProvider{
			baseURL:       strings.TrimRight(tokensmith.BaseURL, "/"),
			serviceAPIKey: tokensmith.ServiceAPIKey,
			targetService: service.TargetService,
			scopes:        append([]string(nil), service.Scopes...),
			refreshSkew:   tokensmith.RefreshSkew,
			httpClient: &http.Client{
				Timeout: tokensmith.Timeout,
			},
		}
	}
	return StaticBearerToken("")
}

func (p *TokensmithProvider) Authorization(ctx context.Context) (string, error) {
	token, err := p.tokenValue(ctx)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", nil
	}
	return "Bearer " + token, nil
}

func (p *TokensmithProvider) tokenValue(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	refreshSkew := p.refreshSkew
	if refreshSkew <= 0 {
		refreshSkew = time.Minute
	}
	if p.token != "" && time.Until(p.expiresAt) > refreshSkew {
		return p.token, nil
	}

	payload := map[string]any{
		"target_service": p.targetService,
		"scopes":         p.scopes,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal tokensmith request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/service/token", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create tokensmith request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Service-API-Key", p.serviceAPIKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("tokensmith request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read tokensmith response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("tokensmith returned status %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode tokensmith response: %w", err)
	}
	if parsed.AccessToken == "" {
		return "", fmt.Errorf("tokensmith response missing access_token")
	}

	expiresAt, err := parseJWTExpiry(parsed.AccessToken)
	if err != nil {
		expiresAt = time.Now().Add(5 * time.Minute)
	}
	p.token = parsed.AccessToken
	p.expiresAt = expiresAt
	return p.token, nil
}

func parseJWTExpiry(raw string) (time.Time, error) {
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid JWT structure")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, err
	}
	exp, ok := claims["exp"]
	if !ok {
		return time.Time{}, fmt.Errorf("exp claim missing")
	}
	switch typed := exp.(type) {
	case float64:
		return time.Unix(int64(typed), 0), nil
	case string:
		value, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return time.Time{}, err
		}
		return time.Unix(value, 0), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported exp type %T", exp)
	}
}
