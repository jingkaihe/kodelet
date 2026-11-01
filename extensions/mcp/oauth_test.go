package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestOAuthFlowPersistenceAndRefresh(t *testing.T) {
	for _, mode := range []string{"dynamic", "configured", "root resource", "occupied callback"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			configured := mode == "configured"
			var registrations, authorizations, refreshes, challenges atomic.Int32
			var registeredRedirect, proof atomic.Value
			registeredRedirect.Store("")
			firstChallenges := make(chan struct{})
			var serverURL, resourceURI string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/mcp" {
					assert.Empty(t, r.Header.Get("X-MCP-Key"), "MCP headers must not reach OAuth endpoints")
					assert.Empty(t, r.Header.Get("Authorization"))
				}
				switch r.URL.Path {
				case "/mcp":
					body, err := io.ReadAll(r.Body)
					assert.NoError(t, err)
					assert.Equal(t, "payload", string(body), "challenge retry must replay the body")
					assert.Equal(t, "private", r.Header.Get("X-MCP-Key"))
					if r.Header.Get("Authorization") != "Bearer access" {
						if challenges.Add(1) == 3 {
							close(firstChallenges)
						}
						select {
						case <-firstChallenges:
						case <-r.Context().Done():
							return
						}
						w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, serverURL+"/resource"))
						if configured {
							w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="mcp.read"`)
							w.WriteHeader(http.StatusForbidden)
						} else {
							if mode == "root resource" {
								w.Header().Set("WWW-Authenticate", "Bearer")
							}
							w.WriteHeader(http.StatusUnauthorized)
						}
						return
					}
					fmt.Fprintln(w, `{"ok":true}`)
				case "/resource", "/.well-known/oauth-protected-resource":
					json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{Resource: resourceURI, AuthorizationServers: []string{serverURL}})
				case "/.well-known/oauth-authorization-server", "/custom-metadata":
					json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
						Issuer: serverURL, AuthorizationEndpoint: serverURL + "/authorize", TokenEndpoint: serverURL + "/token",
						RegistrationEndpoint: serverURL + "/register", CodeChallengeMethodsSupported: []string{"S256"},
					})
				case "/register":
					registrations.Add(1)
					var metadata oauthex.ClientRegistrationMetadata
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&metadata)) || !assert.Len(t, metadata.RedirectURIs, 1) {
						return
					}
					registeredRedirect.Store(metadata.RedirectURIs[0])
					json.NewEncoder(w).Encode(&oauthex.ClientRegistrationResponse{ClientID: "client", ClientRegistrationMetadata: metadata})
				case "/authorize":
					authorizations.Add(1)
					query := r.URL.Query()
					assert.Equal(t, "client", query.Get("client_id"))
					assert.Equal(t, "mcp.read", query.Get("scope"))
					assert.Equal(t, "S256", query.Get("code_challenge_method"))
					assert.Equal(t, resourceURI, query.Get("resource"))
					proof.Store(query.Get("code_challenge"))
					if !configured {
						assert.Equal(t, registeredRedirect.Load(), query.Get("redirect_uri"), "reuse the registered callback port")
					}
					callback, err := url.Parse(query.Get("redirect_uri"))
					if !assert.NoError(t, err) {
						return
					}
					callback.RawQuery = url.Values{"code": {"code"}, "state": {query.Get("state")}, "iss": {serverURL}}.Encode()
					http.Redirect(w, r, callback.String(), http.StatusFound)
				case "/token":
					assert.NoError(t, r.ParseForm())
					assert.Equal(t, "client", r.Form.Get("client_id"))
					assert.Equal(t, resourceURI, r.Form.Get("resource"), "token exchange and refresh use the discovered resource")
					if configured {
						assert.Equal(t, "secret", r.Form.Get("client_secret"))
					}
					if r.Form.Get("grant_type") == "refresh_token" {
						refreshes.Add(1)
						assert.Equal(t, "refresh", r.Form.Get("refresh_token"))
					} else {
						assert.Equal(t, proof.Load(), oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")))
						assert.Equal(t, "code", r.Form.Get("code"))
					}
					fmt.Fprintln(w, `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":3600}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			serverURL = server.URL
			resourceURI = server.URL + "/mcp"
			if mode == "root resource" {
				resourceURI = server.URL
			}
			cfg := oauthConfig{Interactive: "always", Scopes: []string{"mcp.read"}, CallbackTimeout: "5s"}
			if configured {
				cfg.ClientID, cfg.ClientSecret = "client", "secret"
				cfg.AuthServerMetadataURL = server.URL + "/custom-metadata"
				cfg.Scopes = []string{"mcp.base"} // A scope challenge overrides configured defaults.
			}
			newClient := func() (*http.Client, *oauthTransport) {
				client, closeClient, err := newOAuthClient("Test Server", server.URL+"/mcp", cfg)
				require.NoError(t, err)
				t.Cleanup(closeClient)
				client.Timeout = 5 * time.Second
				transport := client.Transport.(*oauthTransport)
				transport.openBrowser = func(address string) error {
					response, err := (&http.Client{Timeout: 5 * time.Second}).Get(address)
					if err == nil {
						response.Body.Close()
					}
					return err
				}
				endpoint, err := url.Parse(server.URL + "/mcp")
				require.NoError(t, err)
				client.Transport = &headerTransport{base: transport, endpoint: endpoint, headers: map[string]string{"X-MCP-Key": "private", "Authorization": "Bearer configured"}}
				return client, transport
			}
			request := func(client *http.Client) {
				response, err := client.Post(server.URL+"/mcp", "application/json", strings.NewReader("payload"))
				if assert.NoError(t, err) {
					response.Body.Close()
					assert.Equal(t, http.StatusOK, response.StatusCode)
				}
			}
			client, transport := newClient()
			var group sync.WaitGroup
			for range 3 {
				group.Go(func() { request(client) })
			}
			group.Wait()
			require.EqualValues(t, 1, authorizations.Load())
			info, err := os.Stat(transport.credentialPath)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			require.NotNil(t, transport.credentials.Tokens)
			transport.credentials.Tokens.Expiry = time.Now().Add(-time.Hour)
			require.NoError(t, transport.save())
			cfg.Interactive = "never"
			client, transport = newClient()
			assert.Equal(t, resourceURI, transport.credentials.DiscoveryState.ResourceURI, "validated resource survives reload")
			request(client)
			assert.EqualValues(t, 1, refreshes.Load())
			assert.EqualValues(t, 1, authorizations.Load())
			// Force a new authorization using the persisted client registration.
			if mode == "occupied callback" {
				callback, err := url.Parse(registeredRedirect.Load().(string))
				require.NoError(t, err)
				listener, err := net.Listen("tcp", callback.Host)
				require.NoError(t, err)
				defer listener.Close()
			}
			transport.credentials.Tokens = nil
			require.NoError(t, transport.save())
			cfg.Interactive = "always"
			client, _ = newClient()
			request(client)
			assert.EqualValues(t, 2, authorizations.Load())
			if configured {
				assert.Zero(t, registrations.Load())
			} else if mode == "occupied callback" {
				assert.EqualValues(t, 2, registrations.Load(), "the replacement callback must be registered")
			} else {
				assert.EqualValues(t, 1, registrations.Load())
			}
		})
	}
}

func TestOAuthPreservesOrdinaryForbiddenResponses(t *testing.T) {
	for _, challenge := range []string{"", `Bearer error="invalid_token"`, `Basic error="insufficient_scope"`, `Bearer error="`} {
		t.Run(challenge, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("WWW-Authenticate", challenge)
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, "permission denied")
			}))
			defer server.Close()
			client, cleanup, err := newOAuthClient("forbidden", server.URL, oauthConfig{Interactive: "always"})
			require.NoError(t, err)
			defer cleanup()
			client.Transport.(*oauthTransport).openBrowser = func(string) error {
				t.Error("ordinary permission failures must not open a browser")
				return nil
			}
			response, err := client.Get(server.URL)
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			assert.Equal(t, http.StatusForbidden, response.StatusCode)
			assert.Equal(t, challenge, response.Header.Get("WWW-Authenticate"))
			assert.Equal(t, "permission denied", string(body))
			assert.EqualValues(t, 1, requests.Load(), "no discovery or authorization requests")
		})
	}
}

func TestOAuthLegacyCredentialsAndOriginScoping(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var foreignRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignRequests.Add(1) }))
	defer foreign.Close()
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/metadata":
			json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
				Issuer: serverURL, AuthorizationEndpoint: serverURL + "/authorize", TokenEndpoint: serverURL + "/token",
			})
		case "/token":
			assert.Empty(t, r.Header.Get("Authorization"))
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, serverURL, r.Form.Get("resource"), "legacy credentials fall back to the configured server URL")
			assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
			assert.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
			assert.Equal(t, "old-client", r.Form.Get("client_id"))
			assert.Equal(t, "old-secret", r.Form.Get("client_secret"))
			fmt.Fprintln(w, `{"access_token":"renewed","token_type":"Bearer","expires_in":3600}`)
		case "/refresh":
			if r.Header.Get("Authorization") != "Bearer renewed" {
				w.WriteHeader(http.StatusUnauthorized)
			}
		default:
			assert.Equal(t, "Bearer old-access", r.Header.Get("Authorization"))
			http.Redirect(w, r, foreign.URL, http.StatusFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL
	client, cleanup, err := newOAuthClient(" My Server! ", server.URL, oauthConfig{Interactive: "never"})
	require.NoError(t, err)
	defer cleanup()
	transport := client.Transport.(*oauthTransport)
	require.NoError(t, transport.save())
	legacy := fmt.Sprintf(`{
		"token": {"access_token": "old-access", "refresh_token": "old-refresh", "token_type": "Bearer", "scope": "mcp.read"},
		"client_id": "old-client", "client_secret": "old-secret",
		"auth_server_metadata_url": %q
	}`, server.URL+"/metadata")
	require.NoError(t, os.WriteFile(transport.credentialPath, []byte(legacy), 0o600))
	require.NoError(t, transport.load())
	assert.Equal(t, "old-refresh", transport.credentials.Tokens.RefreshToken)
	assert.Equal(t, "old-client", transport.credentials.ClientInformation.ClientID)
	assert.Equal(t, "old-secret", transport.credentials.ClientInformation.ClientSecret)
	assert.Equal(t, server.URL+"/metadata", transport.credentials.MetadataURL)
	assert.Regexp(t, `my_server-[a-f0-9]{12}[.]json$`, transport.credentialPath)
	_, err = client.Get(server.URL)
	assert.ErrorContains(t, err, "same origin")
	_, err = client.Get(foreign.URL) // SSE advertised endpoints go through RoundTrip directly.
	assert.ErrorContains(t, err, "configured origin")
	assert.Zero(t, foreignRequests.Load())
	response, err := client.Get(server.URL + "/refresh")
	require.NoError(t, err, "legacy credentials without discovery state must refresh noninteractively")
	response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "renewed", transport.credentials.Tokens.AccessToken)
	assert.Equal(t, "old-refresh", transport.credentials.Tokens.RefreshToken)
	assert.Equal(t, "mcp.read", transport.credentials.Tokens.Scope)
}

func TestOAuthCallbackLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	address := listener.Addr().String()
	redirectURI := "http://" + address + "/callback"
	firstExchange := make(chan struct{})
	releaseExchange := make(chan struct{})
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mcp":
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/metadata":
			json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
				Issuer: serverURL, AuthorizationEndpoint: serverURL + "/authorize", TokenEndpoint: serverURL + "/token",
			})
		case "/token":
			assert.NoError(t, r.ParseForm())
			if r.Form.Get("client_id") == "first" {
				close(firstExchange)
				select {
				case <-releaseExchange:
				case <-r.Context().Done():
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": r.Form.Get("client_id"), "token_type": "Bearer",
			})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL
	release := sync.OnceFunc(func() { close(releaseExchange) })
	defer release()
	opened := make(chan string, 2)
	newClient := func(name string) (*http.Client, func()) {
		client, cleanup, err := newOAuthClient(name, server.URL+"/mcp", oauthConfig{
			ClientID: name, Interactive: true, RedirectURI: redirectURI,
			AuthServerMetadataURL: server.URL + "/metadata", CallbackTimeout: "5s",
		})
		require.NoError(t, err, "construction must not bind the occupied callback port")
		t.Cleanup(cleanup)
		client.Timeout = 5 * time.Second
		transport := client.Transport.(*oauthTransport)
		transport.openBrowser = func(address string) error { opened <- address; return nil }
		return client, cleanup
	}
	first, _ := newClient("first")
	second, closeSecond := newClient("second")
	require.NoError(t, listener.Close())
	startLogin := func(client *http.Client) <-chan error {
		done := make(chan error, 1)
		go func() {
			response, err := client.Get(server.URL + "/mcp")
			if response != nil {
				response.Body.Close()
			}
			done <- err
		}()
		return done
	}
	nextAuthorization := func(name string) url.Values {
		t.Helper()
		var address string
		select {
		case address = <-opened:
		case <-time.After(5 * time.Second):
			t.Fatal("OAuth browser was not invoked")
		}
		authorization, err := url.Parse(address)
		require.NoError(t, err)
		assert.Equal(t, name, authorization.Query().Get("client_id"))
		assert.Equal(t, redirectURI, authorization.Query().Get("redirect_uri"))
		return authorization.Query()
	}
	firstDone := startLogin(first)
	authorization := nextAuthorization("first")
	callback := redirectURI + "?" + url.Values{
		"code": {"code"}, "state": {authorization.Get("state")}, "iss": {serverURL},
	}.Encode()
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(callback)
	require.NoError(t, err)
	response.Body.Close()
	select {
	case <-firstExchange:
	case <-time.After(5 * time.Second):
		t.Fatal("first token exchange did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/mcp", nil)
	require.NoError(t, err)
	_, err = second.Do(request)
	require.ErrorIs(t, err, context.DeadlineExceeded, "waiting must honor the enclosing deadline, not fail to bind the port")
	assert.Empty(t, opened, "the next browser must wait until token exchange finishes")
	secondDone := startLogin(second)
	release()
	require.NoError(t, <-firstDone)
	nextAuthorization("second") // The same fixed port is reusable after exchange.
	closeSecond()
	require.ErrorIs(t, <-secondDone, context.Canceled)
	listener, err = net.Listen("tcp", address)
	require.NoError(t, err, "cleanup must release the callback listener")
	listener.Close()
}

func TestOAuthOccupiedCallbackPreservesExistingRegistration(t *testing.T) {
	for _, mode := range []string{"explicit redirect", "configured client", "registration failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			redirectURI := "http://" + listener.Addr().String() + "/callback"
			var registrations atomic.Int32
			var serverURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/metadata":
					json.NewEncoder(w).Encode(oauthex.AuthServerMeta{
						Issuer: serverURL, AuthorizationEndpoint: serverURL + "/authorize",
						TokenEndpoint: serverURL + "/token", RegistrationEndpoint: serverURL + "/register",
					})
				case "/register":
					registrations.Add(1)
					http.Error(w, "registration unavailable", http.StatusServiceUnavailable)
				default:
					t.Errorf("unexpected OAuth request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			serverURL = server.URL
			config := oauthConfig{Interactive: "always", AuthServerMetadataURL: server.URL + "/metadata"}
			switch mode {
			case "explicit redirect":
				config.RedirectURI = redirectURI
			case "configured client":
				config.ClientID = "configured-client"
			}
			client, cleanup, err := newOAuthClient("occupied", server.URL, config)
			require.NoError(t, err)
			defer cleanup()
			transport := client.Transport.(*oauthTransport)
			registration := &oauthex.ClientRegistrationResponse{
				ClientID: "old-client", ClientSecret: "old-secret",
				ClientRegistrationMetadata: oauthex.ClientRegistrationMetadata{RedirectURIs: []string{redirectURI}},
			}
			transport.credentials.ClientInformation = registration
			require.NoError(t, transport.save())
			before, err := os.ReadFile(transport.credentialPath)
			require.NoError(t, err)
			err = transport.authorize(t.Context(), nil)
			if mode == "registration failure" {
				require.ErrorContains(t, err, "register MCP OAuth client")
				assert.EqualValues(t, 1, registrations.Load())
			} else {
				require.ErrorIs(t, err, syscall.EADDRINUSE)
				assert.Zero(t, registrations.Load(), "explicit settings must not be changed")
			}
			assert.Equal(t, registration, transport.credentials.ClientInformation)
			after, err := os.ReadFile(transport.credentialPath)
			require.NoError(t, err)
			assert.Equal(t, before, after, "failed replacement must not overwrite stored credentials")
		})
	}
}

func TestOAuthRejectsInsecureRegistrationEndpoint(t *testing.T) {
	metadata := &oauthex.AuthServerMeta{Issuer: "https://auth.example", AuthorizationEndpoint: "https://auth.example/authorize", TokenEndpoint: "https://auth.example/token", RegistrationEndpoint: "http://auth.example/register"}
	require.ErrorContains(t, validateOAuthMetadata(metadata), "HTTPS or HTTP loopback")
}
