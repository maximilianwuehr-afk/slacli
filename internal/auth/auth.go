package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"

	"slacli/internal/config"
	"slacli/internal/output"
)

const (
	// Slack OAuth endpoints
	authURL  = "https://slack.com/oauth/v2/authorize"
	tokenURL = "https://slack.com/api/oauth.v2.access"

	// Required scopes for user token
	// Note: These are user scopes, not bot scopes
	userScopes = "channels:history,channels:read,groups:history,groups:read," +
		"im:history,im:read,mpim:history,mpim:read," +
		"users:read,users:read.email,search:read," +
		"chat:write,files:read,files:write"
)

// Credentials holds OAuth tokens
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	TeamID       string `json:"team_id"`
	TeamName     string `json:"team_name"`
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name,omitempty"`
}

// IsExpired returns true if the token is expired
func (c *Credentials) IsExpired() bool {
	if c.ExpiresAt == "" {
		return false // No expiry set
	}
	exp, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		return true
	}
	return time.Now().After(exp)
}

// Login performs OAuth flow
func Login(cfg *config.Config) error {
	// Generate state for CSRF protection
	state, err := generateState()
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}

	// Start local HTTPS server to receive callback (fixed port for Slack OAuth config)
	const callbackPort = 49251

	// Generate self-signed cert for localhost
	tlsCert, err := generateSelfSignedCert()
	if err != nil {
		return fmt.Errorf("generate TLS cert: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	tcpListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", callbackPort))
	if err != nil {
		return fmt.Errorf("start callback server on port %d: %w", callbackPort, err)
	}
	listener := tls.NewListener(tcpListener, tlsConfig)
	defer listener.Close()

	redirectURI := fmt.Sprintf("https://127.0.0.1:%d/callback", callbackPort)

	// Build authorization URL
	// Note: For personal use, you'll need to create a Slack app and get client credentials
	clientID := os.Getenv("SLACLI_CLIENT_ID")
	clientSecret := os.Getenv("SLACLI_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("SLACLI_CLIENT_ID and SLACLI_CLIENT_SECRET environment variables required.\n" +
			"Create a Slack app at https://api.slack.com/apps and set these variables.")
	}

	authParams := url.Values{
		"client_id":    {clientID},
		"scope":        {""}, // Empty for user token flow
		"user_scope":   {userScopes},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}

	authURLFull := authURL + "?" + authParams.Encode()

	// Open browser
	fmt.Fprintf(os.Stderr, "Opening browser for Slack authentication...\n")
	if err := openBrowser(authURLFull); err != nil {
		fmt.Fprintf(os.Stderr, "Please open this URL in your browser:\n%s\n", authURLFull)
	}

	// Wait for callback
	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}

			// Verify state
			if r.URL.Query().Get("state") != state {
				errChan <- fmt.Errorf("invalid state parameter")
				http.Error(w, "Invalid state", http.StatusBadRequest)
				return
			}

			// Check for error
			if errMsg := r.URL.Query().Get("error"); errMsg != "" {
				errChan <- fmt.Errorf("OAuth error: %s", errMsg)
				http.Error(w, errMsg, http.StatusBadRequest)
				return
			}

			code := r.URL.Query().Get("code")
			if code == "" {
				errChan <- fmt.Errorf("no code in callback")
				http.Error(w, "No code", http.StatusBadRequest)
				return
			}

			// Success response
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>slacli</title></head>
<body style="font-family: system-ui; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0;">
<div style="text-align: center;">
<h1>✓ Authentication successful!</h1>
<p>You can close this window and return to your terminal.</p>
</div>
</body>
</html>`)

			codeChan <- code
		}),
	}

	go server.Serve(listener)

	// Wait for code or error with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var code string
	select {
	case code = <-codeChan:
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return fmt.Errorf("authentication timed out")
	}

	// Exchange code for token
	tokenParams := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}

	resp, err := http.PostForm(tokenURL, tokenParams)
	if err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error,omitempty"`
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Team        struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
		AuthedUser struct {
			ID          string `json:"id"`
			AccessToken string `json:"access_token"`
		} `json:"authed_user"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("parse token response: %w", err)
	}

	if !tokenResp.OK {
		return fmt.Errorf("token error: %s", tokenResp.Error)
	}

	// For user token flow, the token is in authed_user
	accessToken := tokenResp.AuthedUser.AccessToken
	if accessToken == "" {
		accessToken = tokenResp.AccessToken
	}

	// Save credentials
	creds := &Credentials{
		AccessToken: accessToken,
		TokenType:   tokenResp.TokenType,
		TeamID:      tokenResp.Team.ID,
		TeamName:    tokenResp.Team.Name,
		UserID:      tokenResp.AuthedUser.ID,
	}

	return SaveCredentials(cfg, creds)
}

// Logout removes stored credentials
func Logout(cfg *config.Config) error {
	return os.Remove(cfg.CredentialsPath())
}

// Status returns current auth status
func Status(cfg *config.Config) (output.AuthStatus, error) {
	creds, err := LoadCredentials(cfg)
	if err != nil {
		return output.AuthStatus{Status: "not_authenticated"}, err
	}

	status := output.AuthStatus{
		TeamID:    creds.TeamID,
		TeamName:  creds.TeamName,
		UserID:    creds.UserID,
		UserName:  creds.UserName,
		ExpiresAt: creds.ExpiresAt,
		Status:    "valid",
	}

	if creds.IsExpired() {
		status.Status = "expired"
	}

	return status, nil
}

// Refresh refreshes the access token
func Refresh(cfg *config.Config) error {
	creds, err := LoadCredentials(cfg)
	if err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}

	if creds.RefreshToken == "" {
		return fmt.Errorf("no refresh token available - run 'slacli auth' to re-authenticate")
	}

	clientID := os.Getenv("SLACLI_CLIENT_ID")
	clientSecret := os.Getenv("SLACLI_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("SLACLI_CLIENT_ID and SLACLI_CLIENT_SECRET required")
	}

	oauthConfig := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: tokenURL,
		},
	}

	token := &oauth2.Token{
		RefreshToken: creds.RefreshToken,
	}

	newToken, err := oauthConfig.TokenSource(context.Background(), token).Token()
	if err != nil {
		return fmt.Errorf("refresh token: %w", err)
	}

	creds.AccessToken = newToken.AccessToken
	if newToken.RefreshToken != "" {
		creds.RefreshToken = newToken.RefreshToken
	}
	if !newToken.Expiry.IsZero() {
		creds.ExpiresAt = newToken.Expiry.Format(time.RFC3339)
	}

	return SaveCredentials(cfg, creds)
}

// GetClient returns an authenticated HTTP client
func GetClient(cfg *config.Config) (*http.Client, error) {
	creds, err := LoadCredentials(cfg)
	if err != nil {
		return nil, fmt.Errorf("not authenticated: %w", err)
	}

	if creds.IsExpired() {
		// Try to refresh
		if err := Refresh(cfg); err != nil {
			return nil, fmt.Errorf("token expired and refresh failed: %w", err)
		}
		// Reload credentials
		creds, err = LoadCredentials(cfg)
		if err != nil {
			return nil, err
		}
	}

	// Create client with auth header
	return &http.Client{
		Transport: &authTransport{
			token: creds.AccessToken,
			base:  http.DefaultTransport,
		},
		Timeout: 30 * time.Second,
	}, nil
}

// LoadCredentials loads credentials from file
func LoadCredentials(cfg *config.Config) (*Credentials, error) {
	data, err := os.ReadFile(cfg.CredentialsPath())
	if err != nil {
		return nil, err
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// SaveCredentials saves credentials to file
func SaveCredentials(cfg *config.Config, creds *Credentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.CredentialsPath(), data, 0600)
}

// authTransport adds auth header to requests
type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req2)
}

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform")
	}
	return cmd.Start()
}

// generateSelfSignedCert creates a self-signed TLS certificate for localhost
func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"slacli"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}, nil
}
