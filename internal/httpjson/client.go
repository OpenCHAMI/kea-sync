package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrNotConfigured = errors.New("service base URL is not configured")

type AuthorizationProvider interface {
	Authorization(ctx context.Context) (string, error)
}

type Config struct {
	BaseURL      string
	Timeout      time.Duration
	Retries      int
	AuthProvider AuthorizationProvider
}

type Client struct {
	baseURL      string
	timeout      time.Duration
	retries      int
	authProvider AuthorizationProvider
	httpClient   *http.Client
}

type RequestOptions struct {
	Query   url.Values
	Body    any
	Headers map[string]string
}

type Response struct {
	StatusCode int
	Headers    http.Header
	Data       any
	RawBody    string
}

type APIError struct {
	Method       string
	URL          string
	StatusCode   int
	ResponseBody string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s failed with status %d", e.Method, e.URL, e.StatusCode)
}

func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	retries := cfg.Retries
	if retries < 0 {
		retries = 0
	}
	return &Client{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		timeout:      timeout,
		retries:      retries,
		authProvider: cfg.AuthProvider,
		httpClient:   &http.Client{Timeout: timeout},
	}
}

func (c *Client) Do(ctx context.Context, method, path string, opts RequestOptions) (Response, error) {
	if strings.TrimSpace(c.baseURL) == "" {
		return Response{}, ErrNotConfigured
	}
	target, err := c.buildURL(path, opts.Query)
	if err != nil {
		return Response{}, err
	}

	var bodyBytes []byte
	if opts.Body != nil {
		bodyBytes, err = json.Marshal(opts.Body)
		if err != nil {
			return Response{}, fmt.Errorf("marshal request body: %w", err)
		}
	}

	attempts := c.retries + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		resp, reqErr := c.doOnce(ctx, method, target, bodyBytes, opts.Headers)
		if reqErr == nil {
			return resp, nil
		}
		lastErr = reqErr

		var apiErr *APIError
		if errors.As(reqErr, &apiErr) && apiErr.StatusCode < http.StatusInternalServerError {
			return Response{}, reqErr
		}
		if attempt+1 < attempts {
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}

	return Response{}, lastErr
}

func (c *Client) doOnce(ctx context.Context, method, target string, body []byte, headers map[string]string) (Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authProvider != nil {
		authHeader, authErr := c.authProvider.Authorization(ctx)
		if authErr != nil {
			return Response{}, authErr
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, err
	}
	out := Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		RawBody:    string(raw),
		Data:       normalizeBody(resp.StatusCode, resp.Header.Get("Content-Type"), raw),
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return Response{}, &APIError{
			Method:       method,
			URL:          target,
			StatusCode:   resp.StatusCode,
			ResponseBody: out.RawBody,
		}
	}
	return out, nil
}

func (c *Client) buildURL(path string, query url.Values) (string, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		target, err := url.Parse(path)
		if err != nil {
			return "", err
		}
		if len(query) > 0 {
			target.RawQuery = query.Encode()
		}
		return target.String(), nil
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	target := base.ResolveReference(ref)
	if len(query) > 0 {
		target.RawQuery = query.Encode()
	}
	return target.String(), nil
}

func normalizeBody(status int, contentType string, raw []byte) any {
	if len(raw) == 0 {
		return map[string]any{"status": status}
	}
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			return decoded
		}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		return decoded
	}
	return map[string]any{
		"status": status,
		"text":   string(raw),
	}
}
