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
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestOAuthFlowPersistenceAndRefresh(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var registrations, authorizations, refreshes, challenges atomic.Int32
			var registeredRedirect, proof atomic.Value
			registeredRedirect.Store("")
			firstChallenges := make(chan struct{})
			var serverURL string
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
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					fmt.Fprintln(w, `{"ok":true}`)
				case "/resource":
					json.NewEncoder(w).Encode(oauthex.ProtectedResourceMetadata{Resource: serverURL + "/mcp", AuthorizationServers: []string{serverURL}})
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
					assert.Equal(t, serverURL+"/mcp", query.Get("resource"))
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
			cfg := oauthConfig{Interactive: "always", Scopes: []string{"mcp.read"}, CallbackTimeout: "5s"}
			if configured {
				cfg.ClientID, cfg.ClientSecret = "client", "secret"
				cfg.AuthServerMetadataURL = server.URL + "/custom-metadata"
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
			request(client)
			assert.EqualValues(t, 1, refreshes.Load())
			assert.EqualValues(t, 1, authorizations.Load())
			// Force a new authorization using the persisted client registration.
			transport.credentials.Tokens = nil
			require.NoError(t, transport.save())
			cfg.Interactive = "always"
			client, _ = newClient()
			request(client)
			assert.EqualValues(t, 2, authorizations.Load())
			if configured {
				assert.Zero(t, registrations.Load())
			} else {
				assert.EqualValues(t, 1, registrations.Load())
			}
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
	address := listener.Addr().String()
	defer listener.Close()
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metadata" {
			json.NewEncoder(w).Encode(oauthex.AuthServerMeta{Issuer: serverURL, AuthorizationEndpoint: serverURL + "/authorize", TokenEndpoint: serverURL + "/token"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	serverURL = server.URL
	client, cleanup, err := newOAuthClient("cancel", server.URL, oauthConfig{
		ClientID: "client", Interactive: true, RedirectURI: "http://" + address + "/callback",
		AuthServerMetadataURL: server.URL + "/metadata", CallbackTimeout: "5s",
	})
	require.NoError(t, err, "construction must not bind the callback port")
	defer cleanup()
	require.NoError(t, listener.Close())
	opened := make(chan struct{})
	client.Transport.(*oauthTransport).openBrowser = func(string) error { close(opened); return nil }
	done := make(chan error, 1)
	go func() {
		response, err := client.Get(server.URL)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("OAuth browser was not invoked")
	}
	cleanup()
	require.ErrorIs(t, <-done, context.Canceled)
	listener, err = net.Listen("tcp", address)
	require.NoError(t, err, "cleanup must release the callback listener")
	listener.Close()
}

func TestOAuthRejectsInsecureRegistrationEndpoint(t *testing.T) {
	metadata := &oauthex.AuthServerMeta{Issuer: "https://auth.example", AuthorizationEndpoint: "https://auth.example/authorize", TokenEndpoint: "https://auth.example/token", RegistrationEndpoint: "http://auth.example/register"}
	require.ErrorContains(t, validateOAuthMetadata(metadata), "HTTPS or HTTP loopback")
}
