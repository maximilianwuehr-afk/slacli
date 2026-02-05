package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"slacli/internal/config"
)

// XoxcCredentials holds xoxc token and d cookie for session auth
type XoxcCredentials struct {
	Token     string `json:"token"`      // xoxc-* token
	Cookie    string `json:"cookie"`     // d cookie value (xoxd-*)
	Workspace string `json:"workspace"`  // Workspace subdomain (e.g., "finn")
	UpdatedAt string `json:"updated_at"` // When credentials were last updated
}

// LoadXoxcCredentials loads xoxc credentials from file
func LoadXoxcCredentials(cfg *config.Config) (*XoxcCredentials, error) {
	data, err := os.ReadFile(cfg.XoxcCredentialsPath())
	if err != nil {
		return nil, err
	}
	var creds XoxcCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// SaveXoxcCredentials saves xoxc credentials to file
func SaveXoxcCredentials(cfg *config.Config, creds *XoxcCredentials) error {
	creds.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.XoxcCredentialsPath(), data, 0600)
}

// HasXoxcCredentials checks if xoxc credentials are configured
func HasXoxcCredentials(cfg *config.Config) bool {
	creds, err := LoadXoxcCredentials(cfg)
	if err != nil {
		return false
	}
	return creds.Token != "" && creds.Cookie != ""
}

// GetXoxcClient returns an HTTP client configured for xoxc authentication
func GetXoxcClient(cfg *config.Config) (*http.Client, *XoxcCredentials, error) {
	creds, err := LoadXoxcCredentials(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("xoxc credentials not configured: %w", err)
	}

	if creds.Token == "" || creds.Cookie == "" {
		return nil, nil, fmt.Errorf("xoxc credentials incomplete: run 'slack drafts setup' first")
	}

	// Validate token format
	if !strings.HasPrefix(creds.Token, "xoxc-") {
		return nil, nil, fmt.Errorf("invalid xoxc token format: must start with 'xoxc-'")
	}

	// Create client with xoxc transport
	return &http.Client{
		Transport: &xoxcTransport{
			token:  creds.Token,
			cookie: creds.Cookie,
			base:   http.DefaultTransport,
		},
		Timeout: 30 * time.Second,
	}, creds, nil
}

// xoxcTransport adds xoxc auth headers to requests
type xoxcTransport struct {
	token  string
	cookie string
	base   http.RoundTripper
}

func (t *xoxcTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "Bearer "+t.token)
	
	// Add the d cookie
	cookieValue := t.cookie
	if !strings.HasPrefix(cookieValue, "d=") {
		cookieValue = "d=" + cookieValue
	}
	req2.Header.Set("Cookie", cookieValue)
	
	return t.base.RoundTrip(req2)
}
