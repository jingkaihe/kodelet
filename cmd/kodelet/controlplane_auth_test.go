package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type controlPlaneAuthRoundTripFunc func(*http.Request) (*http.Response, error)

func (f controlPlaneAuthRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestControlPlaneAuthCommandsRegisteredAndConfigured(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"auth"})
	require.NoError(t, err)
	assert.Same(t, authCmd, command)
	assert.True(t, authCmd.HasSubCommands())

	for _, test := range []struct {
		name      string
		command   *cobra.Command
		noBrowser bool
	}{
		{name: "login", command: authLoginCmd, noBrowser: true},
		{name: "logout", command: authLogoutCmd},
		{name: "status", command: authStatusCmd},
	} {
		t.Run(test.name, func(t *testing.T) {
			found, _, findErr := rootCmd.Find([]string{"auth", test.name})
			require.NoError(t, findErr)
			assert.Same(t, test.command, found)
			serverFlag := test.command.Flags().Lookup("server")
			require.NotNil(t, serverFlag)
			assert.Equal(t, defaultRunnerServer, serverFlag.DefValue)
			if test.noBrowser {
				noBrowserFlag := test.command.Flags().Lookup("no-browser")
				require.NotNil(t, noBrowserFlag)
				assert.Equal(t, "false", noBrowserFlag.DefValue)
			} else {
				assert.Nil(t, test.command.Flags().Lookup("no-browser"))
			}
		})
	}

	var output bytes.Buffer
	previousOutput := authCmd.OutOrStdout()
	authCmd.SetOut(&output)
	t.Cleanup(func() { authCmd.SetOut(previousOutput) })
	require.NoError(t, authCmd.RunE(authCmd, nil))
	assert.Contains(t, output.String(), "Usage:")
	assert.Contains(t, output.String(), "login")
	assert.Contains(t, output.String(), "logout")
	assert.Contains(t, output.String(), "status")
}

func TestControlPlaneAuthConfigsResolveServerPrecedence(t *testing.T) {
	newLoginCommand := func() *cobra.Command {
		cmd := &cobra.Command{Use: "login"}
		cmd.Flags().String("server", defaultRunnerServer, "")
		cmd.Flags().Bool("no-browser", false, "")
		return cmd
	}

	setControlPlaneAuthServerConfigForTest(t, " https://config.example/control ")
	t.Setenv(controlPlaneServerEnv, " https://environment.example/control ")

	environmentConfig := controlPlaneAuthLoginConfigFromFlags(newLoginCommand())
	assert.Equal(t, "https://environment.example/control", environmentConfig.Server)
	assert.False(t, environmentConfig.NoBrowser)

	flagCommand := newLoginCommand()
	require.NoError(t, flagCommand.Flags().Set("server", " https://flag.example/control "))
	require.NoError(t, flagCommand.Flags().Set("no-browser", "true"))
	flagConfig := controlPlaneAuthLoginConfigFromFlags(flagCommand)
	assert.Equal(t, "https://flag.example/control", flagConfig.Server)
	assert.True(t, flagConfig.NoBrowser)

	t.Setenv(controlPlaneServerEnv, "")
	assert.Equal(t, "https://config.example/control", controlPlaneAuthLogoutConfigFromFlags(newControlPlaneAuthServerCommand()).Server)
	assert.Equal(t, "https://config.example/control", controlPlaneAuthStatusConfigFromFlags(newControlPlaneAuthServerCommand()).Server)
}

func TestRunControlPlaneAuthLoginStartsBrowserFlowPersistsAndHidesSecrets(t *testing.T) {
	store, err := userauth.NewStoreAt(filepath.Join(t.TempDir(), "auth-state"))
	require.NoError(t, err)
	now := time.Now().UTC()
	bearer := controlPlaneAuthTestBearer(0x11)
	deviceCode := "private-device-code-login-start"
	verificationURL := "https://kodelet.example/auth/device"
	verificationURLComplete := verificationURL + "?user_code=ABCD-EFGH"
	principal := controlPlaneAuthTestPrincipal("principal-new", "new@example.com")
	credentialExpiry := now.Add(24 * time.Hour)
	var startCalls atomic.Int32
	var pollCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case userauth.DeviceStartPath:
			startCalls.Add(1)
			assert.Equal(t, http.MethodPost, request.Method)
			assert.Empty(t, request.Header.Get("Authorization"))
			var start userauth.DeviceStartRequest
			require.NoError(t, json.NewDecoder(request.Body).Decode(&start))
			require.NoError(t, start.Validate())
			writeControlPlaneAuthJSON(t, w, http.StatusCreated, userauth.DeviceStartResponse{
				AuthorizationID:         "authorization-login-start",
				DeviceCode:              deviceCode,
				UserCode:                "ABCD-EFGH",
				VerificationURL:         verificationURL,
				VerificationURLComplete: verificationURLComplete,
				BearerToken:             bearer,
				ExpiresAt:               now.Add(10 * time.Minute),
				PollIntervalMS:          0,
			})
		case userauth.DevicePollPath:
			pollCalls.Add(1)
			assert.Equal(t, http.MethodPost, request.Method)
			var poll userauth.DevicePollRequest
			require.NoError(t, json.NewDecoder(request.Body).Decode(&poll))
			assert.Equal(t, deviceCode, poll.DeviceCode)
			writeControlPlaneAuthJSON(t, w, http.StatusOK, userauth.DevicePollResponse{
				Status:       userauth.DeviceStatusApproved,
				CredentialID: "credential-new",
				Principal:    principal,
				ExpiresAt:    credentialExpiry,
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	var output bytes.Buffer
	var openedURL string
	var browserCalls atomic.Int32
	require.NoError(t, runControlPlaneAuthLogin(t.Context(), controlPlaneAuthLoginConfig{
		Server:     server.URL,
		Store:      store,
		HTTPClient: server.Client(),
		OpenBrowser: func(target string) error {
			browserCalls.Add(1)
			openedURL = target
			return fmt.Errorf("browser unavailable")
		},
	}, &output))

	assert.Equal(t, int32(1), startCalls.Load())
	assert.Equal(t, int32(1), pollCalls.Load())
	assert.Equal(t, int32(1), browserCalls.Load())
	assert.Equal(t, verificationURL, openedURL)
	rendered := output.String()
	assert.Contains(t, rendered, "Control-plane login started")
	assert.Contains(t, rendered, "Login code: ABCD-EFGH")
	assert.Contains(t, rendered, "Enter this code at: "+verificationURL)
	assert.Contains(t, rendered, "Could not open the browser automatically: browser unavailable")
	assert.Contains(t, rendered, "Open this URL manually to continue: "+verificationURL)
	assert.NotContains(t, rendered, verificationURLComplete)
	assert.Contains(t, rendered, "Control-plane login approved")
	assert.Contains(t, rendered, "Credential ID: credential-new")
	assert.Contains(t, rendered, "Principal: principal-new")
	assert.Contains(t, rendered, "Email: new@example.com")
	assert.Contains(t, rendered, "Roles: user, terminal")
	assert.Contains(t, rendered, formatControlPlaneAuthTime(credentialExpiry))
	assert.Contains(t, rendered, "Credentials directory: "+store.Root())
	assertControlPlaneAuthSecretsHidden(t, rendered, bearer, deviceCode)

	stored, found, err := store.LoadCredential(server.URL)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-new", stored.CredentialID)
	assert.Equal(t, bearer, stored.BearerToken)
	_, found, err = store.LoadPendingLogin(server.URL)
	require.NoError(t, err)
	assert.False(t, found)
	assertControlPlaneAuthStateSecure(t, store.Root())
}

func TestRunControlPlaneAuthLoginExistingValidCredentialIsNoOp(t *testing.T) {
	store, err := userauth.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	bearer := controlPlaneAuthTestBearer(0x21)
	storedPrincipal := controlPlaneAuthTestPrincipal("principal-stored", "stored@example.com")
	currentPrincipal := controlPlaneAuthTestPrincipal("principal-current", "current@example.com")
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		assert.Equal(t, http.MethodGet, request.Method)
		assert.Equal(t, userauth.MePath, request.URL.Path)
		assert.Equal(t, "Bearer "+bearer, request.Header.Get("Authorization"))
		writeControlPlaneAuthJSON(t, w, http.StatusOK, currentPrincipal)
	}))
	t.Cleanup(server.Close)

	expiresAt := time.Now().UTC().Add(time.Hour)
	require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(server.URL, "credential-existing", bearer, storedPrincipal, expiresAt)))
	var browserCalls atomic.Int32
	var output bytes.Buffer
	require.NoError(t, runControlPlaneAuthLogin(t.Context(), controlPlaneAuthLoginConfig{
		Server:     server.URL,
		Store:      store,
		HTTPClient: server.Client(),
		OpenBrowser: func(string) error {
			browserCalls.Add(1)
			return nil
		},
	}, &output))

	assert.Equal(t, int32(1), requests.Load())
	assert.Zero(t, browserCalls.Load())
	rendered := output.String()
	assert.Contains(t, rendered, "Already logged in to "+server.URL)
	assert.Contains(t, rendered, "Principal: principal-current")
	assert.Contains(t, rendered, "Email: current@example.com")
	assert.NotContains(t, rendered, "Control-plane login started")
	assertControlPlaneAuthSecretsHidden(t, rendered, bearer)
	stored, found, err := store.LoadCredential(server.URL)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-existing", stored.CredentialID)
	assert.Equal(t, storedPrincipal, stored.Principal)
}

func TestRunControlPlaneAuthLoginReplacesCredentialRejectedWith401(t *testing.T) {
	store, err := userauth.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	oldBearer := controlPlaneAuthTestBearer(0x31)
	newBearer := controlPlaneAuthTestBearer(0x32)
	deviceCode := "private-device-code-replacement"
	now := time.Now().UTC()
	var validateCalls atomic.Int32
	var startCalls atomic.Int32
	var pollCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case userauth.MePath:
			validateCalls.Add(1)
			assert.Equal(t, "Bearer "+oldBearer, request.Header.Get("Authorization"))
			writeControlPlaneAuthJSON(t, w, http.StatusUnauthorized, map[string]string{"error": "invalid bearer " + oldBearer})
		case userauth.DeviceStartPath:
			startCalls.Add(1)
			writeControlPlaneAuthJSON(t, w, http.StatusCreated, userauth.DeviceStartResponse{
				AuthorizationID: "authorization-replacement",
				DeviceCode:      deviceCode,
				UserCode:        "REPL-ACE1",
				VerificationURL: "https://kodelet.example/auth/device",
				BearerToken:     newBearer,
				ExpiresAt:       now.Add(10 * time.Minute),
				PollIntervalMS:  0,
			})
		case userauth.DevicePollPath:
			pollCalls.Add(1)
			writeControlPlaneAuthJSON(t, w, http.StatusOK, userauth.DevicePollResponse{
				Status:       userauth.DeviceStatusApproved,
				CredentialID: "credential-replacement",
				Principal:    controlPlaneAuthTestPrincipal("principal-replacement", "replacement@example.com"),
				ExpiresAt:    now.Add(24 * time.Hour),
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
		server.URL,
		"credential-stale",
		oldBearer,
		controlPlaneAuthTestPrincipal("principal-stale", "stale@example.com"),
		now.Add(time.Hour),
	)))
	var output bytes.Buffer
	require.NoError(t, runControlPlaneAuthLogin(t.Context(), controlPlaneAuthLoginConfig{
		Server:      server.URL,
		Store:       store,
		HTTPClient:  server.Client(),
		OpenBrowser: func(string) error { return nil },
	}, &output))

	assert.Equal(t, int32(1), validateCalls.Load())
	assert.Equal(t, int32(1), startCalls.Load())
	assert.Equal(t, int32(1), pollCalls.Load())
	rendered := output.String()
	assert.Contains(t, rendered, "invalid or revoked; starting a new login")
	assert.Contains(t, rendered, "Credential ID: credential-replacement")
	assertControlPlaneAuthSecretsHidden(t, rendered, oldBearer, newBearer, deviceCode)
	stored, found, err := store.LoadCredential(server.URL)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "credential-replacement", stored.CredentialID)
	assert.Equal(t, newBearer, stored.BearerToken)
}

func TestRunControlPlaneAuthLoginNetworkFailurePreservesExistingCredential(t *testing.T) {
	store, err := userauth.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	server := "http://localhost:18181"
	bearer := controlPlaneAuthTestBearer(0x41)
	credential := controlPlaneAuthTestCredential(
		server,
		"credential-network",
		bearer,
		controlPlaneAuthTestPrincipal("principal-network", "network@example.com"),
		time.Now().UTC().Add(time.Hour),
	)
	require.NoError(t, store.SaveCredential(credential))
	httpClient := &http.Client{Transport: controlPlaneAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("transport exposed %s", bearer)
	})}

	var output bytes.Buffer
	err = runControlPlaneAuthLogin(t.Context(), controlPlaneAuthLoginConfig{
		Server:     server,
		Store:      store,
		HTTPClient: httpClient,
	}, &output)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to validate existing control-plane credential")
	assert.NotContains(t, err.Error(), bearer)
	assertControlPlaneAuthSecretsHidden(t, output.String(), bearer)
	stored, found, loadErr := store.LoadCredential(server)
	require.NoError(t, loadErr)
	require.True(t, found)
	assert.Equal(t, credential.CredentialID, stored.CredentialID)
	assert.Equal(t, bearer, stored.BearerToken)
}

func TestRunControlPlaneAuthLoginNoBrowserResumesAndPrintsManualInstructions(t *testing.T) {
	store, err := userauth.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	now := time.Now().UTC()
	bearer := controlPlaneAuthTestBearer(0x51)
	deviceCode := "private-device-code-resumed"
	verificationURL := "https://kodelet.example/auth/device"
	verificationURLComplete := verificationURL + "?user_code=RSUM-0001"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		assert.Equal(t, userauth.DevicePollPath, request.URL.Path)
		writeControlPlaneAuthJSON(t, w, http.StatusOK, userauth.DevicePollResponse{
			Status:       userauth.DeviceStatusApproved,
			CredentialID: "credential-resumed",
			Principal:    controlPlaneAuthTestPrincipal("principal-resumed", "resumed@example.com"),
			ExpiresAt:    now.Add(time.Hour),
		})
	}))
	t.Cleanup(server.Close)
	require.NoError(t, store.SavePendingLogin(userauth.PendingLogin{
		Server:                  server.URL,
		AuthorizationID:         "authorization-resumed",
		DeviceCode:              deviceCode,
		UserCode:                "RSUM-0001",
		VerificationURL:         "https://kodelet.example/auth/device",
		VerificationURLComplete: verificationURLComplete,
		BearerToken:             bearer,
		ExpiresAt:               now.Add(10 * time.Minute),
		PollIntervalMS:          0,
		CreatedAt:               now.Add(-time.Minute),
	}))

	var browserCalls atomic.Int32
	var output bytes.Buffer
	require.NoError(t, runControlPlaneAuthLogin(t.Context(), controlPlaneAuthLoginConfig{
		Server:      server.URL,
		NoBrowser:   true,
		Store:       store,
		HTTPClient:  server.Client(),
		OpenBrowser: func(string) error { browserCalls.Add(1); return nil },
	}, &output))

	assert.Equal(t, int32(1), requests.Load())
	assert.Zero(t, browserCalls.Load())
	rendered := output.String()
	assert.Contains(t, rendered, "Resuming pending control-plane login")
	assert.Contains(t, rendered, "Login code: RSUM-0001")
	assert.Contains(t, rendered, "Open this URL manually to continue: "+verificationURL)
	assert.NotContains(t, rendered, verificationURLComplete)
	assertControlPlaneAuthSecretsHidden(t, rendered, bearer, deviceCode)
}

func TestRunControlPlaneAuthLogoutCredentialLifecycle(t *testing.T) {
	t.Run("success deletes matching local credential", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		bearer := controlPlaneAuthTestBearer(0x61)
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			assert.Equal(t, http.MethodDelete, request.Method)
			assert.Equal(t, userauth.CurrentCredentialPath, request.URL.Path)
			assert.Equal(t, "Bearer "+bearer, request.Header.Get("Authorization"))
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(server.Close)
		require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
			server.URL,
			"credential-logout",
			bearer,
			controlPlaneAuthTestPrincipal("principal-logout", "logout@example.com"),
			time.Now().UTC().Add(time.Hour),
		)))

		var output bytes.Buffer
		require.NoError(t, runControlPlaneAuthLogout(t.Context(), controlPlaneAuthLogoutConfig{
			Server: server.URL, Store: store, HTTPClient: server.Client(),
		}, &output))
		assert.Equal(t, int32(1), calls.Load())
		assert.Contains(t, output.String(), "Logged out from "+server.URL)
		assertControlPlaneAuthSecretsHidden(t, output.String(), bearer)
		_, found, loadErr := store.LoadCredential(server.URL)
		require.NoError(t, loadErr)
		assert.False(t, found)
	})

	t.Run("unauthorized remote credential still clears matching local state", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		bearer := controlPlaneAuthTestBearer(0x62)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			assert.Equal(t, "Bearer "+bearer, request.Header.Get("Authorization"))
			writeControlPlaneAuthJSON(t, w, http.StatusUnauthorized, map[string]string{"error": "revoked " + bearer})
		}))
		t.Cleanup(server.Close)
		require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
			server.URL,
			"credential-revoked",
			bearer,
			controlPlaneAuthTestPrincipal("principal-revoked", "revoked@example.com"),
			time.Now().UTC().Add(time.Hour),
		)))

		var output bytes.Buffer
		require.NoError(t, runControlPlaneAuthLogout(t.Context(), controlPlaneAuthLogoutConfig{
			Server: server.URL, Store: store, HTTPClient: server.Client(),
		}, &output))
		assert.Contains(t, output.String(), "Logged out from "+server.URL)
		assertControlPlaneAuthSecretsHidden(t, output.String(), bearer)
		_, found, loadErr := store.LoadCredential(server.URL)
		require.NoError(t, loadErr)
		assert.False(t, found)
	})

	t.Run("network failure preserves local credential", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		server := "http://localhost:28282"
		bearer := controlPlaneAuthTestBearer(0x63)
		credential := controlPlaneAuthTestCredential(
			server,
			"credential-preserved",
			bearer,
			controlPlaneAuthTestPrincipal("principal-preserved", "preserved@example.com"),
			time.Now().UTC().Add(time.Hour),
		)
		require.NoError(t, store.SaveCredential(credential))
		httpClient := &http.Client{Transport: controlPlaneAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("transport exposed %s", bearer)
		})}

		var output bytes.Buffer
		err = runControlPlaneAuthLogout(t.Context(), controlPlaneAuthLogoutConfig{
			Server: server, Store: store, HTTPClient: httpClient,
		}, &output)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to revoke control-plane credential")
		assert.NotContains(t, err.Error(), bearer)
		stored, found, loadErr := store.LoadCredential(server)
		require.NoError(t, loadErr)
		require.True(t, found)
		assert.Equal(t, credential.CredentialID, stored.CredentialID)
		assert.Equal(t, bearer, stored.BearerToken)
	})

	t.Run("no local credential is already logged out", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		var output bytes.Buffer
		require.NoError(t, runControlPlaneAuthLogout(t.Context(), controlPlaneAuthLogoutConfig{
			Server: "http://localhost:38383", Store: store,
		}, &output))
		assert.Contains(t, output.String(), "Already logged out")
	})
}

func TestRunControlPlaneAuthStatus(t *testing.T) {
	t.Run("valid credential reports current principal", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		bearer := controlPlaneAuthTestBearer(0x71)
		currentPrincipal := controlPlaneAuthTestPrincipal("principal-current-status", "current-status@example.com")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			assert.Equal(t, "/base"+userauth.MePath, request.URL.Path)
			assert.Equal(t, "Bearer "+bearer, request.Header.Get("Authorization"))
			writeControlPlaneAuthJSON(t, w, http.StatusOK, currentPrincipal)
		}))
		t.Cleanup(server.Close)
		rawServer := server.URL + "/base/./"
		canonicalServer := server.URL + "/base"
		expiresAt := time.Now().UTC().Add(time.Hour)
		require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
			rawServer,
			"credential-status-valid",
			bearer,
			controlPlaneAuthTestPrincipal("principal-stored-status", "stored-status@example.com"),
			expiresAt,
		)))

		var output bytes.Buffer
		require.NoError(t, runControlPlaneAuthStatus(t.Context(), controlPlaneAuthStatusConfig{
			Server: rawServer, Store: store, HTTPClient: server.Client(),
		}, &output))
		rendered := output.String()
		assert.Contains(t, rendered, "Server: "+canonicalServer)
		assert.Contains(t, rendered, "Credential ID: credential-status-valid")
		assert.Contains(t, rendered, "Credential status: valid")
		assert.Contains(t, rendered, "Principal: principal-current-status")
		assert.Contains(t, rendered, "Email: current-status@example.com")
		assert.Contains(t, rendered, "Roles: user, terminal")
		assert.Contains(t, rendered, formatControlPlaneAuthTime(expiresAt))
		assert.NotContains(t, rendered, "principal-stored-status")
		assertControlPlaneAuthSecretsHidden(t, rendered, bearer)
	})

	t.Run("expired credential is reported without a request", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			http.Error(w, "expired credentials must not be sent", http.StatusInternalServerError)
		}))
		t.Cleanup(server.Close)
		bearer := controlPlaneAuthTestBearer(0x72)
		expiresAt := time.Now().UTC().Add(-time.Hour)
		require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
			server.URL,
			"credential-status-expired",
			bearer,
			controlPlaneAuthTestPrincipal("principal-expired", "expired@example.com"),
			expiresAt,
		)))

		var output bytes.Buffer
		require.NoError(t, runControlPlaneAuthStatus(t.Context(), controlPlaneAuthStatusConfig{
			Server: server.URL, Store: store, HTTPClient: server.Client(),
		}, &output))
		assert.Zero(t, calls.Load())
		rendered := output.String()
		assert.Contains(t, rendered, "Credential status: expired")
		assert.Contains(t, rendered, "Credential ID: credential-status-expired")
		assert.Contains(t, rendered, "Principal: principal-expired")
		assert.Contains(t, rendered, "Email: expired@example.com")
		assert.Contains(t, rendered, formatControlPlaneAuthTime(expiresAt))
		assertControlPlaneAuthSecretsHidden(t, rendered, bearer)
		_, found, loadErr := store.LoadCredential(server.URL)
		require.NoError(t, loadErr)
		assert.True(t, found)
	})

	t.Run("pending login reports only display-safe metadata", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		now := time.Now().UTC()
		server := "http://localhost:48484/base/./"
		canonicalServer := "http://localhost:48484/base"
		bearer := controlPlaneAuthTestBearer(0x73)
		deviceCode := "private-device-code-status"
		verificationURL := "https://kodelet.example/auth/device"
		verificationURLComplete := verificationURL + "?user_code=PEND-0001"
		expiresAt := now.Add(10 * time.Minute)
		require.NoError(t, store.SavePendingLogin(userauth.PendingLogin{
			Server:                  server,
			AuthorizationID:         "authorization-status-pending",
			DeviceCode:              deviceCode,
			UserCode:                "PEND-0001",
			VerificationURL:         verificationURL,
			VerificationURLComplete: verificationURLComplete,
			BearerToken:             bearer,
			ExpiresAt:               expiresAt,
			PollIntervalMS:          1000,
			CreatedAt:               now.Add(-time.Minute),
		}))

		var output bytes.Buffer
		require.NoError(t, runControlPlaneAuthStatus(t.Context(), controlPlaneAuthStatusConfig{
			Server: server, Store: store,
		}, &output))
		rendered := output.String()
		assert.Contains(t, rendered, "Server: "+canonicalServer)
		assert.Contains(t, rendered, "Credential status: logged out")
		assert.Contains(t, rendered, "Pending login status: pending")
		assert.Contains(t, rendered, "Pending login code: PEND-0001")
		assert.Contains(t, rendered, "Pending verification URL: "+verificationURL)
		assert.NotContains(t, rendered, verificationURLComplete)
		assert.Contains(t, rendered, formatControlPlaneAuthTime(expiresAt))
		assertControlPlaneAuthSecretsHidden(t, rendered, bearer, deviceCode, "authorization-status-pending")
	})

	t.Run("no state is a successful logged-out status", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		var output bytes.Buffer
		require.NoError(t, runControlPlaneAuthStatus(t.Context(), controlPlaneAuthStatusConfig{
			Server: "http://LOCALHOST:80/base/./", Store: store,
		}, &output))
		assert.Equal(t, "Server: http://localhost/base\nCredential status: logged out\n", output.String())
	})

	t.Run("unauthorized status preserves local credential", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		bearer := controlPlaneAuthTestBearer(0x74)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeControlPlaneAuthJSON(t, w, http.StatusUnauthorized, map[string]string{"error": "invalid " + bearer})
		}))
		t.Cleanup(server.Close)
		require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
			server.URL,
			"credential-status-revoked",
			bearer,
			controlPlaneAuthTestPrincipal("principal-status-revoked", "status-revoked@example.com"),
			time.Now().UTC().Add(time.Hour),
		)))

		var output bytes.Buffer
		require.NoError(t, runControlPlaneAuthStatus(t.Context(), controlPlaneAuthStatusConfig{
			Server: server.URL, Store: store, HTTPClient: server.Client(),
		}, &output))
		assert.Contains(t, output.String(), "Credential status: invalid or revoked")
		assertControlPlaneAuthSecretsHidden(t, output.String(), bearer)
		_, found, loadErr := store.LoadCredential(server.URL)
		require.NoError(t, loadErr)
		assert.True(t, found)
	})

	t.Run("network failure returns an error and preserves local credential", func(t *testing.T) {
		store, err := userauth.NewStoreAt(t.TempDir())
		require.NoError(t, err)
		server := "http://localhost:58585"
		bearer := controlPlaneAuthTestBearer(0x75)
		require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
			server,
			"credential-status-network",
			bearer,
			controlPlaneAuthTestPrincipal("principal-status-network", "status-network@example.com"),
			time.Now().UTC().Add(time.Hour),
		)))
		httpClient := &http.Client{Transport: controlPlaneAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("transport exposed %s", bearer)
		})}

		var output bytes.Buffer
		err = runControlPlaneAuthStatus(t.Context(), controlPlaneAuthStatusConfig{
			Server: server, Store: store, HTTPClient: httpClient,
		}, &output)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to validate control-plane credential")
		assert.NotContains(t, err.Error(), bearer)
		_, found, loadErr := store.LoadCredential(server)
		require.NoError(t, loadErr)
		assert.True(t, found)
	})
}

func TestResolveControlPlaneAuthTokenPrecedenceAndCanonicalLookup(t *testing.T) {
	t.Run("explicit flag overrides environment and stored credential", func(t *testing.T) {
		basePath := t.TempDir()
		t.Setenv("KODELET_BASE_PATH", basePath)
		t.Setenv(controlPlaneAuthTokenEnv, "environment-token")
		cmd := newControlPlaneAuthTokenCommand()
		require.NoError(t, cmd.Flags().Set("auth-token", " flag-token "))

		token, source, err := resolveControlPlaneAuthToken(cmd, "not a valid server")
		require.NoError(t, err)
		assert.Equal(t, "flag-token", token)
		assert.Equal(t, "flag", source)
	})

	t.Run("explicit empty flag does not fall through", func(t *testing.T) {
		basePath := t.TempDir()
		t.Setenv("KODELET_BASE_PATH", basePath)
		t.Setenv(controlPlaneAuthTokenEnv, "environment-token")
		cmd := newControlPlaneAuthTokenCommand()
		require.NoError(t, cmd.Flags().Set("auth-token", ""))

		token, source, err := resolveControlPlaneAuthToken(cmd, "not a valid server")
		require.NoError(t, err)
		assert.Empty(t, token)
		assert.Equal(t, "flag", source)
	})

	t.Run("trimmed non-empty environment overrides stored credential", func(t *testing.T) {
		t.Setenv("KODELET_BASE_PATH", t.TempDir())
		t.Setenv(controlPlaneAuthTokenEnv, " environment-token ")

		token, source, err := resolveControlPlaneAuthToken(newControlPlaneAuthTokenCommand(), "not a valid server")
		require.NoError(t, err)
		assert.Equal(t, "environment-token", token)
		assert.Equal(t, "environment", source)
	})

	t.Run("whitespace environment falls through to canonical stored credential", func(t *testing.T) {
		t.Setenv("KODELET_BASE_PATH", t.TempDir())
		t.Setenv(controlPlaneAuthTokenEnv, "   ")
		store, err := userauth.NewStore()
		require.NoError(t, err)
		rawServer := "http://LOCALHOST:80/base/./"
		canonicalServer := "http://localhost/base"
		bearer := controlPlaneAuthTestBearer(0x81)
		require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
			rawServer,
			"credential-token-stored",
			bearer,
			controlPlaneAuthTestPrincipal("principal-token-stored", "token-stored@example.com"),
			time.Now().UTC().Add(time.Hour),
		)))

		token, source, err := resolveControlPlaneAuthToken(newControlPlaneAuthTokenCommand(), canonicalServer+"/")
		require.NoError(t, err)
		assert.Equal(t, bearer, token)
		assert.Equal(t, "stored", source)
	})

	t.Run("no configured token reports none", func(t *testing.T) {
		t.Setenv("KODELET_BASE_PATH", t.TempDir())
		t.Setenv(controlPlaneAuthTokenEnv, "")

		token, source, err := resolveControlPlaneAuthToken(newControlPlaneAuthTokenCommand(), "http://localhost:60606")
		require.NoError(t, err)
		assert.Empty(t, token)
		assert.Equal(t, "none", source)
	})

	t.Run("expired stored credential returns actionable login error", func(t *testing.T) {
		t.Setenv("KODELET_BASE_PATH", t.TempDir())
		t.Setenv(controlPlaneAuthTokenEnv, "")
		store, err := userauth.NewStore()
		require.NoError(t, err)
		server := "http://localhost:61616/base"
		bearer := controlPlaneAuthTestBearer(0x82)
		require.NoError(t, store.SaveCredential(controlPlaneAuthTestCredential(
			server,
			"credential-token-expired",
			bearer,
			controlPlaneAuthTestPrincipal("principal-token-expired", "token-expired@example.com"),
			time.Now().UTC().Add(-time.Hour),
		)))

		token, source, err := resolveControlPlaneAuthToken(newControlPlaneAuthTokenCommand(), server+"/./")
		require.Error(t, err)
		assert.Empty(t, token)
		assert.Equal(t, "stored", source)
		assert.Contains(t, err.Error(), "stored control-plane credential")
		assert.Contains(t, err.Error(), "kodelet auth login --server "+server)
		assert.NotContains(t, err.Error(), bearer)
	})
}

func newControlPlaneAuthServerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth-test"}
	cmd.Flags().String("server", defaultRunnerServer, "")
	return cmd
}

func newControlPlaneAuthTokenCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth-token-test"}
	cmd.Flags().String("auth-token", "", "")
	return cmd
}

func setControlPlaneAuthServerConfigForTest(t *testing.T, value string) {
	t.Helper()
	previous := viper.Get("server")
	wasSet := viper.IsSet("server")
	viper.Set("server", value)
	t.Cleanup(func() {
		if wasSet {
			viper.Set("server", previous)
			return
		}
		viper.Set("server", nil)
	})
}

func controlPlaneAuthTestBearer(discriminator byte) string {
	payload := bytes.Repeat([]byte{discriminator}, 32)
	return userauth.BearerTokenPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

func controlPlaneAuthTestPrincipal(id, email string) userauth.PrincipalSnapshot {
	return userauth.PrincipalSnapshot{
		ID:    id,
		Name:  "Test User",
		Email: email,
		Roles: []string{"user", "terminal"},
	}
}

func controlPlaneAuthTestCredential(server, credentialID, bearer string, principal userauth.PrincipalSnapshot, expiresAt time.Time) userauth.Credential {
	createdAt := expiresAt.Add(-time.Hour)
	if expiresAt.After(time.Now().UTC()) {
		createdAt = time.Now().UTC().Add(-time.Hour)
	}
	return userauth.Credential{
		Server:       server,
		CredentialID: credentialID,
		BearerToken:  bearer,
		Principal:    principal,
		CreatedAt:    createdAt,
		ExpiresAt:    expiresAt,
	}
}

func writeControlPlaneAuthJSON(t *testing.T, writer http.ResponseWriter, statusCode int, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	require.NoError(t, json.NewEncoder(writer).Encode(value))
}

func assertControlPlaneAuthSecretsHidden(t *testing.T, rendered string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		assert.NotContains(t, rendered, secret)
	}
}

func assertControlPlaneAuthStateSecure(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), path)
			return nil
		}
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), path)
		return nil
	}))
}
