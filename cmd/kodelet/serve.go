package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	oidcCallbackPath           = "/auth/oidc/callback"
	defaultOIDCSessionDuration = 12 * time.Hour
)

var defaultOIDCScopes = []string{"openid", "profile", "email"}

type ServeConfig struct {
	Host                 string
	Port                 int
	CWD                  string
	CompactRatio         float64
	AuthToken            string
	RunnerAuthToken      string
	WebAuthMode          webui.WebAuthMode
	RunnerAuthMode       webui.RunnerAuthMode
	OIDC                 webui.OIDCConfig
	OIDCClientSecretFile string
	SkipAuth             bool
	CORSOrigins          []string
	ConfigError          error
}

type trustedServeConfig struct {
	Host            *string                 `mapstructure:"host"`
	Port            *int                    `mapstructure:"port"`
	CWD             *string                 `mapstructure:"cwd"`
	AuthToken       *string                 `mapstructure:"auth_token"`
	RunnerAuthToken *string                 `mapstructure:"runner_auth_token"`
	WebAuthMode     *string                 `mapstructure:"web_auth_mode"`
	RunnerAuthMode  *string                 `mapstructure:"runner_auth_mode"`
	SkipAuth        *bool                   `mapstructure:"skip_auth"`
	CORSOrigins     []string                `mapstructure:"cors_origins"`
	OIDC            *trustedServeOIDCConfig `mapstructure:"oidc"`
}

type trustedServeOIDCConfig struct {
	IssuerURL         *string        `mapstructure:"issuer"`
	ClientID          *string        `mapstructure:"client_id"`
	ClientSecretFile  *string        `mapstructure:"client_secret_file"`
	RedirectURL       *string        `mapstructure:"redirect_url"`
	Scopes            []string       `mapstructure:"scopes"`
	AllowedEmails     []string       `mapstructure:"allowed_emails"`
	AllowedDomains    []string       `mapstructure:"allowed_domains"`
	AdminEmails       []string       `mapstructure:"admin_emails"`
	TerminalEmails    []string       `mapstructure:"terminal_emails"`
	RunnerAdminEmails []string       `mapstructure:"runner_admin_emails"`
	AllowAnyUser      *bool          `mapstructure:"allow_any_user"`
	SessionDuration   *time.Duration `mapstructure:"session_duration"`
}

func NewServeConfig() *ServeConfig {
	return &ServeConfig{
		Host:         "localhost",
		Port:         8080,
		CWD:          "",
		CompactRatio: llmtypes.DefaultCompactRatio,
		OIDC: webui.OIDCConfig{
			Scopes:          append([]string(nil), defaultOIDCScopes...),
			SessionDuration: defaultOIDCSessionDuration,
		},
	}
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web UI server for chatting with kodelet",
	Long: `Start a local web server that provides an interactive chat interface for kodelet.
The web UI lets you continue conversations, inspect tool activity, and browse recent
chat history from the browser while still using the same embedded assets in the binary.

The server will be available at http://localhost:8080 by default. A random
web authentication token and a separate runner authentication token are generated
unless explicit authentication modes are configured or --skip-auth is set.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		config, err := consumeServeConfig(cmd)
		if err != nil {
			return err
		}
		runServeCommand(ctx, config)
		return nil
	},
}

func init() {
	addServeFlags(serveCmd, NewServeConfig())
}

func addServeFlags(cmd *cobra.Command, defaults *ServeConfig) {
	cmd.Flags().String("host", defaults.Host, "Host to bind the web server to")
	cmd.Flags().Int("port", defaults.Port, "Port to bind the web server to")
	cmd.Flags().String("cwd", defaults.CWD, "Default working directory for new web conversations")
	cmd.Flags().String("web-auth-mode", string(defaults.WebAuthMode), "Web authentication mode: token, oidc, or none (default: token)")
	cmd.Flags().String("runner-auth-mode", string(defaults.RunnerAuthMode), "Runner authentication mode: token, enrollment, hybrid, or none (default: token)")
	cmd.Flags().String("auth-token", defaults.AuthToken, "Web UI token; generated in token mode, or used as an admin compatibility credential in OIDC mode")
	cmd.Flags().String("runner-auth-token", defaults.RunnerAuthToken, "Runner registration token; generated in token and hybrid modes")
	cmd.Flags().Bool("skip-auth", defaults.SkipAuth, "Compatibility shorthand for --web-auth-mode=none --runner-auth-mode=none")
	cmd.Flags().String("oidc-issuer", defaults.OIDC.IssuerURL, "OIDC issuer URL")
	cmd.Flags().String("oidc-client-id", defaults.OIDC.ClientID, "OIDC client ID")
	cmd.Flags().String("oidc-client-secret-file", defaults.OIDCClientSecretFile, "Path to a file containing the OIDC client secret")
	cmd.Flags().String("oidc-redirect-url", defaults.OIDC.RedirectURL, "OIDC redirect URL; its path must be /auth/oidc/callback")
	cmd.Flags().StringSlice("oidc-scopes", defaults.OIDC.Scopes, "OIDC scopes (comma-separated or repeated)")
	cmd.Flags().StringSlice("oidc-allowed-emails", defaults.OIDC.AllowedEmails, "Email addresses allowed to sign in with OIDC (comma-separated or repeated)")
	cmd.Flags().StringSlice("oidc-allowed-domains", defaults.OIDC.AllowedDomains, "Email domains allowed to sign in with OIDC (comma-separated or repeated)")
	cmd.Flags().StringSlice("oidc-admin-emails", defaults.OIDC.AdminEmails, "OIDC email addresses granted the admin role (comma-separated or repeated)")
	cmd.Flags().StringSlice("oidc-terminal-emails", defaults.OIDC.TerminalEmails, "OIDC email addresses granted terminal access (comma-separated or repeated)")
	cmd.Flags().StringSlice("oidc-runner-admin-emails", defaults.OIDC.RunnerAdminEmails, "OIDC email addresses allowed to administer runners (comma-separated or repeated)")
	cmd.Flags().Bool("oidc-allow-any-user", defaults.OIDC.AllowAnyUser, "Allow any verified OIDC user to sign in")
	cmd.Flags().Duration("oidc-session-duration", defaults.OIDC.SessionDuration, "OIDC web session duration")
	cmd.Flags().StringSlice("cors-origins", defaults.CORSOrigins, "Additional allowed CORS origins for browser clients (comma-separated or repeated); loopback origins are always allowed")
}

func getServeConfigFromFlags(cmd *cobra.Command) *ServeConfig {
	config := NewServeConfig()
	if err := applyTrustedServeConfig(config); err != nil {
		config.ConfigError = err
	}

	if host, err := cmd.Flags().GetString("host"); err == nil && cmd.Flags().Changed("host") {
		config.Host = host
	}
	if port, err := cmd.Flags().GetInt("port"); err == nil && cmd.Flags().Changed("port") {
		config.Port = port
	}
	if cwd, err := cmd.Flags().GetString("cwd"); err == nil && cmd.Flags().Changed("cwd") {
		config.CWD = strings.TrimSpace(cwd)
	}
	if webAuthMode, err := cmd.Flags().GetString("web-auth-mode"); err == nil && cmd.Flags().Changed("web-auth-mode") {
		config.WebAuthMode = webui.WebAuthMode(webAuthMode)
	}
	if runnerAuthMode, err := cmd.Flags().GetString("runner-auth-mode"); err == nil && cmd.Flags().Changed("runner-auth-mode") {
		config.RunnerAuthMode = webui.RunnerAuthMode(runnerAuthMode)
	}
	if authToken, err := cmd.Flags().GetString("auth-token"); err == nil && cmd.Flags().Changed("auth-token") {
		config.AuthToken = authToken
	}
	if runnerAuthToken, err := cmd.Flags().GetString("runner-auth-token"); err == nil && cmd.Flags().Changed("runner-auth-token") {
		config.RunnerAuthToken = runnerAuthToken
	}
	if skipAuth, err := cmd.Flags().GetBool("skip-auth"); err == nil && cmd.Flags().Changed("skip-auth") {
		config.SkipAuth = skipAuth
	}
	if oidcIssuer, err := cmd.Flags().GetString("oidc-issuer"); err == nil && cmd.Flags().Changed("oidc-issuer") {
		config.OIDC.IssuerURL = oidcIssuer
	}
	if oidcClientID, err := cmd.Flags().GetString("oidc-client-id"); err == nil && cmd.Flags().Changed("oidc-client-id") {
		config.OIDC.ClientID = oidcClientID
	}
	if oidcClientSecretFile, err := cmd.Flags().GetString("oidc-client-secret-file"); err == nil && cmd.Flags().Changed("oidc-client-secret-file") {
		config.OIDCClientSecretFile = strings.TrimSpace(oidcClientSecretFile)
	}
	if oidcRedirectURL, err := cmd.Flags().GetString("oidc-redirect-url"); err == nil && cmd.Flags().Changed("oidc-redirect-url") {
		config.OIDC.RedirectURL = oidcRedirectURL
	}
	if oidcScopes, err := cmd.Flags().GetStringSlice("oidc-scopes"); err == nil && cmd.Flags().Changed("oidc-scopes") {
		config.OIDC.Scopes = oidcScopes
	}
	if oidcAllowedEmails, err := cmd.Flags().GetStringSlice("oidc-allowed-emails"); err == nil && cmd.Flags().Changed("oidc-allowed-emails") {
		config.OIDC.AllowedEmails = oidcAllowedEmails
	}
	if oidcAllowedDomains, err := cmd.Flags().GetStringSlice("oidc-allowed-domains"); err == nil && cmd.Flags().Changed("oidc-allowed-domains") {
		config.OIDC.AllowedDomains = oidcAllowedDomains
	}
	if oidcAdminEmails, err := cmd.Flags().GetStringSlice("oidc-admin-emails"); err == nil && cmd.Flags().Changed("oidc-admin-emails") {
		config.OIDC.AdminEmails = oidcAdminEmails
	}
	if oidcTerminalEmails, err := cmd.Flags().GetStringSlice("oidc-terminal-emails"); err == nil && cmd.Flags().Changed("oidc-terminal-emails") {
		config.OIDC.TerminalEmails = oidcTerminalEmails
	}
	if oidcRunnerAdminEmails, err := cmd.Flags().GetStringSlice("oidc-runner-admin-emails"); err == nil && cmd.Flags().Changed("oidc-runner-admin-emails") {
		config.OIDC.RunnerAdminEmails = oidcRunnerAdminEmails
	}
	if oidcAllowAnyUser, err := cmd.Flags().GetBool("oidc-allow-any-user"); err == nil && cmd.Flags().Changed("oidc-allow-any-user") {
		config.OIDC.AllowAnyUser = oidcAllowAnyUser
	}
	if oidcSessionDuration, err := cmd.Flags().GetDuration("oidc-session-duration"); err == nil && cmd.Flags().Changed("oidc-session-duration") {
		config.OIDC.SessionDuration = oidcSessionDuration
	}
	if corsOrigins, err := cmd.Flags().GetStringSlice("cors-origins"); err == nil && cmd.Flags().Changed("cors-origins") {
		config.CORSOrigins = corsOrigins
	}
	llmConfig, err := llm.GetConfigFromViperWithCmd(cmd)
	if err != nil {
		if config.ConfigError == nil {
			config.ConfigError = err
		}
	} else {
		config.CompactRatio = llmConfig.CompactRatio
	}

	return config
}

func consumeServeConfig(cmd *cobra.Command) (*ServeConfig, error) {
	config := getServeConfigFromFlags(cmd)
	config.AuthToken = strings.Clone(config.AuthToken)
	config.RunnerAuthToken = strings.Clone(config.RunnerAuthToken)
	if config.AuthToken != "" || config.RunnerAuthToken != "" {
		if err := protectProcessSecrets("auth-token", "runner-auth-token"); err != nil {
			return nil, errors.Wrap(err, "failed to protect server authentication tokens")
		}
	}
	return config, nil
}

func applyTrustedServeConfig(config *ServeConfig) error {
	var trusted trustedServeConfig
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           &trusted,
		TagName:          "mapstructure",
		WeaklyTypedInput: false,
		ErrorUnused:      true,
		DecodeHook:       mapstructure.StringToTimeDurationHookFunc(),
	})
	if err != nil {
		return errors.Wrap(err, "failed to initialize trusted serve configuration decoder")
	}
	if err := decoder.Decode(viper.Get("serve")); err != nil {
		return errors.Wrap(err, "failed to decode trusted serve configuration")
	}
	if trusted.Host != nil {
		config.Host = *trusted.Host
	}
	if trusted.Port != nil {
		config.Port = *trusted.Port
	}
	if trusted.CWD != nil {
		config.CWD = strings.TrimSpace(*trusted.CWD)
	}
	if trusted.AuthToken != nil {
		config.AuthToken = *trusted.AuthToken
	}
	if trusted.RunnerAuthToken != nil {
		config.RunnerAuthToken = *trusted.RunnerAuthToken
	}
	if trusted.WebAuthMode != nil {
		config.WebAuthMode = webui.WebAuthMode(*trusted.WebAuthMode)
	}
	if trusted.RunnerAuthMode != nil {
		config.RunnerAuthMode = webui.RunnerAuthMode(*trusted.RunnerAuthMode)
	}
	if trusted.SkipAuth != nil {
		config.SkipAuth = *trusted.SkipAuth
	}
	if trusted.CORSOrigins != nil {
		config.CORSOrigins = trusted.CORSOrigins
	}
	if trusted.OIDC == nil {
		return nil
	}
	if trusted.OIDC.IssuerURL != nil {
		config.OIDC.IssuerURL = *trusted.OIDC.IssuerURL
	}
	if trusted.OIDC.ClientID != nil {
		config.OIDC.ClientID = *trusted.OIDC.ClientID
	}
	if trusted.OIDC.ClientSecretFile != nil {
		config.OIDCClientSecretFile = strings.TrimSpace(*trusted.OIDC.ClientSecretFile)
	}
	if trusted.OIDC.RedirectURL != nil {
		config.OIDC.RedirectURL = *trusted.OIDC.RedirectURL
	}
	if trusted.OIDC.Scopes != nil {
		config.OIDC.Scopes = trusted.OIDC.Scopes
	}
	if trusted.OIDC.AllowedEmails != nil {
		config.OIDC.AllowedEmails = trusted.OIDC.AllowedEmails
	}
	if trusted.OIDC.AllowedDomains != nil {
		config.OIDC.AllowedDomains = trusted.OIDC.AllowedDomains
	}
	if trusted.OIDC.AdminEmails != nil {
		config.OIDC.AdminEmails = trusted.OIDC.AdminEmails
	}
	if trusted.OIDC.TerminalEmails != nil {
		config.OIDC.TerminalEmails = trusted.OIDC.TerminalEmails
	}
	if trusted.OIDC.RunnerAdminEmails != nil {
		config.OIDC.RunnerAdminEmails = trusted.OIDC.RunnerAdminEmails
	}
	if trusted.OIDC.AllowAnyUser != nil {
		config.OIDC.AllowAnyUser = *trusted.OIDC.AllowAnyUser
	}
	if trusted.OIDC.SessionDuration != nil {
		config.OIDC.SessionDuration = *trusted.OIDC.SessionDuration
	}
	return nil
}

func validateServeConfig(config *ServeConfig) error {
	if config == nil {
		return errors.New("server configuration is required")
	}
	if config.ConfigError != nil {
		return config.ConfigError
	}

	if config.Host == "" {
		return errors.New("host cannot be empty")
	}

	if config.Host != "localhost" && config.Host != "0.0.0.0" {
		if ip := net.ParseIP(config.Host); ip == nil {
			if strings.Contains(config.Host, " ") || strings.Contains(config.Host, ":") {
				return fmt.Errorf("invalid host: %s", config.Host)
			}
		}
	}

	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", config.Port)
	}

	if config.Port < 1024 {
		logger.G(context.Background()).WithField("port", config.Port).Warn("using privileged port (< 1024) may require elevated permissions")
	}

	if config.CompactRatio <= 0.0 || config.CompactRatio > 1.0 {
		return errors.New("compact-ratio must be greater than 0.0 and less than or equal to 1.0")
	}

	webAuthMode, runnerAuthMode, err := resolveServeAuthModes(config)
	if err != nil {
		return err
	}

	if err := webui.ValidateAuthToken(config.AuthToken); err != nil {
		return err
	}
	if err := webui.ValidateAuthToken(config.RunnerAuthToken); err != nil {
		return errors.Wrap(err, "invalid runner auth token")
	}
	if config.AuthToken != "" && config.RunnerAuthToken != "" && config.AuthToken == config.RunnerAuthToken {
		return errors.New("runner auth token must differ from web auth token")
	}
	if webAuthMode == webui.WebAuthModeNone && config.AuthToken != "" {
		return errors.New("web auth token cannot be used when web authentication mode is none")
	}
	if runnerAuthMode == webui.RunnerAuthModeNone && config.RunnerAuthToken != "" {
		return errors.New("runner auth token cannot be used when runner authentication mode is none")
	}
	if runnerAuthMode == webui.RunnerAuthModeEnrollment && config.RunnerAuthToken != "" {
		return errors.New("runner auth token requires runner authentication mode token or hybrid")
	}
	if (runnerAuthMode == webui.RunnerAuthModeEnrollment || runnerAuthMode == webui.RunnerAuthModeHybrid) && webAuthMode == webui.WebAuthModeNone {
		return errors.New("runner enrollment requires web authentication")
	}
	if webAuthMode == webui.WebAuthModeOIDC {
		if strings.TrimSpace(config.OIDCClientSecretFile) == "" {
			return errors.New("OIDC client secret file is required when web authentication mode is oidc")
		}
		if config.OIDC.SessionDuration <= 0 {
			return errors.New("OIDC session duration must be greater than zero")
		}

		oidcConfig := config.OIDC
		oidcConfig.ClientSecret = "loaded-from-file"
		if err := oidcConfig.Validate(); err != nil {
			return errors.Wrap(err, "invalid OIDC configuration")
		}
		redirectURL, err := url.Parse(strings.TrimSpace(oidcConfig.RedirectURL))
		if err != nil {
			return errors.Wrap(err, "invalid OIDC redirect URL")
		}
		if redirectURL.Path != oidcCallbackPath {
			return errors.Errorf("OIDC redirect URL path must be %s", oidcCallbackPath)
		}
		if (runnerAuthMode == webui.RunnerAuthModeEnrollment || runnerAuthMode == webui.RunnerAuthModeHybrid) && config.AuthToken == "" && len(oidcConfig.AdminEmails) == 0 && len(oidcConfig.RunnerAdminEmails) == 0 {
			return errors.New("OIDC runner enrollment requires a runner-admin/admin email or an administrative compatibility token")
		}
	}

	if err := webui.ValidateCORSOrigins(config.CORSOrigins); err != nil {
		return err
	}

	return nil
}

func resolveServeAuthModes(config *ServeConfig) (webui.WebAuthMode, webui.RunnerAuthMode, error) {
	if config == nil {
		return "", "", errors.New("server configuration is required")
	}

	webAuthMode := webui.WebAuthMode(strings.ToLower(strings.TrimSpace(string(config.WebAuthMode))))
	runnerAuthMode := webui.RunnerAuthMode(strings.ToLower(strings.TrimSpace(string(config.RunnerAuthMode))))
	if config.SkipAuth {
		if config.AuthToken != "" {
			return "", "", errors.New("web auth token cannot be used when authentication is disabled")
		}
		if config.RunnerAuthToken != "" {
			return "", "", errors.New("runner auth token cannot be used when authentication is disabled")
		}
		if webAuthMode != "" && webAuthMode != webui.WebAuthModeNone {
			return "", "", errors.New("disabled authentication conflicts with a non-none web authentication mode")
		}
		if runnerAuthMode != "" && runnerAuthMode != webui.RunnerAuthModeNone {
			return "", "", errors.New("disabled authentication conflicts with a non-none runner authentication mode")
		}
		return webui.WebAuthModeNone, webui.RunnerAuthModeNone, nil
	}

	if webAuthMode == "" {
		webAuthMode = webui.WebAuthModeToken
	}
	switch webAuthMode {
	case webui.WebAuthModeToken, webui.WebAuthModeOIDC, webui.WebAuthModeNone:
	default:
		return "", "", errors.Errorf("invalid web authentication mode %q: must be token, oidc, or none", config.WebAuthMode)
	}

	if runnerAuthMode == "" {
		runnerAuthMode = webui.RunnerAuthModeToken
	}
	switch runnerAuthMode {
	case webui.RunnerAuthModeToken, webui.RunnerAuthModeEnrollment, webui.RunnerAuthModeHybrid, webui.RunnerAuthModeNone:
	default:
		return "", "", errors.Errorf("invalid runner authentication mode %q: must be token, enrollment, hybrid, or none", config.RunnerAuthMode)
	}

	return webAuthMode, runnerAuthMode, nil
}

func loadOIDCClientSecret(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("OIDC client secret file path cannot be empty")
	}
	if runtime.GOOS == "windows" {
		if err := osutil.EnsurePrivateFile(path); err != nil {
			return "", errors.Wrapf(err, "failed to secure OIDC client secret file %q", path)
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read OIDC client secret file %q", path)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", errors.Wrapf(err, "failed to inspect OIDC client secret file %q", path)
	}
	if !info.Mode().IsRegular() {
		return "", errors.Errorf("OIDC client secret file %q must be a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", errors.Errorf("OIDC client secret file %q must not be accessible by group or other users", path)
	}
	contents, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return "", errors.Wrapf(err, "failed to read OIDC client secret file %q", path)
	}
	if len(contents) > 64*1024 {
		return "", errors.Errorf("OIDC client secret file %q exceeds 65536 bytes", path)
	}
	clientSecret := strings.TrimSpace(string(contents))
	if clientSecret == "" {
		return "", errors.Errorf("OIDC client secret file %q is empty", path)
	}
	return clientSecret, nil
}

func buildWebUIServerConfig(config *ServeConfig) (*webui.ServerConfig, error) {
	if err := validateServeConfig(config); err != nil {
		return nil, err
	}

	webAuthMode, runnerAuthMode, err := resolveServeAuthModes(config)
	if err != nil {
		return nil, err
	}
	authToken := config.AuthToken
	runnerAuthToken := config.RunnerAuthToken
	if webAuthMode == webui.WebAuthModeToken && authToken == "" {
		authToken, err = webui.NewAuthToken()
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate web auth token")
		}
	}
	if (runnerAuthMode == webui.RunnerAuthModeToken || runnerAuthMode == webui.RunnerAuthModeHybrid) && runnerAuthToken == "" {
		for runnerAuthToken == "" || runnerAuthToken == authToken {
			runnerAuthToken, err = webui.NewAuthToken()
			if err != nil {
				return nil, errors.Wrap(err, "failed to generate runner auth token")
			}
		}
	}

	var oidcConfig webui.OIDCConfig
	if webAuthMode == webui.WebAuthModeOIDC {
		oidcConfig = config.OIDC
		oidcConfig.ClientSecret, err = loadOIDCClientSecret(config.OIDCClientSecretFile)
		if err != nil {
			return nil, err
		}
	}

	serverConfig := &webui.ServerConfig{
		Host:            config.Host,
		Port:            config.Port,
		CWD:             config.CWD,
		CompactRatio:    config.CompactRatio,
		AuthToken:       authToken,
		RunnerAuthToken: runnerAuthToken,
		WebAuthMode:     webAuthMode,
		RunnerAuthMode:  runnerAuthMode,
		OIDC:            oidcConfig,
		CORSOrigins:     config.CORSOrigins,
	}
	if err := serverConfig.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid resolved server configuration")
	}
	return serverConfig, nil
}

func runServeCommand(ctx context.Context, config *ServeConfig) {
	serverConfig, err := buildWebUIServerConfig(config)
	if err != nil {
		presenter.Error(err, "invalid server configuration")
		os.Exit(1)
	}

	logger.G(ctx).WithFields(map[string]any{
		"host":             serverConfig.Host,
		"port":             serverConfig.Port,
		"web_auth_mode":    serverConfig.WebAuthMode,
		"runner_auth_mode": serverConfig.RunnerAuthMode,
	}).Info("Starting web UI server")

	server, err := webui.NewServer(ctx, serverConfig)
	if err != nil {
		presenter.Error(err, "failed to create web server")
		os.Exit(1)
	}
	defer func() {
		if closeErr := server.Close(); closeErr != nil {
			logger.G(ctx).WithError(closeErr).Error("failed to close web server")
		}
	}()

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	baseURL := serveBaseURL(serverConfig.Host, serverConfig.Port)
	webTokenConfigured := strings.TrimSpace(config.AuthToken) != ""
	runnerTokenConfigured := strings.TrimSpace(config.RunnerAuthToken) != ""
	presenter.Success(fmt.Sprintf("Web UI server starting on %s", baseURL))
	switch serverConfig.WebAuthMode {
	case webui.WebAuthModeToken:
		presenter.Info("Web UI authentication mode: token")
		if webTokenConfigured {
			presenter.Info("Authentication token: configured (value not displayed)")
		} else {
			presenter.Info(fmt.Sprintf("Authentication token: %s", serverConfig.AuthToken))
			presenter.Info(fmt.Sprintf("Open this URL: %s", serveURLWithToken(baseURL, serverConfig.AuthToken)))
		}
	case webui.WebAuthModeOIDC:
		presenter.Info("Web UI authentication mode: OIDC")
		presenter.Info(fmt.Sprintf("Open this URL: %s", baseURL))
		if serverConfig.AuthToken != "" {
			presenter.Info("OIDC admin compatibility token: configured (value not displayed)")
		}
	case webui.WebAuthModeNone:
		if config.SkipAuth {
			presenter.Warning("Web UI authentication disabled (--skip-auth)")
		} else {
			presenter.Warning("Web UI authentication disabled (--web-auth-mode=none)")
		}
	}
	switch serverConfig.RunnerAuthMode {
	case webui.RunnerAuthModeToken:
		presenter.Info("Runner authentication mode: token")
		if runnerTokenConfigured {
			presenter.Info("Runner authentication token: configured (value not displayed)")
		} else {
			presenter.Info(fmt.Sprintf("Runner authentication token: %s", serverConfig.RunnerAuthToken))
		}
	case webui.RunnerAuthModeEnrollment:
		presenter.Info("Runner authentication mode: enrollment")
		presenter.Info(fmt.Sprintf("Approve runner enrollments at: %s/runner/enroll", baseURL))
	case webui.RunnerAuthModeHybrid:
		presenter.Info("Runner authentication mode: hybrid (token or enrollment)")
		if runnerTokenConfigured {
			presenter.Info("Runner authentication token: configured (value not displayed)")
		} else {
			presenter.Info(fmt.Sprintf("Runner authentication token: %s", serverConfig.RunnerAuthToken))
		}
		presenter.Info(fmt.Sprintf("Approve runner enrollments at: %s/runner/enroll", baseURL))
	case webui.RunnerAuthModeNone:
		if config.SkipAuth {
			presenter.Warning("Runner authentication disabled (--skip-auth)")
		} else {
			presenter.Warning("Runner authentication disabled (--runner-auth-mode=none)")
		}
	}
	presenter.Info("Press Ctrl+C to stop the server")

	if err := server.Start(ctx); err != nil {
		logger.G(ctx).WithError(err).Error("web server error")
		presenter.Error(err, "web server failed")
		os.Exit(1)
	}

	presenter.Info("Web server stopped")
}

func serveBaseURL(host string, port int) string {
	displayHost := host
	if displayHost == "" {
		displayHost = "localhost"
	}
	if displayHost == "0.0.0.0" || displayHost == "::" {
		displayHost = "localhost"
	}
	if strings.Contains(displayHost, ":") && !strings.HasPrefix(displayHost, "[") {
		displayHost = "[" + displayHost + "]"
	}

	return fmt.Sprintf("http://%s:%d", displayHost, port)
}

func serveURLWithToken(baseURL string, authToken string) string {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}

	query := parsedURL.Query()
	query.Set("token", authToken)
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String()
}
