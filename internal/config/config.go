package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var trueValues = map[string]struct{}{
	"1":    {},
	"true": {},
	"yes":  {},
	"on":   {},
}

type HTTPService struct {
	BaseURL       string
	Timeout       time.Duration
	Retries       int
	BearerToken   string
	TargetService string
	Scopes        []string
}

type KeaService struct {
	HTTPService
	ControlAgentServices []string
	OperationTarget      string
}

type TokensmithConfig struct {
	BaseURL       string
	ServiceAPIKey string
	RefreshSkew   time.Duration
	Timeout       time.Duration
}

func (c TokensmithConfig) Enabled() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.ServiceAPIKey) != ""
}

type AdminAuth struct {
	JWKSURL             string
	JWKSRefreshInterval time.Duration
	ReadScopes          []string
	WriteScopes         []string
}

func (a AdminAuth) Enabled() bool {
	return strings.TrimSpace(a.JWKSURL) != ""
}

type Hostnames struct {
	NodePattern    string
	BMCPattern     string
	Domain         string
	DefaultPattern string
	ByType         map[string]string
}

type Config struct {
	LogLevel            string
	ListenAddr          string
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPShutdownTimeout time.Duration
	SyncTimeout         time.Duration
	EnablePeriodicSync  bool
	SyncInterval        time.Duration
	PageLimit           int
	ManagedBy           string
	ManagedSource       string

	SMD        HTTPService
	Kea        KeaService
	Tokensmith TokensmithConfig
	Auth       AdminAuth
	Hostnames  Hostnames
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		LogLevel:            envString("KEA_SYNC_LOG_LEVEL", "info"),
		ListenAddr:          envString("KEA_SYNC_LISTEN_ADDR", ":8080"),
		HTTPReadTimeout:     envDuration("KEA_SYNC_HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout:    envDuration("KEA_SYNC_HTTP_WRITE_TIMEOUT", 30*time.Second),
		HTTPShutdownTimeout: envDuration("KEA_SYNC_HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
		SyncTimeout:         envDuration("KEA_SYNC_SYNC_TIMEOUT", 30*time.Second),
		EnablePeriodicSync:  envBool("KEA_SYNC_ENABLE_PERIODIC_SYNC", true),
		SyncInterval:        envDuration("KEA_SYNC_SYNC_INTERVAL", 30*time.Second),
		PageLimit:           envInt("KEA_SYNC_PAGE_LIMIT", 100),
		ManagedBy:           envString("KEA_SYNC_MANAGED_BY", "kea-sync"),
		ManagedSource:       envString("KEA_SYNC_MANAGED_SOURCE", "smd"),
		SMD:                 loadHTTPService("KEA_SYNC_SMD", "smd"),
		Kea: KeaService{
			HTTPService:          loadHTTPService("KEA_SYNC_KEA", "kea"),
			ControlAgentServices: envCSV("KEA_SYNC_KEA_SERVICES", []string{"dhcp4"}),
			OperationTarget:      envString("KEA_SYNC_KEA_OPERATION_TARGET", ""),
		},
		Tokensmith: TokensmithConfig{
			BaseURL:       envString("KEA_SYNC_TOKENSMITH_URL", ""),
			ServiceAPIKey: envString("KEA_SYNC_TOKENSMITH_SERVICE_API_KEY", ""),
			RefreshSkew:   envDuration("KEA_SYNC_TOKENSMITH_REFRESH_SKEW", time.Minute),
			Timeout:       envDuration("KEA_SYNC_TOKENSMITH_TIMEOUT", 10*time.Second),
		},
		Auth: AdminAuth{
			JWKSURL:             envString("KEA_SYNC_AUTH_JWKS_URL", ""),
			JWKSRefreshInterval: envDuration("KEA_SYNC_AUTH_JWKS_REFRESH_INTERVAL", time.Hour),
			ReadScopes:          envCSV("KEA_SYNC_AUTH_READ_SCOPES", []string{"kea-sync.read"}),
			WriteScopes:         envCSV("KEA_SYNC_AUTH_WRITE_SCOPES", []string{"kea-sync.write"}),
		},
		Hostnames: Hostnames{
			NodePattern:    envString("KEA_SYNC_NODE_PATTERN", "nid{04d}"),
			BMCPattern:     envString("KEA_SYNC_BMC_PATTERN", "bmc{04d}"),
			Domain:         envString("KEA_SYNC_DOMAIN", ""),
			DefaultPattern: envString("KEA_SYNC_HOSTNAME_DEFAULT", ""),
			ByType:         envMap("KEA_SYNC_HOSTNAME_BY_TYPE"),
		},
	}

	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("KEA_SYNC_LISTEN_ADDR must not be empty")
	}
	if c.SyncTimeout <= 0 {
		return fmt.Errorf("KEA_SYNC_SYNC_TIMEOUT must be positive")
	}
	if c.SyncInterval <= 0 {
		return fmt.Errorf("KEA_SYNC_SYNC_INTERVAL must be positive")
	}
	if c.PageLimit <= 0 {
		return fmt.Errorf("KEA_SYNC_PAGE_LIMIT must be positive")
	}
	if c.ManagedBy == "" {
		return fmt.Errorf("KEA_SYNC_MANAGED_BY must not be empty")
	}
	if c.ManagedSource == "" {
		return fmt.Errorf("KEA_SYNC_MANAGED_SOURCE must not be empty")
	}
	for _, svc := range []struct {
		name string
		cfg  HTTPService
	}{
		{name: "smd", cfg: c.SMD},
		{name: "kea", cfg: c.Kea.HTTPService},
	} {
		if strings.TrimSpace(svc.cfg.BaseURL) == "" {
			return fmt.Errorf("%s base URL must not be empty", svc.name)
		}
		if svc.cfg.Timeout <= 0 {
			return fmt.Errorf("%s timeout must be positive", svc.name)
		}
		if svc.cfg.Retries < 0 {
			return fmt.Errorf("%s retries cannot be negative", svc.name)
		}
	}
	if c.Tokensmith.Enabled() && c.Tokensmith.Timeout <= 0 {
		return fmt.Errorf("KEA_SYNC_TOKENSMITH_TIMEOUT must be positive")
	}
	if c.Auth.Enabled() && c.Auth.JWKSRefreshInterval <= 0 {
		return fmt.Errorf("KEA_SYNC_AUTH_JWKS_REFRESH_INTERVAL must be positive")
	}
	return nil
}

func loadHTTPService(prefix, defaultTarget string) HTTPService {
	return HTTPService{
		BaseURL:       envString(prefix+"_URL", ""),
		Timeout:       envDuration(prefix+"_TIMEOUT", 10*time.Second),
		Retries:       envInt(prefix+"_RETRIES", 1),
		BearerToken:   envString(prefix+"_BEARER_TOKEN", ""),
		TargetService: envString(prefix+"_TARGET_SERVICE", defaultTarget),
		Scopes:        envCSV(prefix+"_SCOPES", nil),
	}
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	_, ok := trueValues[raw]
	return ok
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envCSV(key string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return append([]string(nil), fallback...)
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envMap(key string) map[string]string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		pattern := strings.TrimSpace(parts[1])
		if name == "" {
			continue
		}
		out[name] = pattern
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
