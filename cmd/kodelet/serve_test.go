package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateServeConfig(t *testing.T) {
	tests := []struct {
		name          string
		config        *ServeConfig
		expectedError string
	}{
		{
			name: "valid config",
			config: &ServeConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
			},
		},
		{
			name: "valid IP address",
			config: &ServeConfig{
				Host:         "127.0.0.1",
				Port:         8080,
				CompactRatio: 0.8,
			},
		},
		{
			name: "valid 0.0.0.0",
			config: &ServeConfig{
				Host:         "0.0.0.0",
				Port:         3000,
				CompactRatio: 0.8,
			},
		},
		{
			name: "empty host",
			config: &ServeConfig{
				Host: "",
				Port: 8080,
			},
			expectedError: "host cannot be empty",
		},
		{
			name: "invalid host with space",
			config: &ServeConfig{
				Host: "local host",
				Port: 8080,
			},
			expectedError: "invalid host: local host",
		},
		{
			name: "invalid host with colon",
			config: &ServeConfig{
				Host: "localhost:8080",
				Port: 8080,
			},
			expectedError: "invalid host: localhost:8080",
		},
		{
			name: "port too low",
			config: &ServeConfig{
				Host: "localhost",
				Port: 0,
			},
			expectedError: "port must be between 1 and 65535",
		},
		{
			name: "port too high",
			config: &ServeConfig{
				Host: "localhost",
				Port: 65536,
			},
			expectedError: "port must be between 1 and 65535",
		},
		{
			name: "privileged port warning",
			config: &ServeConfig{
				Host:         "localhost",
				Port:         80,
				CompactRatio: 0.8,
			},
			// No error expected, just a warning logged
		},
		{
			name: "invalid compact ratio",
			config: &ServeConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 1.5,
			},
			expectedError: "compact-ratio must be greater than 0.0 and less than or equal to 1.0",
		},
		{
			name: "zero compact ratio",
			config: &ServeConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0,
			},
			expectedError: "compact-ratio must be greater than 0.0 and less than or equal to 1.0",
		},
		{
			name: "auth token conflicts with skip auth",
			config: &ServeConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
				AuthToken:    "secret",
				SkipAuth:     true,
			},
			expectedError: "web auth token cannot be used when authentication is disabled",
		},
		{
			name: "runner auth token conflicts with skip auth",
			config: &ServeConfig{
				Host:            "localhost",
				Port:            8080,
				CompactRatio:    0.8,
				RunnerAuthToken: "runner-secret",
				SkipAuth:        true,
			},
			expectedError: "runner auth token cannot be used when authentication is disabled",
		},
		{
			name: "runner and web auth tokens must differ",
			config: &ServeConfig{
				Host:            "localhost",
				Port:            8080,
				CompactRatio:    0.8,
				AuthToken:       "same-secret",
				RunnerAuthToken: "same-secret",
			},
			expectedError: "runner auth token must differ from web auth token",
		},
		{
			name: "invalid runner auth token",
			config: &ServeConfig{
				Host:            "localhost",
				Port:            8080,
				CompactRatio:    0.8,
				RunnerAuthToken: "runner token",
			},
			expectedError: "invalid runner auth token",
		},
		{
			name: "whitespace auth token",
			config: &ServeConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
				AuthToken:    "   ",
			},
			expectedError: "auth-token cannot be empty",
		},
		{
			name: "auth token with cookie-unsafe characters",
			config: &ServeConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
				AuthToken:    "secret;token",
			},
			expectedError: "auth-token can only contain letters, numbers, and URL-safe punctuation",
		},
		{
			name: "invalid cors origin",
			config: &ServeConfig{
				Host:         "localhost",
				Port:         8080,
				CompactRatio: 0.8,
				CORSOrigins:  []string{"https://example.com/app"},
			},
			expectedError: "invalid cors-origin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServeConfig(tt.config)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestServeBaseURL(t *testing.T) {
	assert.Equal(t, "http://localhost:8080", serveBaseURL("localhost", 8080))
	assert.Equal(t, "http://localhost:8080", serveBaseURL("0.0.0.0", 8080))
	assert.Equal(t, "http://[::1]:8080", serveBaseURL("::1", 8080))
}

func TestServeURLWithToken(t *testing.T) {
	assert.Equal(t, "http://localhost:8080?token=abc123", serveURLWithToken("http://localhost:8080", "abc123"))
}

func TestResolveServeAuthModes(t *testing.T) {
	tests := []struct {
		name               string
		config             *ServeConfig
		expectedWebMode    webui.WebAuthMode
		expectedRunnerMode webui.RunnerAuthMode
		expectedError      string
	}{
		{
			name:               "legacy defaults",
			config:             NewServeConfig(),
			expectedWebMode:    webui.WebAuthModeToken,
			expectedRunnerMode: webui.RunnerAuthModeToken,
		},
		{
			name: "explicit modes are normalized",
			config: &ServeConfig{
				WebAuthMode:    " OIDC ",
				RunnerAuthMode: " Hybrid ",
			},
			expectedWebMode:    webui.WebAuthModeOIDC,
			expectedRunnerMode: webui.RunnerAuthModeHybrid,
		},
		{
			name: "skip auth resolves both modes to none",
			config: &ServeConfig{
				SkipAuth: true,
			},
			expectedWebMode:    webui.WebAuthModeNone,
			expectedRunnerMode: webui.RunnerAuthModeNone,
		},
		{
			name: "skip auth permits explicit none modes",
			config: &ServeConfig{
				WebAuthMode:    webui.WebAuthModeNone,
				RunnerAuthMode: webui.RunnerAuthModeNone,
				SkipAuth:       true,
			},
			expectedWebMode:    webui.WebAuthModeNone,
			expectedRunnerMode: webui.RunnerAuthModeNone,
		},
		{
			name: "skip auth conflicts with web mode",
			config: &ServeConfig{
				WebAuthMode: webui.WebAuthModeOIDC,
				SkipAuth:    true,
			},
			expectedError: "disabled authentication conflicts with a non-none web authentication mode",
		},
		{
			name: "skip auth conflicts with runner mode",
			config: &ServeConfig{
				RunnerAuthMode: webui.RunnerAuthModeHybrid,
				SkipAuth:       true,
			},
			expectedError: "disabled authentication conflicts with a non-none runner authentication mode",
		},
		{
			name: "invalid web mode",
			config: &ServeConfig{
				WebAuthMode: "password",
			},
			expectedError: "invalid web authentication mode",
		},
		{
			name: "invalid runner mode",
			config: &ServeConfig{
				RunnerAuthMode: "password",
			},
			expectedError: "invalid runner authentication mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webMode, runnerMode, err := resolveServeAuthModes(tt.config)
			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedWebMode, webMode)
			assert.Equal(t, tt.expectedRunnerMode, runnerMode)
		})
	}
}

func TestValidateServeAuthConfig(t *testing.T) {
	t.Run("OIDC configuration is valid", func(t *testing.T) {
		config := newValidOIDCServeConfig(t)
		config.AuthToken = "compat-admin-token"

		assert.NoError(t, validateServeConfig(config))
	})

	tests := []struct {
		name          string
		configure     func(*ServeConfig)
		expectedError string
	}{
		{
			name: "web token conflicts with none mode",
			configure: func(config *ServeConfig) {
				config.WebAuthMode = webui.WebAuthModeNone
				config.AuthToken = "web-token"
			},
			expectedError: "web auth token cannot be used when web authentication mode is none",
		},
		{
			name: "runner token conflicts with none mode",
			configure: func(config *ServeConfig) {
				config.RunnerAuthMode = webui.RunnerAuthModeNone
				config.RunnerAuthToken = "runner-token"
			},
			expectedError: "runner auth token cannot be used when runner authentication mode is none",
		},
		{
			name: "runner token requires token accepting mode",
			configure: func(config *ServeConfig) {
				config.RunnerAuthMode = webui.RunnerAuthModeEnrollment
				config.RunnerAuthToken = "runner-token"
			},
			expectedError: "runner auth token requires runner authentication mode token or hybrid",
		},
		{
			name: "enrollment requires web authentication",
			configure: func(config *ServeConfig) {
				config.WebAuthMode = webui.WebAuthModeNone
				config.RunnerAuthMode = webui.RunnerAuthModeEnrollment
			},
			expectedError: "runner enrollment requires web authentication",
		},
		{
			name: "OIDC requires secret file",
			configure: func(config *ServeConfig) {
				config.WebAuthMode = webui.WebAuthModeOIDC
			},
			expectedError: "OIDC client secret file is required",
		},
		{
			name: "OIDC requires issuer",
			configure: func(config *ServeConfig) {
				configureValidOIDC(config, writeOIDCSecretFile(t, "secret"))
				config.OIDC.IssuerURL = ""
			},
			expectedError: "OIDC issuer URL is required",
		},
		{
			name: "OIDC issuer requires TLS away from loopback",
			configure: func(config *ServeConfig) {
				configureValidOIDC(config, writeOIDCSecretFile(t, "secret"))
				config.OIDC.IssuerURL = "http://issuer.example.com"
			},
			expectedError: "OIDC issuer URL must use https except on loopback hosts",
		},
		{
			name: "OIDC redirect uses fixed callback path",
			configure: func(config *ServeConfig) {
				configureValidOIDC(config, writeOIDCSecretFile(t, "secret"))
				config.OIDC.RedirectURL = "http://localhost:8080/different/callback"
			},
			expectedError: "OIDC redirect URL path must be /auth/oidc/callback",
		},
		{
			name: "OIDC session duration must be positive",
			configure: func(config *ServeConfig) {
				configureValidOIDC(config, writeOIDCSecretFile(t, "secret"))
				config.OIDC.SessionDuration = 0
			},
			expectedError: "OIDC session duration must be greater than zero",
		},
		{
			name: "OIDC enrollment requires an approver",
			configure: func(config *ServeConfig) {
				configureValidOIDC(config, writeOIDCSecretFile(t, "secret"))
				config.RunnerAuthMode = webui.RunnerAuthModeEnrollment
				config.OIDC.RunnerAdminEmails = nil
			},
			expectedError: "requires a runner-admin/admin email",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := NewServeConfig()
			tt.configure(config)

			err := validateServeConfig(config)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestBuildWebUIServerConfigAuthResolution(t *testing.T) {
	t.Run("legacy defaults generate separate tokens", func(t *testing.T) {
		serverConfig, err := buildWebUIServerConfig(NewServeConfig())
		require.NoError(t, err)

		assert.Equal(t, webui.WebAuthModeToken, serverConfig.WebAuthMode)
		assert.Equal(t, webui.RunnerAuthModeToken, serverConfig.RunnerAuthMode)
		assert.NotEmpty(t, serverConfig.AuthToken)
		assert.NotEmpty(t, serverConfig.RunnerAuthToken)
		assert.NotEqual(t, serverConfig.AuthToken, serverConfig.RunnerAuthToken)
	})

	t.Run("OIDC does not generate a web token", func(t *testing.T) {
		config := newValidOIDCServeConfig(t)

		serverConfig, err := buildWebUIServerConfig(config)
		require.NoError(t, err)

		assert.Equal(t, webui.WebAuthModeOIDC, serverConfig.WebAuthMode)
		assert.Empty(t, serverConfig.AuthToken)
		assert.Equal(t, "client-secret", serverConfig.OIDC.ClientSecret)
	})

	t.Run("OIDC preserves an admin compatibility token", func(t *testing.T) {
		config := newValidOIDCServeConfig(t)
		config.AuthToken = "compat-admin-token"

		serverConfig, err := buildWebUIServerConfig(config)
		require.NoError(t, err)

		assert.Equal(t, "compat-admin-token", serverConfig.AuthToken)
	})

	t.Run("enrollment does not generate a runner token", func(t *testing.T) {
		config := NewServeConfig()
		config.RunnerAuthMode = webui.RunnerAuthModeEnrollment

		serverConfig, err := buildWebUIServerConfig(config)
		require.NoError(t, err)

		assert.Equal(t, webui.WebAuthModeToken, serverConfig.WebAuthMode)
		assert.NotEmpty(t, serverConfig.AuthToken)
		assert.Equal(t, webui.RunnerAuthModeEnrollment, serverConfig.RunnerAuthMode)
		assert.Empty(t, serverConfig.RunnerAuthToken)
	})

	t.Run("hybrid generates a runner token", func(t *testing.T) {
		config := NewServeConfig()
		config.RunnerAuthMode = webui.RunnerAuthModeHybrid

		serverConfig, err := buildWebUIServerConfig(config)
		require.NoError(t, err)

		assert.Equal(t, webui.RunnerAuthModeHybrid, serverConfig.RunnerAuthMode)
		assert.NotEmpty(t, serverConfig.RunnerAuthToken)
		assert.NotEqual(t, serverConfig.AuthToken, serverConfig.RunnerAuthToken)
	})

	t.Run("skip auth disables both token types", func(t *testing.T) {
		config := NewServeConfig()
		config.SkipAuth = true

		serverConfig, err := buildWebUIServerConfig(config)
		require.NoError(t, err)

		assert.Equal(t, webui.WebAuthModeNone, serverConfig.WebAuthMode)
		assert.Equal(t, webui.RunnerAuthModeNone, serverConfig.RunnerAuthMode)
		assert.Empty(t, serverConfig.AuthToken)
		assert.Empty(t, serverConfig.RunnerAuthToken)
	})
}

func TestLoadOIDCClientSecret(t *testing.T) {
	t.Run("trims file contents", func(t *testing.T) {
		path := writeOIDCSecretFile(t, "  super-secret\n")

		secret, err := loadOIDCClientSecret(path)
		require.NoError(t, err)
		assert.Equal(t, "super-secret", secret)
	})

	t.Run("rejects empty file", func(t *testing.T) {
		path := writeOIDCSecretFile(t, " \n\t")

		secret, err := loadOIDCClientSecret(path)
		assert.Empty(t, secret)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is empty")
	})

	t.Run("rejects unreadable file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing-secret")

		secret, err := loadOIDCClientSecret(path)
		assert.Empty(t, secret)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read OIDC client secret file")
		assert.NotContains(t, err.Error(), "super-secret")
	})

	if runtime.GOOS != "windows" {
		t.Run("rejects group-readable file", func(t *testing.T) {
			path := writeOIDCSecretFile(t, "super-secret")
			require.NoError(t, os.Chmod(path, 0o640))

			_, err := loadOIDCClientSecret(path)
			require.ErrorContains(t, err, "must not be accessible by group or other users")
		})
	}
}

func TestIsSensitiveFlagName(t *testing.T) {
	assert.True(t, isSensitiveFlagName("auth-token"))
	assert.True(t, isSensitiveFlagName("api-key"))
	assert.True(t, isSensitiveFlagName("password"))
	assert.False(t, isSensitiveFlagName("host"))
}

func TestGetServeConfigFromFlags_UsesConfiguredCompactRatio(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("compact_ratio", 0.65)

	cmd := newServeCommandForTest()

	config := getServeConfigFromFlags(cmd)
	assert.Equal(t, 0.65, config.CompactRatio)
	assert.Equal(t, defaultOIDCScopes, config.OIDC.Scopes)
	assert.Equal(t, defaultOIDCSessionDuration, config.OIDC.SessionDuration)
}

func TestGetServeConfigFromFlags_UsesCommaSeparatedCORSOrigins(t *testing.T) {
	cmd := newServeCommandForTest()
	require.NoError(t, cmd.Flags().Set("cors-origins", "https://app.example.com,http://localhost:3000"))

	config := getServeConfigFromFlags(cmd)
	assert.Equal(t, []string{"https://app.example.com", "http://localhost:3000"}, config.CORSOrigins)
}

func TestGetServeConfigFromFlags_UsesTrustedYAMLSettings(t *testing.T) {
	setTrustedServeConfigForTest(t, map[string]any{
		"host":              "127.0.0.1",
		"port":              8443,
		"cwd":               " /srv/kodelet ",
		"web_auth_mode":     "oidc",
		"runner_auth_mode":  "hybrid",
		"auth_token":        "compat-token",
		"runner_auth_token": "runner-token",
		"cors_origins":      []string{"https://app.example.com"},
		"oidc": map[string]any{
			"issuer":              "https://issuer.example.com",
			"client_id":           "kodelet",
			"client_secret_file":  " /run/secrets/kodelet-oidc ",
			"redirect_url":        "https://kodelet.example.com/auth/oidc/callback",
			"scopes":              []string{"openid", "profile", "email", "groups"},
			"allowed_emails":      []string{"user@example.com"},
			"allowed_domains":     []string{"example.com"},
			"admin_emails":        []string{"admin@example.com"},
			"terminal_emails":     []string{"terminal@example.com"},
			"runner_admin_emails": []string{"runners@example.com"},
			"allow_any_user":      true,
			"session_duration":    "24h",
		},
	})

	config := getServeConfigFromFlags(newServeCommandForTest())
	require.NoError(t, config.ConfigError)
	assert.Equal(t, "127.0.0.1", config.Host)
	assert.Equal(t, 8443, config.Port)
	assert.Equal(t, "/srv/kodelet", config.CWD)
	assert.Equal(t, webui.WebAuthModeOIDC, config.WebAuthMode)
	assert.Equal(t, webui.RunnerAuthModeHybrid, config.RunnerAuthMode)
	assert.Equal(t, "compat-token", config.AuthToken)
	assert.Equal(t, "runner-token", config.RunnerAuthToken)
	assert.Equal(t, []string{"https://app.example.com"}, config.CORSOrigins)
	assert.Equal(t, "https://issuer.example.com", config.OIDC.IssuerURL)
	assert.Equal(t, "kodelet", config.OIDC.ClientID)
	assert.Equal(t, "/run/secrets/kodelet-oidc", config.OIDCClientSecretFile)
	assert.Equal(t, "https://kodelet.example.com/auth/oidc/callback", config.OIDC.RedirectURL)
	assert.Equal(t, []string{"openid", "profile", "email", "groups"}, config.OIDC.Scopes)
	assert.Equal(t, []string{"user@example.com"}, config.OIDC.AllowedEmails)
	assert.Equal(t, []string{"example.com"}, config.OIDC.AllowedDomains)
	assert.Equal(t, []string{"admin@example.com"}, config.OIDC.AdminEmails)
	assert.Equal(t, []string{"terminal@example.com"}, config.OIDC.TerminalEmails)
	assert.Equal(t, []string{"runners@example.com"}, config.OIDC.RunnerAdminEmails)
	assert.True(t, config.OIDC.AllowAnyUser)
	assert.Equal(t, 24*time.Hour, config.OIDC.SessionDuration)
}

func TestGetServeConfigFromFlags_ExplicitFlagsOverrideTrustedYAML(t *testing.T) {
	setTrustedServeConfigForTest(t, map[string]any{
		"host":             "127.0.0.1",
		"web_auth_mode":    "token",
		"runner_auth_mode": "token",
		"auth_token":       "yaml-token",
		"oidc": map[string]any{
			"issuer":         "https://yaml-issuer.example.com",
			"allow_any_user": true,
		},
	})
	cmd := newServeCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{
		"--host=0.0.0.0",
		"--web-auth-mode=oidc",
		"--runner-auth-mode=none",
		"--auth-token=flag-token",
		"--oidc-issuer=https://flag-issuer.example.com",
		"--oidc-allow-any-user=false",
	}))

	config := getServeConfigFromFlags(cmd)
	require.NoError(t, config.ConfigError)
	assert.Equal(t, "0.0.0.0", config.Host)
	assert.Equal(t, webui.WebAuthModeOIDC, config.WebAuthMode)
	assert.Equal(t, webui.RunnerAuthModeNone, config.RunnerAuthMode)
	assert.Equal(t, "flag-token", config.AuthToken)
	assert.Equal(t, "https://flag-issuer.example.com", config.OIDC.IssuerURL)
	assert.False(t, config.OIDC.AllowAnyUser)
}

func TestConsumeServeConfigCapturesConfiguredCredentials(t *testing.T) {
	setTrustedServeConfigForTest(t, nil)
	cmd := newServeCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{
		"--auth-token=flag-web-token",
		"--runner-auth-token=flag-runner-token",
	}))

	config, err := consumeServeConfig(cmd)

	require.NoError(t, err)
	assert.Equal(t, "flag-web-token", config.AuthToken)
	assert.Equal(t, "flag-runner-token", config.RunnerAuthToken)
}

func TestGetServeConfigFromFlags_ExplicitEmptyFlagsOverrideTrustedYAML(t *testing.T) {
	setTrustedServeConfigForTest(t, map[string]any{
		"auth_token":   "yaml-token",
		"cors_origins": []string{"https://app.example.com"},
		"oidc": map[string]any{
			"scopes": []string{"openid", "profile", "email"},
		},
	})
	cmd := newServeCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{
		"--auth-token=",
		"--cors-origins=",
		"--oidc-scopes=",
	}))

	config := getServeConfigFromFlags(cmd)
	require.NoError(t, config.ConfigError)
	assert.Empty(t, config.AuthToken)
	assert.Empty(t, config.CORSOrigins)
	assert.Empty(t, config.OIDC.Scopes)
}

func TestGetServeConfigFromFlags_RejectsInvalidTrustedYAML(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
	}{
		{
			name: "numeric skip auth",
			config: map[string]any{
				"skip_auth": 1,
			},
		},
		{
			name: "boolean auth token",
			config: map[string]any{
				"auth_token": true,
			},
		},
		{
			name: "scalar CORS origins",
			config: map[string]any{
				"cors_origins": "https://app.example.com",
			},
		},
		{
			name: "direct OIDC client secret",
			config: map[string]any{
				"oidc": map[string]any{
					"client_secret": "must-not-be-accepted",
				},
			},
		},
		{
			name: "unknown serve setting",
			config: map[string]any{
				"skip_authentication": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setTrustedServeConfigForTest(t, tt.config)

			config := getServeConfigFromFlags(newServeCommandForTest())
			require.Error(t, config.ConfigError)
			assert.Contains(t, config.ConfigError.Error(), "failed to decode trusted serve configuration")
		})
	}
}

func TestGetServeConfigFromFlags_ParsesAuthenticationFlags(t *testing.T) {
	cmd := newServeCommandForTest()
	require.NoError(t, cmd.ParseFlags([]string{
		"--web-auth-mode=oidc",
		"--runner-auth-mode=hybrid",
		"--auth-token=compat-token",
		"--runner-auth-token=runner-token",
		"--oidc-issuer=https://issuer.example.com",
		"--oidc-client-id=kodelet",
		"--oidc-client-secret-file=/run/secrets/kodelet-oidc",
		"--oidc-redirect-url=https://kodelet.example.com/auth/oidc/callback",
		"--oidc-scopes=openid,profile,email,groups",
		"--oidc-allowed-emails=user@example.com,second@example.com",
		"--oidc-allowed-domains=example.com,example.org",
		"--oidc-admin-emails=admin@example.com",
		"--oidc-terminal-emails=terminal@example.com",
		"--oidc-runner-admin-emails=runners@example.com",
		"--oidc-allow-any-user",
		"--oidc-session-duration=24h",
	}))

	config := getServeConfigFromFlags(cmd)
	assert.Equal(t, webui.WebAuthModeOIDC, config.WebAuthMode)
	assert.Equal(t, webui.RunnerAuthModeHybrid, config.RunnerAuthMode)
	assert.Equal(t, "compat-token", config.AuthToken)
	assert.Equal(t, "runner-token", config.RunnerAuthToken)
	assert.Equal(t, "https://issuer.example.com", config.OIDC.IssuerURL)
	assert.Equal(t, "kodelet", config.OIDC.ClientID)
	assert.Equal(t, "/run/secrets/kodelet-oidc", config.OIDCClientSecretFile)
	assert.Empty(t, config.OIDC.ClientSecret)
	assert.Equal(t, "https://kodelet.example.com/auth/oidc/callback", config.OIDC.RedirectURL)
	assert.Equal(t, []string{"openid", "profile", "email", "groups"}, config.OIDC.Scopes)
	assert.Equal(t, []string{"user@example.com", "second@example.com"}, config.OIDC.AllowedEmails)
	assert.Equal(t, []string{"example.com", "example.org"}, config.OIDC.AllowedDomains)
	assert.Equal(t, []string{"admin@example.com"}, config.OIDC.AdminEmails)
	assert.Equal(t, []string{"terminal@example.com"}, config.OIDC.TerminalEmails)
	assert.Equal(t, []string{"runners@example.com"}, config.OIDC.RunnerAdminEmails)
	assert.True(t, config.OIDC.AllowAnyUser)
	assert.Equal(t, 24*time.Hour, config.OIDC.SessionDuration)
	assert.Nil(t, cmd.Flags().Lookup("oidc-client-secret"))
}

func newServeCommandForTest() *cobra.Command {
	cmd := &cobra.Command{Use: "serve"}
	addServeFlags(cmd, NewServeConfig())
	return cmd
}

func setTrustedServeConfigForTest(t *testing.T, config map[string]any) {
	t.Helper()
	previous := viper.Get("serve")
	wasSet := viper.IsSet("serve")
	viper.Set("serve", config)
	t.Cleanup(func() {
		if wasSet {
			viper.Set("serve", previous)
			return
		}
		viper.Set("serve", nil)
	})
}

func newValidOIDCServeConfig(t *testing.T) *ServeConfig {
	t.Helper()
	config := NewServeConfig()
	configureValidOIDC(config, writeOIDCSecretFile(t, "client-secret"))
	return config
}

func configureValidOIDC(config *ServeConfig, clientSecretFile string) {
	config.WebAuthMode = webui.WebAuthModeOIDC
	config.RunnerAuthMode = webui.RunnerAuthModeNone
	config.OIDCClientSecretFile = clientSecretFile
	config.OIDC.IssuerURL = "https://issuer.example.com"
	config.OIDC.ClientID = "kodelet"
	config.OIDC.RedirectURL = "http://localhost:8080/auth/oidc/callback"
	config.OIDC.AllowAnyUser = true
	config.OIDC.RunnerAdminEmails = []string{"runner-admin@example.com"}
}

func writeOIDCSecretFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oidc-client-secret")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
