package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

const (
	mcpOAuthCallbackPath   = "/mcp/oauth/callback"
	mcpOAuthDefaultTimeout = 2 * time.Minute
	mcpOAuthTokenSkew      = 30 * time.Second
)

// MCPOAuthPrompter presents an MCP OAuth authorization URL to a user.
// CLI callers can use the default browser-opening implementation; UI callers
// can install a custom prompter that opens a browser popup or returns the URL to
// the client.
type MCPOAuthPrompter interface {
	PromptMCPOAuth(ctx context.Context, serverName, authURL string) error
}

type mcpOAuthPrompterContextKey struct{}

// WithMCPOAuthPrompter returns a context that uses prompter for MCP OAuth
// authorization prompts triggered while using that context.
func WithMCPOAuthPrompter(ctx context.Context, prompter MCPOAuthPrompter) context.Context {
	if prompter == nil {
		return ctx
	}
	return context.WithValue(ctx, mcpOAuthPrompterContextKey{}, prompter)
}

type defaultMCPOAuthPrompter struct{}

func (defaultMCPOAuthPrompter) PromptMCPOAuth(_ context.Context, serverName, authURL string) error {
	name := strings.TrimSpace(serverName)
	if name == "" {
		name = "MCP server"
	} else {
		name = fmt.Sprintf("MCP server %q", name)
	}

	openBrowser := true
	if viper.IsSet("mcp.oauth.open_browser") {
		openBrowser = viper.GetBool("mcp.oauth.open_browser")
	}

	if openBrowser {
		fmt.Fprintf(os.Stderr, "%s requires OAuth authorization. Opening your browser...\n", name)
		if err := osutil.OpenBrowser(authURL); err != nil {
			fmt.Fprintf(os.Stderr, "Could not open browser automatically: %v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "%s requires OAuth authorization.\n", name)
	}

	fmt.Fprintf(os.Stderr, "If your browser did not open, visit this URL:\n\n  %s\n\n", authURL)
	return nil
}

var (
	mcpOAuthPrompterMu    sync.RWMutex
	mcpOAuthPromptHandler MCPOAuthPrompter = defaultMCPOAuthPrompter{}
)

// SetDefaultMCPOAuthPrompter installs a process-wide MCP OAuth prompter and
// returns a restore function. Passing nil restores the default CLI prompter.
func SetDefaultMCPOAuthPrompter(prompter MCPOAuthPrompter) func() {
	mcpOAuthPrompterMu.Lock()
	previous := mcpOAuthPromptHandler
	if prompter == nil {
		mcpOAuthPromptHandler = defaultMCPOAuthPrompter{}
	} else {
		mcpOAuthPromptHandler = prompter
	}
	mcpOAuthPrompterMu.Unlock()

	return func() {
		mcpOAuthPrompterMu.Lock()
		mcpOAuthPromptHandler = previous
		mcpOAuthPrompterMu.Unlock()
	}
}

func currentMCPOAuthPrompter(ctx context.Context) MCPOAuthPrompter {
	if ctx != nil {
		if prompter, ok := ctx.Value(mcpOAuthPrompterContextKey{}).(MCPOAuthPrompter); ok && prompter != nil {
			return prompter
		}
	}
	mcpOAuthPrompterMu.RLock()
	defer mcpOAuthPrompterMu.RUnlock()
	return mcpOAuthPromptHandler
}

func hasCustomMCPOAuthPrompter(ctx context.Context) bool {
	if ctx != nil {
		if prompter, ok := ctx.Value(mcpOAuthPrompterContextKey{}).(MCPOAuthPrompter); ok && prompter != nil {
			return true
		}
	}
	mcpOAuthPrompterMu.RLock()
	defer mcpOAuthPrompterMu.RUnlock()
	_, isDefault := mcpOAuthPromptHandler.(defaultMCPOAuthPrompter)
	return !isDefault
}

type mcpOAuthRoundTripper struct {
	base       http.RoundTripper
	serverName string
	serverURL  string
	config     MCPOAuthConfig
	store      *mcpOAuthCredentialStore

	mu sync.Mutex
}

func newMCPOAuthRoundTripper(serverName, serverURL string, config MCPOAuthConfig) (*mcpOAuthRoundTripper, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.Errorf("MCP OAuth requires an http or https server URL, got %q", serverURL)
	}

	store, err := newMCPOAuthCredentialStore(serverName, serverURL)
	if err != nil {
		return nil, err
	}

	return &mcpOAuthRoundTripper{
		base:       http.DefaultTransport,
		serverName: serverName,
		serverURL:  serverURL,
		config:     expandMCPOAuthConfig(config),
		store:      store,
	}, nil
}

func expandMCPOAuthConfig(config MCPOAuthConfig) MCPOAuthConfig {
	config.ClientID = expandConfigValue(config.ClientID)
	config.ClientSecret = expandConfigValue(config.ClientSecret)
	config.RedirectURI = expandConfigValue(config.RedirectURI)
	config.AuthServerMetadataURL = expandConfigValue(config.AuthServerMetadataURL)
	return config
}

func expandConfigValue(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "$") && !strings.HasPrefix(value, "${") {
		return os.Getenv(strings.TrimPrefix(value, "$"))
	}
	return os.ExpandEnv(value)
}

func (rt *mcpOAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := readAndRestoreBody(req)
	if err != nil {
		return nil, err
	}

	if authHeader, err := rt.authorizationHeader(req.Context()); err != nil {
		return nil, err
	} else if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	hadAuthHeader := req.Header.Get("Authorization") != ""
	resp, err := rt.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	challenge := parseMCPBearerChallenge(resp.Header.Values("WWW-Authenticate"))
	if resp.StatusCode != http.StatusUnauthorized || !challenge.bearer {
		return resp, nil
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if err := rt.authorize(req.Context(), challenge, hadAuthHeader); err != nil {
		return nil, err
	}

	retryReq := req.Clone(req.Context())
	retryReq.Body = io.NopCloser(bytes.NewReader(body))
	retryReq.ContentLength = int64(len(body))
	if len(body) == 0 && req.Body == nil {
		retryReq.Body = nil
		retryReq.ContentLength = 0
	}
	if authHeader, err := rt.authorizationHeader(req.Context()); err != nil {
		return nil, err
	} else if authHeader != "" {
		retryReq.Header.Set("Authorization", authHeader)
	}

	return rt.base.RoundTrip(retryReq)
}

func readAndRestoreBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func (rt *mcpOAuthRoundTripper) HTTPHeaderFunc(ctx context.Context) map[string]string {
	authHeader, err := rt.authorizationHeader(ctx)
	if err != nil || authHeader == "" {
		return nil
	}
	return map[string]string{"Authorization": authHeader}
}

func (rt *mcpOAuthRoundTripper) authorizationHeader(ctx context.Context) (string, error) {
	creds, err := rt.store.Load(ctx)
	if err != nil {
		if errors.Is(err, transport.ErrNoToken) {
			return "", nil
		}
		return "", err
	}
	if creds.Token == nil || creds.Token.AccessToken == "" {
		return "", nil
	}
	if tokenValid(creds.Token) {
		return tokenAuthorizationHeader(creds.Token), nil
	}
	if creds.Token.RefreshToken == "" {
		return "", nil
	}

	refreshed, err := rt.refreshToken(ctx, creds)
	if err != nil {
		return "", nil
	}
	return tokenAuthorizationHeader(refreshed), nil
}

func tokenValid(token *transport.Token) bool {
	return token != nil && token.AccessToken != "" && (token.ExpiresAt.IsZero() || time.Now().Add(mcpOAuthTokenSkew).Before(token.ExpiresAt))
}

func tokenAuthorizationHeader(token *transport.Token) string {
	tokenType := strings.TrimSpace(token.TokenType)
	if tokenType == "" || strings.EqualFold(tokenType, "bearer") {
		tokenType = "Bearer"
	}
	return tokenType + " " + token.AccessToken
}

func (rt *mcpOAuthRoundTripper) authorize(ctx context.Context, challenge mcpBearerChallenge, force bool) error {
	if !mcpOAuthInteractiveAllowed(ctx) {
		return errors.Errorf("MCP server %q requires OAuth authorization; run in an interactive session or set mcp.oauth.interactive to allow browser authorization", rt.serverName)
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	creds, err := rt.store.Load(ctx)
	if err == nil && tokenValid(creds.Token) && !force {
		return nil
	}
	if err != nil && !errors.Is(err, transport.ErrNoToken) {
		return err
	}

	callback, err := startMCPOAuthCallbackServer(rt.config.RedirectURI)
	if err != nil {
		return err
	}
	defer callback.Close()

	metadata, protectedResource, err := rt.discoverAuthMetadata(ctx, challenge.resourceMetadata)
	if err != nil {
		return err
	}

	registration := rt.registrationFromConfigOrStore(creds)
	if registration.ClientID == "" {
		registered, err := registerMCPClient(ctx, metadata, rt.clientName(), callback.RedirectURI, rt.oauthScopes(challenge, protectedResource))
		if err != nil {
			return err
		}
		registration = registered
	}

	codeVerifier, err := randomURLSafeString(64)
	if err != nil {
		return err
	}
	state, err := randomURLSafeString(32)
	if err != nil {
		return err
	}

	authURL, err := buildMCPAuthorizationURL(metadata.AuthorizationEndpoint, mcpAuthorizationParams{
		ClientID:      registration.ClientID,
		RedirectURI:   callback.RedirectURI,
		State:         state,
		CodeChallenge: codeChallenge(codeVerifier),
		Scopes:        rt.oauthScopes(challenge, protectedResource),
		Resource:      rt.oauthResource(protectedResource),
	})
	if err != nil {
		return err
	}

	if err := currentMCPOAuthPrompter(ctx).PromptMCPOAuth(ctx, rt.serverName, authURL); err != nil {
		return err
	}

	callbackResult, err := callback.Wait(ctx, mcpOAuthTimeout())
	if err != nil {
		return err
	}
	if callbackResult.Err != "" {
		return errors.Errorf("OAuth authorization failed: %s", callbackResult.Err)
	}
	if callbackResult.State != state {
		return errors.New("invalid OAuth state parameter, possible CSRF attack")
	}
	if callbackResult.Issuer != "" && metadata.Issuer != "" && callbackResult.Issuer != metadata.Issuer {
		return errors.New("invalid OAuth issuer in authorization response")
	}
	if callbackResult.Code == "" {
		return errors.New("OAuth authorization callback did not include an authorization code")
	}

	token, err := exchangeMCPAuthorizationCode(ctx, metadata, registration, callback.RedirectURI, callbackResult.Code, codeVerifier, rt.oauthResource(protectedResource))
	if err != nil {
		return err
	}

	return rt.store.Save(ctx, &mcpOAuthStoredCredentials{
		Token:                 token,
		ClientID:              registration.ClientID,
		ClientSecret:          registration.ClientSecret,
		Issuer:                metadata.Issuer,
		Resource:              rt.oauthResource(protectedResource),
		Scopes:                rt.oauthScopes(challenge, protectedResource),
		AuthServerMetadataURL: metadata.MetadataURL,
	})
}

func mcpOAuthInteractiveAllowed(ctx context.Context) bool {
	mode := strings.ToLower(strings.TrimSpace(viper.GetString("mcp.oauth.interactive")))
	switch mode {
	case "always", "true", "enabled", "on", "yes":
		return true
	case "never", "false", "disabled", "off", "no":
		return false
	default:
		return isTerminal(os.Stdin) || hasCustomMCPOAuthPrompter(ctx)
	}
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	stat, err := file.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

func mcpOAuthTimeout() time.Duration {
	if timeout := viper.GetDuration("mcp.oauth.callback_timeout"); timeout > 0 {
		return timeout
	}
	return mcpOAuthDefaultTimeout
}

func (rt *mcpOAuthRoundTripper) clientName() string {
	if strings.TrimSpace(rt.serverName) == "" {
		return "kodelet"
	}
	return "kodelet-" + rt.serverName
}

type mcpOAuthRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

func (rt *mcpOAuthRoundTripper) registrationFromConfigOrStore(creds *mcpOAuthStoredCredentials) mcpOAuthRegistration {
	if rt.config.ClientID != "" {
		return mcpOAuthRegistration{ClientID: rt.config.ClientID, ClientSecret: rt.config.ClientSecret}
	}
	if creds != nil && creds.ClientID != "" {
		return mcpOAuthRegistration{ClientID: creds.ClientID, ClientSecret: creds.ClientSecret}
	}
	return mcpOAuthRegistration{}
}

func (rt *mcpOAuthRoundTripper) oauthScopes(challenge mcpBearerChallenge, protectedResource *mcpProtectedResourceMetadata) []string {
	scopes := append([]string{}, rt.config.Scopes...)
	if len(challenge.scopes) > 0 {
		scopes = append(scopes, challenge.scopes...)
	} else if len(scopes) == 0 && protectedResource != nil {
		scopes = append(scopes, protectedResource.ScopesSupported...)
	}
	return uniqueStrings(scopes)
}

func (rt *mcpOAuthRoundTripper) oauthResource(protectedResource *mcpProtectedResourceMetadata) string {
	if protectedResource != nil && strings.TrimSpace(protectedResource.Resource) != "" {
		return strings.TrimSpace(protectedResource.Resource)
	}
	return rt.serverURL
}

func (rt *mcpOAuthRoundTripper) refreshToken(ctx context.Context, creds *mcpOAuthStoredCredentials) (*transport.Token, error) {
	metadata, _, err := rt.discoverAuthMetadata(ctx, "")
	if err != nil {
		return nil, err
	}
	registration := rt.registrationFromConfigOrStore(creds)
	if registration.ClientID == "" {
		return nil, errors.New("cannot refresh MCP OAuth token without a client ID")
	}
	resource := creds.Resource
	if resource == "" {
		resource = rt.serverURL
	}
	token, err := refreshMCPOAuthToken(ctx, metadata, registration, creds.Token.RefreshToken, resource)
	if err != nil {
		return nil, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = creds.Token.RefreshToken
	}
	creds.Token = token
	creds.ClientID = registration.ClientID
	creds.ClientSecret = registration.ClientSecret
	creds.AuthServerMetadataURL = metadata.MetadataURL
	if err := rt.store.Save(ctx, creds); err != nil {
		return nil, err
	}
	return token, nil
}

type mcpBearerChallenge struct {
	bearer           bool
	resourceMetadata string
	scopes           []string
}

func parseMCPBearerChallenge(values []string) mcpBearerChallenge {
	for _, value := range values {
		if !strings.Contains(strings.ToLower(value), "bearer") {
			continue
		}
		params := parseAuthParams(value)
		return mcpBearerChallenge{
			bearer:           true,
			resourceMetadata: params["resource_metadata"],
			scopes:           strings.Fields(params["scope"]),
		}
	}
	return mcpBearerChallenge{}
}

func parseAuthParams(value string) map[string]string {
	params := map[string]string{}
	idx := strings.Index(value, " ")
	if idx >= 0 {
		value = value[idx+1:]
	}

	for _, part := range splitAuthHeaderParams(value) {
		key, rawValue, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		rawValue = strings.TrimSpace(rawValue)
		rawValue = strings.Trim(rawValue, `"`)
		params[key] = rawValue
	}
	return params
}

func splitAuthHeaderParams(value string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	for _, r := range value {
		switch r {
		case '"':
			inQuotes = !inQuotes
			current.WriteRune(r)
		case ',':
			if inQuotes {
				current.WriteRune(r)
				continue
			}
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		parts = append(parts, strings.TrimSpace(current.String()))
	}
	return parts
}

type mcpOAuthStoredCredentials struct {
	Token                 *transport.Token `json:"token,omitempty"`
	ClientID              string           `json:"client_id,omitempty"`
	ClientSecret          string           `json:"client_secret,omitempty"`
	Issuer                string           `json:"issuer,omitempty"`
	Resource              string           `json:"resource,omitempty"`
	Scopes                []string         `json:"scopes,omitempty"`
	AuthServerMetadataURL string           `json:"auth_server_metadata_url,omitempty"`
}

type mcpOAuthCredentialStore struct {
	path string
	mu   sync.Mutex
}

func newMCPOAuthCredentialStore(serverName, serverURL string) (*mcpOAuthCredentialStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve user home directory")
	}
	filename := safeMCPServerTokenFilename(serverName, serverURL)
	return &mcpOAuthCredentialStore{path: filepath.Join(home, ".kodelet", "mcp", "oauth", filename)}, nil
}

func safeMCPServerTokenFilename(serverName, serverURL string) string {
	name := strings.TrimSpace(serverName)
	if name == "" {
		name = "server"
	}
	var safe strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			safe.WriteRune(r)
		} else {
			safe.WriteRune('_')
		}
	}
	hash := sha256.Sum256([]byte(serverURL))
	return fmt.Sprintf("%s-%s.json", strings.Trim(safe.String(), "_"), hex.EncodeToString(hash[:])[:12])
}

func (s *mcpOAuthCredentialStore) Load(ctx context.Context) (*mcpOAuthStoredCredentials, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, transport.ErrNoToken
		}
		return nil, errors.Wrap(err, "failed to open MCP OAuth credentials")
	}
	defer f.Close()

	var creds mcpOAuthStoredCredentials
	if err := json.NewDecoder(f).Decode(&creds); err != nil {
		return nil, errors.Wrap(err, "failed to decode MCP OAuth credentials")
	}
	if creds.Token == nil {
		return nil, transport.ErrNoToken
	}
	return &creds, nil
}

func (s *mcpOAuthCredentialStore) Save(ctx context.Context, creds *mcpOAuthStoredCredentials) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return errors.Wrap(err, "failed to create MCP OAuth credentials directory")
	}

	tmpPath := s.path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.Wrap(err, "failed to write MCP OAuth credentials")
	}
	encodeErr := json.NewEncoder(f).Encode(creds)
	closeErr := f.Close()
	if encodeErr != nil {
		_ = os.Remove(tmpPath)
		return errors.Wrap(encodeErr, "failed to encode MCP OAuth credentials")
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return errors.Wrap(closeErr, "failed to close MCP OAuth credentials")
	}
	return os.Rename(tmpPath, s.path)
}

type mcpAuthServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	MetadataURL                       string   `json:"-"`
}

type mcpProtectedResourceMetadata struct {
	AuthorizationServers []string `json:"authorization_servers"`
	Resource             string   `json:"resource"`
	ResourceName         string   `json:"resource_name,omitempty"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

func (rt *mcpOAuthRoundTripper) discoverAuthMetadata(ctx context.Context, resourceMetadataURL string) (*mcpAuthServerMetadata, *mcpProtectedResourceMetadata, error) {
	if rt.config.AuthServerMetadataURL != "" {
		metadata, err := fetchMCPAuthServerMetadata(ctx, rt.config.AuthServerMetadataURL)
		return metadata, nil, err
	}

	if creds, err := rt.store.Load(ctx); err == nil && creds.AuthServerMetadataURL != "" {
		metadata, fetchErr := fetchMCPAuthServerMetadata(ctx, creds.AuthServerMetadataURL)
		if fetchErr == nil {
			return metadata, nil, nil
		}
	}

	if resourceMetadataURL != "" {
		protectedResource, metadata, err := discoverFromProtectedResource(ctx, resourceMetadataURL)
		if err == nil {
			return metadata, protectedResource, nil
		}
	}

	baseURL, err := oauthBaseURL(rt.serverURL)
	if err != nil {
		return nil, nil, err
	}

	protectedResource, metadata, err := discoverFromProtectedResource(ctx, baseURL+"/.well-known/oauth-protected-resource")
	if err == nil {
		return metadata, protectedResource, nil
	}

	for _, metadataURL := range []string{
		baseURL + "/.well-known/oauth-authorization-server",
		baseURL + "/.well-known/openid-configuration",
	} {
		metadata, err := fetchMCPAuthServerMetadata(ctx, metadataURL)
		if err == nil {
			return metadata, nil, nil
		}
	}

	return nil, nil, errors.New("failed to discover OAuth authorization server metadata")
}

func oauthBaseURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.Errorf("invalid OAuth base URL %q", rawURL)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func discoverFromProtectedResource(ctx context.Context, resourceMetadataURL string) (*mcpProtectedResourceMetadata, *mcpAuthServerMetadata, error) {
	protectedResource, err := fetchMCPProtectedResourceMetadata(ctx, resourceMetadataURL)
	if err != nil {
		return nil, nil, err
	}
	for _, authServer := range protectedResource.AuthorizationServers {
		authServer = strings.TrimRight(authServer, "/")
		for _, metadataURL := range []string{
			authServer + "/.well-known/openid-configuration",
			authServer + "/.well-known/oauth-authorization-server",
		} {
			metadata, err := fetchMCPAuthServerMetadata(ctx, metadataURL)
			if err == nil {
				return protectedResource, metadata, nil
			}
		}
	}
	return nil, nil, errors.New("protected resource metadata did not identify a usable authorization server")
}

func fetchMCPProtectedResourceMetadata(ctx context.Context, metadataURL string) (*mcpProtectedResourceMetadata, error) {
	var metadata mcpProtectedResourceMetadata
	if err := fetchOAuthJSON(ctx, metadataURL, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func fetchMCPAuthServerMetadata(ctx context.Context, metadataURL string) (*mcpAuthServerMetadata, error) {
	var metadata mcpAuthServerMetadata
	if err := fetchOAuthJSON(ctx, metadataURL, &metadata); err != nil {
		return nil, err
	}
	if metadata.AuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
		return nil, errors.Errorf("OAuth metadata at %s is missing required endpoints", metadataURL)
	}
	metadata.MetadataURL = metadataURL
	return &metadata, nil
}

func fetchOAuthJSON(ctx context.Context, requestURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return errors.Errorf("GET %s failed with status %d: %s", requestURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func registerMCPClient(ctx context.Context, metadata *mcpAuthServerMetadata, clientName, redirectURI string, scopes []string) (mcpOAuthRegistration, error) {
	if metadata.RegistrationEndpoint == "" {
		return mcpOAuthRegistration{}, errors.New("authorization server does not support dynamic client registration and no OAuth client_id was configured")
	}

	requestBody := map[string]any{
		"client_name":                clientName,
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	}
	if len(scopes) > 0 {
		requestBody["scope"] = strings.Join(scopes, " ")
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return mcpOAuthRegistration{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, metadata.RegistrationEndpoint, bytes.NewReader(payload))
	if err != nil {
		return mcpOAuthRegistration{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return mcpOAuthRegistration{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return mcpOAuthRegistration{}, errors.Errorf("dynamic client registration failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var registration mcpOAuthRegistration
	if err := json.NewDecoder(resp.Body).Decode(&registration); err != nil {
		return mcpOAuthRegistration{}, err
	}
	if registration.ClientID == "" {
		return mcpOAuthRegistration{}, errors.New("dynamic client registration response did not include client_id")
	}
	return registration, nil
}

type mcpAuthorizationParams struct {
	ClientID      string
	RedirectURI   string
	State         string
	CodeChallenge string
	Scopes        []string
	Resource      string
}

func buildMCPAuthorizationURL(endpoint string, params mcpAuthorizationParams) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", params.ClientID)
	query.Set("redirect_uri", params.RedirectURI)
	query.Set("state", params.State)
	query.Set("code_challenge", params.CodeChallenge)
	query.Set("code_challenge_method", "S256")
	if len(params.Scopes) > 0 {
		query.Set("scope", strings.Join(params.Scopes, " "))
	}
	if params.Resource != "" {
		query.Set("resource", params.Resource)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func exchangeMCPAuthorizationCode(ctx context.Context, metadata *mcpAuthServerMetadata, registration mcpOAuthRegistration, redirectURI, code, codeVerifier, resource string) (*transport.Token, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("client_id", registration.ClientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("code_verifier", codeVerifier)
	if registration.ClientSecret != "" {
		values.Set("client_secret", registration.ClientSecret)
	}
	if resource != "" {
		values.Set("resource", resource)
	}
	return sendMCPTokenRequest(ctx, metadata.TokenEndpoint, values)
}

func refreshMCPOAuthToken(ctx context.Context, metadata *mcpAuthServerMetadata, registration mcpOAuthRegistration, refreshToken, resource string) (*transport.Token, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("client_id", registration.ClientID)
	if registration.ClientSecret != "" {
		values.Set("client_secret", registration.ClientSecret)
	}
	if resource != "" {
		values.Set("resource", resource)
	}
	return sendMCPTokenRequest(ctx, metadata.TokenEndpoint, values)
}

func sendMCPTokenRequest(ctx context.Context, endpoint string, values url.Values) (*transport.Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("OAuth token request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var token transport.Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, err
	}
	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return &token, nil
}

type mcpOAuthCallbackServer struct {
	server      *http.Server
	listener    net.Listener
	resultCh    chan mcpOAuthCallbackResult
	RedirectURI string
}

type mcpOAuthCallbackResult struct {
	Code   string
	State  string
	Issuer string
	Err    string
}

func startMCPOAuthCallbackServer(configuredRedirectURI string) (*mcpOAuthCallbackServer, error) {
	path := mcpOAuthCallbackPath
	listenAddr := "127.0.0.1:0"
	redirectURI := ""

	if configuredRedirectURI != "" {
		parsed, err := url.Parse(configuredRedirectURI)
		if err != nil {
			return nil, err
		}
		if parsed.Scheme != "http" || parsed.Host == "" {
			return nil, errors.New("MCP OAuth redirect_uri must be an http loopback URL")
		}
		if parsed.Path != "" {
			path = parsed.Path
		}
		listenAddr = parsed.Host
		redirectURI = configuredRedirectURI
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to start MCP OAuth callback server")
	}

	if redirectURI == "" {
		redirectURI = "http://" + listener.Addr().String() + path
	}

	callback := &mcpOAuthCallbackServer{
		listener:    listener,
		resultCh:    make(chan mcpOAuthCallbackResult, 1),
		RedirectURI: redirectURI,
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, callback.handle)
	callback.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		_ = callback.server.Serve(listener)
	}()

	return callback, nil
}

func (s *mcpOAuthCallbackServer) handle(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	result := mcpOAuthCallbackResult{
		Code:   query.Get("code"),
		State:  query.Get("state"),
		Issuer: query.Get("iss"),
		Err:    query.Get("error"),
	}
	select {
	case s.resultCh <- result:
	default:
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><html><body><h1>Authorization complete</h1><p>You can close this window and return to Kodelet.</p><script>window.close();</script></body></html>`)
}

func (s *mcpOAuthCallbackServer) Wait(ctx context.Context, timeout time.Duration) (mcpOAuthCallbackResult, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return mcpOAuthCallbackResult{}, ctx.Err()
	case <-timer.C:
		return mcpOAuthCallbackResult{}, errors.New("timed out waiting for OAuth callback")
	case result := <-s.resultCh:
		return result, nil
	}
}

func (s *mcpOAuthCallbackServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

func randomURLSafeString(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func codeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
