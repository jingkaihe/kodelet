package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"golang.org/x/term"
)

// Keep the existing Node extension's credential format and filename so upgrades
// can reuse client registrations and refresh tokens. Expiry is added for new tokens.
type oauthCredentials struct {
	Tokens            *oauthToken                         `json:"tokens,omitempty"`
	ClientInformation *oauthex.ClientRegistrationResponse `json:"clientInformation,omitempty"`
	DiscoveryState    *oauthDiscoveryState                `json:"discoveryState,omitempty"`
	MetadataURL       string                              `json:"authServerMetadataUrl,omitempty"`
}

type oauthToken struct {
	oauth2.Token
	Scope string `json:"scope,omitempty"`
}

type oauthDiscoveryState struct {
	ServerURL   string                  `json:"authorizationServerUrl"`
	Metadata    *oauthex.AuthServerMeta `json:"authorizationServerMetadata"`
	ResourceURI string                  `json:"resource,omitempty"`
}

type oauthTransport struct {
	name, serverURL, credentialPath string
	config                          oauthConfig
	base                            *http.Transport
	client                          *http.Client // Never includes configured MCP headers.
	ctx                             context.Context
	gate                            chan struct{} // Serializes refresh and concurrent challenges.
	credentials                     oauthCredentials
	generation                      uint64
	challengeErr                    error
	openBrowser                     func(string) error
}

func newOAuthClient(name, serverURL string, config oauthConfig) (*http.Client, func(), error) {
	endpoint, err := url.Parse(serverURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, nil, errors.Errorf("invalid MCP server URL %q", serverURL)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, errors.Wrap(err, "find MCP OAuth credential directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	base := http.DefaultTransport.(*http.Transport).Clone()
	t := &oauthTransport{
		name: name, serverURL: serverURL, config: config, base: base, ctx: ctx,
		credentialPath: filepath.Join(home, ".kodelet", "mcp", "oauth", oauthTokenFilename(name, serverURL)),
		gate:           make(chan struct{}, 1),
		openBrowser:    openOAuthBrowser,
		client:         &http.Client{Transport: base, Timeout: 30 * time.Second, CheckRedirect: sameOriginRedirect},
	}
	if err := t.load(); err != nil {
		cancel()
		return nil, nil, err
	}
	cleanup := func() {
		cancel()
		base.CloseIdleConnections()
	}
	return &http.Client{Transport: t, CheckRedirect: sameOriginRedirect}, cleanup, nil
}

func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 || req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return errors.New("MCP/OAuth redirect must stay on the same origin")
	}
	return nil
}

func (t *oauthTransport) lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.ctx.Done():
		return t.ctx.Err()
	case t.gate <- struct{}{}:
		return nil
	}
}

func (t *oauthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// SSE may use another path on this origin, but must not send credentials to
	// a different origin advertised by the server or reached through a redirect.
	endpoint, _ := url.Parse(t.serverURL)
	if req.URL.Scheme != endpoint.Scheme || req.URL.Host != endpoint.Host {
		return nil, errors.New("MCP request must stay on the configured origin")
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	defer cancel()
	if err := t.lock(ctx); err != nil {
		return nil, err
	}
	token, err := t.token(ctx, false)
	generation := t.generation
	<-t.gate
	if err != nil {
		return nil, err
	}
	request := req.Clone(req.Context())
	if token != nil {
		token.SetAuthHeader(request)
	}
	response, err := t.base.RoundTrip(request)
	if err != nil || (response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden) {
		return response, err
	}
	if response.StatusCode == http.StatusForbidden {
		challenges, err := oauthex.ParseWWWAuthenticate(response.Header.Values("WWW-Authenticate"))
		if err != nil || !slices.ContainsFunc(challenges, func(challenge oauthex.Challenge) bool {
			return challenge.Scheme == "bearer" && challenge.Params["error"] == "insufficient_scope"
		}) {
			return response, nil
		}
	}
	// The SDK's HTTP and SSE transports use replayable request bodies.
	if req.Body != nil && req.GetBody == nil {
		return response, nil
	}
	response.Body.Close()
	if err := t.lock(ctx); err != nil {
		return nil, err
	}
	err = func() error {
		defer func() { <-t.gate }()
		if t.generation == generation {
			// Refresh rejected tokens, but scope escalation requires authorization.
			if response.StatusCode == http.StatusUnauthorized {
				token, err = t.token(ctx, true)
			}
			if err == nil && (token == nil || t.generation == generation || response.StatusCode == http.StatusForbidden) {
				err = t.authorize(ctx, response.Header.Values("WWW-Authenticate"))
			}
			// Concurrent challenges share failures as well as successful tokens.
			t.generation++
			t.challengeErr = err
		}
		if t.credentials.Tokens != nil {
			token = &t.credentials.Tokens.Token
		}
		return t.challengeErr
	}()
	if err != nil {
		return nil, errors.Wrapf(err, "authorize MCP server %q", t.name)
	}
	request = req.Clone(req.Context())
	if req.Body != nil {
		request.Body, err = req.GetBody()
		if err != nil {
			return nil, errors.Wrap(err, "replay MCP request")
		}
	}
	if token != nil {
		token.SetAuthHeader(request)
	}
	return t.base.RoundTrip(request)
}

func (t *oauthTransport) token(ctx context.Context, forceRefresh bool) (*oauth2.Token, error) {
	token := t.credentials.Tokens
	if token == nil {
		return nil, nil //nolint:nilnil // No token has been acquired yet.
	}
	if !forceRefresh && token.Valid() {
		return &token.Token, nil
	}
	if token.RefreshToken == "" {
		return nil, nil //nolint:nilnil // No usable token; wait for an OAuth challenge.
	}
	if t.config.AuthServerMetadataURL != "" || t.credentials.DiscoveryState == nil || t.credentials.DiscoveryState.Metadata == nil {
		state, _, err := t.discover(ctx, nil)
		if err != nil {
			return nil, err
		}
		t.credentials.DiscoveryState = state
	}
	if err := validateOAuthMetadata(t.credentials.DiscoveryState.Metadata); err != nil {
		return nil, err
	}
	cfg := t.oauthConfig(t.credentials.DiscoveryState.Metadata)
	if cfg.ClientID == "" {
		return nil, nil //nolint:nilnil // No usable client; authorization may register one.
	}
	resourceURI := t.credentials.DiscoveryState.ResourceURI
	if resourceURI == "" {
		resourceURI = t.serverURL // Older credential files did not store the resource.
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, t.client)
	// Exchange accepts grant parameters, unlike TokenSource. An empty code is
	// ignored per RFC 6749, as in the SDK's other token exchange helpers.
	refreshed, err := cfg.Exchange(ctx, "",
		oauth2.SetAuthURLParam("grant_type", "refresh_token"),
		oauth2.SetAuthURLParam("refresh_token", token.RefreshToken),
		oauth2.SetAuthURLParam("resource", resourceURI),
	)
	if err != nil {
		var rejected *oauth2.RetrieveError
		if errors.As(err, &rejected) && (rejected.ErrorCode == "invalid_grant" || rejected.ErrorCode == "invalid_client") {
			t.credentials.Tokens = nil
			if rejected.ErrorCode == "invalid_client" {
				t.credentials.ClientInformation = nil
			}
			return nil, t.save()
		}
		return nil, errors.Wrap(err, "refresh MCP OAuth token")
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	t.challengeErr = nil
	return refreshed, t.saveToken(refreshed)
}

func (t *oauthTransport) saveToken(token *oauth2.Token) error {
	scope, _ := token.Extra("scope").(string)
	if scope == "" && t.credentials.Tokens != nil {
		scope = t.credentials.Tokens.Scope
	}
	t.credentials.Tokens = &oauthToken{Token: *token, Scope: scope}
	t.generation++
	return t.save()
}

func (t *oauthTransport) oauthConfig(metadata *oauthex.AuthServerMeta) *oauth2.Config {
	cfg := &oauth2.Config{
		ClientID: t.config.ClientID, ClientSecret: t.config.ClientSecret, Scopes: t.config.Scopes,
		Endpoint: oauth2.Endpoint{AuthURL: metadata.AuthorizationEndpoint, TokenURL: metadata.TokenEndpoint, AuthStyle: oauth2.AuthStyleInParams},
	}
	if cfg.ClientID == "" && t.credentials.ClientInformation != nil {
		cfg.ClientID = t.credentials.ClientInformation.ClientID
		cfg.ClientSecret = t.credentials.ClientInformation.ClientSecret
		if t.credentials.ClientInformation.TokenEndpointAuthMethod == "client_secret_basic" {
			cfg.Endpoint.AuthStyle = oauth2.AuthStyleInHeader
		}
	}
	return cfg
}

func (t *oauthTransport) authorize(ctx context.Context, headers []string) error {
	if strings.HasPrefix(strings.ToLower(t.config.Flow), "device") {
		return errors.New("MCP OAuth device flow is not supported")
	}
	discovery, scopes, err := t.discover(ctx, headers)
	if err != nil {
		return err
	}
	metadata := discovery.Metadata
	if !oauthInteractiveAllowed(t.config.Interactive) {
		return errors.New("OAuth authorization requires an interactive session or oauth.interactive: always")
	}
	ctx, cancel := context.WithTimeout(ctx, oauthCallbackTimeout(t.config.CallbackTimeout))
	defer cancel()
	redirectURI := t.config.RedirectURI
	if redirectURI == "" && t.credentials.ClientInformation != nil && len(t.credentials.ClientInformation.RedirectURIs) > 0 {
		// RFC 8252 allows loopback port changes, but some providers still require
		// an exact match with the previously registered redirect URI.
		redirectURI = t.credentials.ClientInformation.RedirectURIs[0]
	}
	redirect, results, closeCallback, err := startOAuthCallback(redirectURI)
	if err != nil {
		return err
	}
	defer closeCallback()
	if t.config.ClientID == "" && (t.credentials.ClientInformation == nil ||
		(!t.credentials.ClientInformation.ClientSecretExpiresAt.IsZero() && time.Now().After(t.credentials.ClientInformation.ClientSecretExpiresAt))) {
		if metadata.RegistrationEndpoint == "" {
			return errors.New("OAuth server does not support dynamic registration; configure oauth.client_id")
		}
		registration, err := oauthex.RegisterClient(ctx, metadata.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
			RedirectURIs: []string{redirect}, TokenEndpointAuthMethod: "none",
			ClientName: "kodelet-" + t.name, GrantTypes: []string{"authorization_code", "refresh_token"},
			ResponseTypes: []string{"code"}, Scope: strings.Join(scopes, " "), ApplicationType: "native",
		}, t.client)
		if err != nil {
			return errors.Wrap(err, "register MCP OAuth client")
		}
		t.credentials.ClientInformation = registration
	}
	t.credentials.DiscoveryState = discovery
	if err := t.save(); err != nil {
		return err
	}
	cfg := t.oauthConfig(metadata)
	cfg.RedirectURL, cfg.Scopes = redirect, scopes
	state, verifier := rand.Text(), oauth2.GenerateVerifier()
	authorizationURL := cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", discovery.ResourceURI))
	slog.Info("MCP server requires OAuth authorization; open this URL", "server", t.name, "url", authorizationURL)
	if t.config.OpenBrowser == nil || *t.config.OpenBrowser {
		if err := t.openBrowser(authorizationURL); err != nil {
			slog.Warn("Could not open OAuth browser automatically", "error", err)
		}
	}
	select {
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "waiting for MCP OAuth callback")
	case result := <-results:
		if result.Get("state") != state {
			return errors.New("invalid OAuth state parameter")
		}
		if issuer := result.Get("iss"); (issuer != "" && issuer != metadata.Issuer) || (issuer == "" && metadata.AuthorizationResponseIssParameterSupported) {
			return errors.New("invalid OAuth issuer in authorization response")
		}
		if result.Get("error") != "" {
			return errors.Errorf("OAuth authorization failed: %s: %s", result.Get("error"), result.Get("error_description"))
		}
		if result.Get("code") == "" {
			return errors.New("OAuth callback did not include an authorization code")
		}
		ctx = context.WithValue(ctx, oauth2.HTTPClient, t.client)
		token, err := cfg.Exchange(ctx, result.Get("code"), oauth2.VerifierOption(verifier), oauth2.SetAuthURLParam("resource", discovery.ResourceURI))
		if err != nil {
			return errors.Wrap(err, "exchange MCP OAuth authorization code")
		}
		return t.saveToken(token)
	}
}

func (t *oauthTransport) discover(ctx context.Context, headers []string) (*oauthDiscoveryState, []string, error) {
	challenges, err := oauthex.ParseWWWAuthenticate(headers)
	if err != nil {
		return nil, nil, err
	}
	scopes := t.config.Scopes
	state := &oauthDiscoveryState{ResourceURI: t.serverURL}
	metadataURL := t.config.AuthServerMetadataURL
	if metadataURL == "" {
		metadataURL = t.credentials.MetadataURL
	}
	endpoint, _ := url.Parse(t.serverURL)
	issuer := endpoint.Scheme + "://" + endpoint.Host
	resourceURL := ""
	for _, challenge := range challenges {
		if challenge.Scheme == "bearer" {
			resourceURL = challenge.Params["resource_metadata"]
			if required := strings.Fields(challenge.Params["scope"]); len(required) > 0 {
				scopes = required
			}
		}
	}
	if metadataURL != "" {
		// An explicit metadata URL need not encode its issuer in its path.
		if previous := t.credentials.DiscoveryState; previous != nil && previous.ResourceURI != "" {
			state.ResourceURI = previous.ResourceURI
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
		if err != nil {
			return nil, nil, err
		}
		request.Header.Set("Accept", "application/json")
		response, err := t.client.Do(request)
		if err != nil {
			return nil, nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, nil, errors.Errorf("OAuth metadata request failed: %s", response.Status)
		}
		var metadata oauthex.AuthServerMeta
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&metadata); err != nil {
			return nil, nil, errors.Wrap(err, "decode OAuth metadata")
		}
		state.ServerURL, state.Metadata = metadata.Issuer, &metadata
		return state, scopes, validateOAuthMetadata(&metadata)
	}
	resourceURLs := []string{issuer + "/.well-known/oauth-protected-resource" + endpoint.Path, issuer + "/.well-known/oauth-protected-resource"}
	if resourceURL != "" {
		resourceURLs = []string{resourceURL}
	}
	for _, address := range resourceURLs {
		resourceURI := t.serverURL
		if resourceURL == "" && address == issuer+"/.well-known/oauth-protected-resource" {
			resourceURI = issuer
		}
		resource, err := oauthex.GetProtectedResourceMetadata(ctx, address, resourceURI, t.client)
		if err != nil {
			if resourceURL != "" {
				return nil, nil, err
			}
			continue
		}
		if len(resource.AuthorizationServers) == 0 {
			return nil, nil, errors.New("MCP resource metadata has no authorization servers")
		}
		issuer = resource.AuthorizationServers[0]
		state.ResourceURI = resource.Resource
		if len(scopes) == 0 {
			scopes = resource.ScopesSupported
		}
		break
	}
	metadata, err := auth.GetAuthServerMetadata(ctx, issuer, t.client)
	if err != nil {
		return nil, nil, err
	}
	if metadata == nil {
		metadata = &oauthex.AuthServerMeta{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", RegistrationEndpoint: issuer + "/register"}
	}
	state.ServerURL, state.Metadata = metadata.Issuer, metadata
	return state, scopes, validateOAuthMetadata(metadata)
}

func validateOAuthMetadata(metadata *oauthex.AuthServerMeta) error {
	addresses := []string{metadata.Issuer, metadata.AuthorizationEndpoint, metadata.TokenEndpoint}
	if metadata.RegistrationEndpoint != "" {
		addresses = append(addresses, metadata.RegistrationEndpoint)
	}
	for _, address := range addresses {
		parsed, err := url.Parse(address)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopbackHost(parsed.Hostname()))) {
			return errors.Errorf("invalid OAuth endpoint %q: HTTPS or HTTP loopback required", address)
		}
	}
	if len(metadata.CodeChallengeMethodsSupported) > 0 && !slices.Contains(metadata.CodeChallengeMethodsSupported, "S256") {
		return errors.New("OAuth server does not support PKCE S256")
	}
	return nil
}

func startOAuthCallback(configured string) (string, <-chan url.Values, func(), error) {
	if configured == "" {
		configured = "http://127.0.0.1:0/mcp/oauth/callback"
	}
	redirect, err := url.Parse(configured)
	if err != nil || redirect.Scheme != "http" || !isLoopbackHost(redirect.Hostname()) || redirect.User != nil {
		return "", nil, nil, errors.New("MCP OAuth redirect_uri must be an HTTP loopback URL")
	}
	port := redirect.Port()
	if port == "" {
		port = "0"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(redirect.Hostname(), port))
	if err != nil {
		return "", nil, nil, errors.Wrap(err, "listen for MCP OAuth callback")
	}
	redirect.Host = net.JoinHostPort(redirect.Hostname(), strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	if redirect.Path == "" || redirect.Path == "/" {
		redirect.Path = "/mcp/oauth/callback"
	}
	results := make(chan url.Values, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != redirect.Path {
			http.NotFound(w, r)
			return
		}
		select {
		case results <- r.URL.Query():
		default:
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "Authorization received. You can close this window and return to Kodelet.")
	})}
	go func() { _ = server.Serve(listener) }()
	return redirect.String(), results, func() { _ = server.Close() }, nil
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func oauthInteractiveAllowed(value any) bool {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "always", "true", "enabled", "on", "yes":
		return true
	case "never", "false", "disabled", "off", "no":
		return false
	default:
		return term.IsTerminal(int(os.Stdin.Fd())) || term.IsTerminal(int(os.Stderr.Fd()))
	}
}

func oauthCallbackTimeout(value any) time.Duration {
	text := fmt.Sprint(value)
	if milliseconds, err := strconv.ParseFloat(text, 64); err == nil && milliseconds > 0 {
		return time.Duration(milliseconds * float64(time.Millisecond))
	}
	if duration, err := time.ParseDuration(text); err == nil && duration > 0 {
		return duration
	}
	return 2 * time.Minute
}

func openOAuthBrowser(address string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	process := exec.Command(command, address)
	if err := process.Start(); err != nil {
		return err
	}
	go func() { _ = process.Wait() }()
	return nil
}

func oauthTokenFilename(name, serverURL string) string {
	name = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "server"
	}
	hash := sha256.Sum256([]byte(serverURL))
	return fmt.Sprintf("%s-%x.json", name, hash[:6])
}

func (t *oauthTransport) load() error {
	data, err := os.ReadFile(t.credentialPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "read MCP OAuth credentials")
	}
	if err := json.Unmarshal(data, &t.credentials); err != nil {
		return errors.Wrap(err, "decode MCP OAuth credentials")
	}
	var legacy struct {
		Token       *oauthToken `json:"token"`
		ClientID    string      `json:"client_id"`
		Secret      string      `json:"client_secret"`
		MetadataURL string      `json:"auth_server_metadata_url"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return errors.Wrap(err, "decode legacy MCP OAuth credentials")
	}
	if t.credentials.Tokens == nil {
		t.credentials.Tokens = legacy.Token
	}
	if t.credentials.ClientInformation == nil && legacy.ClientID != "" {
		t.credentials.ClientInformation = &oauthex.ClientRegistrationResponse{ClientID: legacy.ClientID, ClientSecret: legacy.Secret}
	}
	if t.credentials.MetadataURL == "" {
		t.credentials.MetadataURL = legacy.MetadataURL
	}
	return nil
}

func (t *oauthTransport) save() error {
	if err := os.MkdirAll(filepath.Dir(t.credentialPath), 0o700); err != nil {
		return errors.Wrap(err, "create MCP OAuth credential directory")
	}
	data, err := json.MarshalIndent(t.credentials, "", "  ")
	if err != nil {
		return errors.Wrap(err, "encode MCP OAuth credentials")
	}
	file, err := os.CreateTemp(filepath.Dir(t.credentialPath), ".oauth-*")
	if err != nil {
		return errors.Wrap(err, "create MCP OAuth credential file")
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return errors.Wrap(writeErr, "write MCP OAuth credentials")
	}
	if closeErr != nil {
		return errors.Wrap(closeErr, "close MCP OAuth credentials")
	}
	return errors.Wrap(os.Rename(file.Name(), t.credentialPath), "save MCP OAuth credentials")
}
