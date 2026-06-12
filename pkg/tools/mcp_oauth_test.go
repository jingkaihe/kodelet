package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noopMCPOAuthPrompter struct{}

func (noopMCPOAuthPrompter) PromptMCPOAuth(context.Context, string, string) error {
	return nil
}

func TestDefaultMCPOAuthPrompterPrintsAuthorizationURL(t *testing.T) {
	isolateViper(t)
	viper.Set("mcp.oauth.open_browser", false)

	oldStderr := os.Stderr
	readEnd, writeEnd, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = writeEnd
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	promptErr := defaultMCPOAuthPrompter{}.PromptMCPOAuth(context.Background(), "demo", "https://auth.example/authorize")
	os.Stderr = oldStderr
	require.NoError(t, writeEnd.Close())

	output, err := io.ReadAll(readEnd)
	require.NoError(t, err)
	require.NoError(t, readEnd.Close())
	require.NoError(t, promptErr)

	assert.Contains(t, string(output), `MCP server "demo" requires OAuth authorization.`)
	assert.Contains(t, string(output), "https://auth.example/authorize")
}

func TestMCPOAuthPrompterSelectionAndInteractiveModes(t *testing.T) {
	isolateViper(t)

	prompter := &noopMCPOAuthPrompter{}
	ctx := WithMCPOAuthPrompter(context.Background(), prompter)

	assert.Same(t, prompter, currentMCPOAuthPrompter(ctx))
	assert.True(t, hasCustomMCPOAuthPrompter(ctx))
	assert.True(t, mcpOAuthInteractiveAllowed(ctx))

	viper.Set("mcp.oauth.interactive", "never")
	assert.False(t, mcpOAuthInteractiveAllowed(ctx))

	viper.Set("mcp.oauth.interactive", "always")
	assert.True(t, mcpOAuthInteractiveAllowed(context.Background()))

	restore := SetDefaultMCPOAuthPrompter(prompter)
	assert.True(t, hasCustomMCPOAuthPrompter(context.Background()))
	restore()
	assert.False(t, hasCustomMCPOAuthPrompter(context.Background()))
}

func TestMCPOAuthAuthorizationHeaderRefreshesExpiredToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var tokenRequests int
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 "https://issuer.example",
				"authorization_endpoint": "https://issuer.example/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case "/token":
			tokenRequests++
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
			assert.Equal(t, "stored-client", r.Form.Get("client_id"))
			assert.Equal(t, "stored-secret", r.Form.Get("client_secret"))
			assert.Equal(t, "old-refresh-token", r.Form.Get("refresh_token"))
			assert.Equal(t, "https://resource.example/mcp", r.Form.Get("resource"))

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "refreshed-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	metadataURL := authServer.URL + "/metadata"
	rt, err := newMCPOAuthRoundTripper("refresh-test", "https://mcp.example/mcp", MCPOAuthConfig{
		AuthServerMetadataURL: metadataURL,
	})
	require.NoError(t, err)

	require.NoError(t, rt.store.Save(context.Background(), &mcpOAuthStoredCredentials{
		Token: &transport.Token{
			AccessToken:  "expired-access-token",
			TokenType:    "Bearer",
			RefreshToken: "old-refresh-token",
			ExpiresAt:    time.Now().Add(-time.Hour),
		},
		ClientID:     "stored-client",
		ClientSecret: "stored-secret",
		Resource:     "https://resource.example/mcp",
	}))

	header, err := rt.authorizationHeader(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer refreshed-access-token", header)
	assert.Equal(t, 1, tokenRequests)

	creds, err := rt.store.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "refreshed-access-token", creds.Token.AccessToken)
	assert.Equal(t, "old-refresh-token", creds.Token.RefreshToken)
	assert.Equal(t, metadataURL, creds.AuthServerMetadataURL)
	assert.WithinDuration(t, time.Now().Add(time.Hour), creds.Token.ExpiresAt, 5*time.Second)
}

func TestMCPOAuthDiscoverAuthMetadata(t *testing.T) {
	t.Run("uses stored metadata URL", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/stored-metadata", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 "https://stored.example",
				"authorization_endpoint": "https://stored.example/authorize",
				"token_endpoint":         "https://stored.example/token",
			})
		}))
		defer authServer.Close()

		rt, err := newMCPOAuthRoundTripper("stored-discovery", "https://mcp.example/mcp", MCPOAuthConfig{})
		require.NoError(t, err)
		require.NoError(t, rt.store.Save(context.Background(), &mcpOAuthStoredCredentials{
			Token: &transport.Token{
				AccessToken: "cached-token",
				TokenType:   "Bearer",
				ExpiresAt:   time.Now().Add(time.Hour),
			},
			AuthServerMetadataURL: authServer.URL + "/stored-metadata",
		}))

		metadata, protectedResource, err := rt.discoverAuthMetadata(context.Background(), "")
		require.NoError(t, err)
		assert.Nil(t, protectedResource)
		assert.Equal(t, "https://stored.example", metadata.Issuer)
		assert.Equal(t, authServer.URL+"/stored-metadata", metadata.MetadataURL)
	})

	t.Run("falls back to server well-known metadata", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/oauth-protected-resource":
				http.Error(w, "no protected resource metadata", http.StatusNotFound)
			case "/.well-known/oauth-authorization-server":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer":                 "https://fallback.example",
					"authorization_endpoint": "https://fallback.example/authorize",
					"token_endpoint":         "https://fallback.example/token",
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer authServer.Close()

		rt, err := newMCPOAuthRoundTripper("fallback-discovery", authServer.URL+"/mcp", MCPOAuthConfig{})
		require.NoError(t, err)

		metadata, protectedResource, err := rt.discoverAuthMetadata(context.Background(), "")
		require.NoError(t, err)
		assert.Nil(t, protectedResource)
		assert.Equal(t, "https://fallback.example", metadata.Issuer)
		assert.Equal(t, authServer.URL+"/.well-known/oauth-authorization-server", metadata.MetadataURL)
	})
}

func TestMCPOAuthRegistrationAndTokenRequestErrors(t *testing.T) {
	_, err := registerMCPClient(context.Background(), &mcpAuthServerMetadata{}, "kodelet-test", "http://127.0.0.1/callback", nil)
	require.ErrorContains(t, err, "does not support dynamic client registration")

	t.Run("successful registration sends scopes", func(t *testing.T) {
		registrationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			var requestBody map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
			assert.Equal(t, "kodelet-test", requestBody["client_name"])
			assert.Equal(t, "read write", requestBody["scope"])
			assert.Equal(t, "none", requestBody["token_endpoint_auth_method"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":     "registered-client",
				"client_secret": "registered-secret",
			})
		}))
		defer registrationServer.Close()

		registration, err := registerMCPClient(context.Background(), &mcpAuthServerMetadata{
			RegistrationEndpoint: registrationServer.URL,
		}, "kodelet-test", "http://127.0.0.1/callback", []string{"read", "write"})
		require.NoError(t, err)
		assert.Equal(t, "registered-client", registration.ClientID)
		assert.Equal(t, "registered-secret", registration.ClientSecret)
	})

	t.Run("rejects registration response without client id", func(t *testing.T) {
		registrationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}))
		defer registrationServer.Close()

		_, err := registerMCPClient(context.Background(), &mcpAuthServerMetadata{
			RegistrationEndpoint: registrationServer.URL,
		}, "kodelet-test", "http://127.0.0.1/callback", nil)
		require.ErrorContains(t, err, "did not include client_id")
	})

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid grant", http.StatusBadRequest)
	}))
	defer tokenServer.Close()

	_, err = sendMCPTokenRequest(context.Background(), tokenServer.URL, url.Values{"grant_type": []string{"refresh_token"}})
	require.ErrorContains(t, err, "OAuth token request failed with status 400")
	require.ErrorContains(t, err, "invalid grant")
}

func TestMCPOAuthCallbackServer(t *testing.T) {
	t.Run("uses configured callback path", func(t *testing.T) {
		callback, err := startMCPOAuthCallbackServer("http://127.0.0.1:0/custom/callback")
		require.NoError(t, err)
		defer callback.Close()

		callbackURL := "http://" + callback.listener.Addr().String() + "/custom/callback?code=oauth-code&state=state-123&iss=https%3A%2F%2Fissuer.example"
		resp, err := http.Get(callbackURL)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(body), "Authorization complete")

		result, err := callback.Wait(context.Background(), time.Second)
		require.NoError(t, err)
		assert.Equal(t, "oauth-code", result.Code)
		assert.Equal(t, "state-123", result.State)
		assert.Equal(t, "https://issuer.example", result.Issuer)
	})

	t.Run("rejects non-http redirect URI", func(t *testing.T) {
		_, err := startMCPOAuthCallbackServer("https://127.0.0.1/callback")
		require.ErrorContains(t, err, "redirect_uri must be an http loopback URL")
	})

	t.Run("wait returns context errors", func(t *testing.T) {
		callback, err := startMCPOAuthCallbackServer("")
		require.NoError(t, err)
		defer callback.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = callback.Wait(ctx, time.Hour)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestMCPOAuthCredentialStoreValidation(t *testing.T) {
	store := &mcpOAuthCredentialStore{path: filepath.Join(t.TempDir(), "credentials.json")}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.Load(canceledCtx)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, store.Save(canceledCtx, &mcpOAuthStoredCredentials{}), context.Canceled)

	require.NoError(t, os.WriteFile(store.path, []byte(`{`), 0o600))
	_, err = store.Load(context.Background())
	require.ErrorContains(t, err, "failed to decode MCP OAuth credentials")

	require.NoError(t, os.WriteFile(store.path, []byte(`{}`), 0o600))
	_, err = store.Load(context.Background())
	require.ErrorIs(t, err, transport.ErrNoToken)
}

func TestMCPOAuthHelpers(t *testing.T) {
	t.Setenv("MCP_OAUTH_VALUE", "expanded-value")

	assert.Equal(t, "expanded-value", expandConfigValue("${MCP_OAUTH_VALUE}"))
	assert.Equal(t, "literal", expandConfigValue(" literal "))

	baseURL, err := oauthBaseURL("https://mcp.example/path?query=1")
	require.NoError(t, err)
	assert.Equal(t, "https://mcp.example", baseURL)

	_, err = oauthBaseURL("/relative")
	require.ErrorContains(t, err, "invalid OAuth base URL")

	challenge := parseMCPBearerChallenge([]string{"Basic realm=example", `Bearer realm="mcp", scope="read write"`})
	assert.True(t, challenge.bearer)
	assert.Equal(t, []string{"read", "write"}, challenge.scopes)
	assert.False(t, parseMCPBearerChallenge([]string{"Basic realm=example"}).bearer)

	filename := safeMCPServerTokenFilename(" My Server! ", "https://mcp.example")
	assert.True(t, strings.HasPrefix(filename, "my_server-"))
	assert.True(t, strings.HasSuffix(filename, ".json"))

	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": "https://issuer.example"})
	}))
	defer metadataServer.Close()

	_, err = fetchMCPAuthServerMetadata(context.Background(), metadataServer.URL)
	require.ErrorContains(t, err, "missing required endpoints")
}
