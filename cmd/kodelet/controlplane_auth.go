package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/controlplane/userauth"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

const (
	controlPlaneAuthTokenSourceFlag        = "flag"
	controlPlaneAuthTokenSourceEnvironment = "environment"
	controlPlaneAuthTokenSourceStored      = "stored"
	controlPlaneAuthTokenSourceNone        = "none"
)

type controlPlaneAuthLoginConfig struct {
	Server      string
	NoBrowser   bool
	Store       *userauth.Store
	HTTPClient  *http.Client
	OpenBrowser func(string) error
}

type controlPlaneAuthLogoutConfig struct {
	Server     string
	Store      *userauth.Store
	HTTPClient *http.Client
}

type controlPlaneAuthStatusConfig struct {
	Server     string
	Store      *userauth.Store
	HTTPClient *http.Client
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage control-plane authentication",
	Long:  "Log in to, log out from, and inspect authentication for a Kodelet control plane.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to a control plane",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runControlPlaneAuthLogin(cmd.Context(), controlPlaneAuthLoginConfigFromFlags(cmd), cmd.OutOrStdout())
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out from a control plane",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runControlPlaneAuthLogout(cmd.Context(), controlPlaneAuthLogoutConfigFromFlags(cmd), cmd.OutOrStdout())
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show control-plane authentication status",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runControlPlaneAuthStatus(cmd.Context(), controlPlaneAuthStatusConfigFromFlags(cmd), cmd.OutOrStdout())
	},
}

func init() {
	for _, command := range []*cobra.Command{authLoginCmd, authLogoutCmd, authStatusCmd} {
		command.Flags().String("server", defaultRunnerServer, "Control-plane URL")
	}
	authLoginCmd.Flags().Bool("no-browser", false, "Do not open the browser automatically")
	authCmd.AddCommand(authLoginCmd, authLogoutCmd, authStatusCmd)
	rootCmd.AddCommand(authCmd)
}

func controlPlaneAuthLoginConfigFromFlags(cmd *cobra.Command) controlPlaneAuthLoginConfig {
	server, _ := serverFlagOrConfig(cmd)
	noBrowser, _ := cmd.Flags().GetBool("no-browser")
	return controlPlaneAuthLoginConfig{Server: server, NoBrowser: noBrowser}
}

func controlPlaneAuthLogoutConfigFromFlags(cmd *cobra.Command) controlPlaneAuthLogoutConfig {
	server, _ := serverFlagOrConfig(cmd)
	return controlPlaneAuthLogoutConfig{Server: server}
}

func controlPlaneAuthStatusConfigFromFlags(cmd *cobra.Command) controlPlaneAuthStatusConfig {
	server, _ := serverFlagOrConfig(cmd)
	return controlPlaneAuthStatusConfig{Server: server}
}

func runControlPlaneAuthLogin(ctx context.Context, config controlPlaneAuthLoginConfig, output io.Writer) error {
	server, store, err := prepareControlPlaneAuthState(config.Server, config.Store)
	if err != nil {
		return err
	}
	output = controlPlaneAuthOutput(output)

	credential, found, err := store.LoadCredential(server)
	if err != nil {
		return errors.Wrap(err, "failed to load local control-plane credential")
	}
	if found {
		if !credential.ExpiresAt.After(time.Now().UTC()) {
			if _, err := store.DeleteCredential(server, credential.CredentialID); err != nil {
				return errors.Wrap(err, "failed to delete expired local control-plane credential")
			}
			fmt.Fprintln(output, "Stored control-plane credential has expired; starting a new login")
		} else {
			principal, validateErr := userauth.ValidateCredential(ctx, server, credential.BearerToken, config.HTTPClient)
			switch {
			case validateErr == nil:
				fmt.Fprintf(output, "Already logged in to %s\n", server)
				writeControlPlaneCredential(output, credential.CredentialID, principal, credential.ExpiresAt)
				fmt.Fprintf(output, "Credentials directory: %s\n", store.Root())
				return nil
			case isControlPlaneAuthUnauthorized(validateErr):
				if _, err := store.DeleteCredential(server, credential.CredentialID); err != nil {
					return errors.Wrap(err, "failed to delete invalid local control-plane credential")
				}
				fmt.Fprintln(output, "Stored control-plane credential is invalid or revoked; starting a new login")
			default:
				return errors.Wrap(validateErr, "failed to validate existing control-plane credential")
			}
		}
	}

	openBrowser := config.OpenBrowser
	if openBrowser == nil {
		openBrowser = osutil.OpenBrowser
	}
	credential, err = userauth.Login(ctx, userauth.LoginConfig{
		Server:     server,
		Store:      store,
		HTTPClient: config.HTTPClient,
		OnPending: func(info userauth.LoginInfo) {
			if info.Resumed {
				fmt.Fprintln(output, "Resuming pending control-plane login")
			} else {
				fmt.Fprintln(output, "Control-plane login started")
			}
			fmt.Fprintf(output, "Login code: %s\n", info.UserCode)
			fmt.Fprintf(output, "Enter this code at: %s\n", info.VerificationURL)
			if config.NoBrowser {
				fmt.Fprintf(output, "Open this URL manually to continue: %s\n", info.VerificationURL)
			} else if browserErr := openBrowser(info.VerificationURL); browserErr != nil {
				fmt.Fprintf(output, "Could not open the browser automatically: %v\n", browserErr)
				fmt.Fprintf(output, "Open this URL manually to continue: %s\n", info.VerificationURL)
			}
			fmt.Fprintln(output, "Waiting for browser approval...")
		},
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(output, "Control-plane login approved")
	fmt.Fprintf(output, "Server: %s\n", server)
	writeControlPlaneCredential(output, credential.CredentialID, credential.Principal, credential.ExpiresAt)
	fmt.Fprintf(output, "Credentials directory: %s\n", store.Root())
	return nil
}

func runControlPlaneAuthLogout(ctx context.Context, config controlPlaneAuthLogoutConfig, output io.Writer) error {
	server, store, err := prepareControlPlaneAuthState(config.Server, config.Store)
	if err != nil {
		return err
	}
	output = controlPlaneAuthOutput(output)

	credential, found, err := store.LoadCredential(server)
	if err != nil {
		return errors.Wrap(err, "failed to load local control-plane credential")
	}
	if !found {
		fmt.Fprintf(output, "Already logged out from %s\n", server)
		return nil
	}

	revokeErr := userauth.RevokeCredential(ctx, server, credential.BearerToken, config.HTTPClient)
	if revokeErr != nil && !isControlPlaneAuthUnauthorized(revokeErr) {
		return errors.Wrap(revokeErr, "failed to revoke control-plane credential")
	}
	if _, err := store.DeleteCredential(server, credential.CredentialID); err != nil {
		return errors.Wrap(err, "failed to delete local control-plane credential")
	}
	fmt.Fprintf(output, "Logged out from %s\n", server)
	return nil
}

func runControlPlaneAuthStatus(ctx context.Context, config controlPlaneAuthStatusConfig, output io.Writer) error {
	server, store, err := prepareControlPlaneAuthState(config.Server, config.Store)
	if err != nil {
		return err
	}
	output = controlPlaneAuthOutput(output)

	credential, credentialFound, err := store.LoadCredential(server)
	if err != nil {
		return errors.Wrap(err, "failed to load local control-plane credential")
	}
	pending, pendingFound, err := store.LoadPendingLogin(server)
	if err != nil {
		return errors.Wrap(err, "failed to load pending control-plane login")
	}

	fmt.Fprintf(output, "Server: %s\n", server)
	if !credentialFound {
		fmt.Fprintln(output, "Credential status: logged out")
	} else {
		fmt.Fprintf(output, "Credential ID: %s\n", credential.CredentialID)
		fmt.Fprintf(output, "Expires: %s\n", formatControlPlaneAuthTime(credential.ExpiresAt))
		if !credential.ExpiresAt.After(time.Now().UTC()) {
			fmt.Fprintln(output, "Credential status: expired")
			writeControlPlanePrincipal(output, credential.Principal)
		} else {
			principal, validateErr := userauth.ValidateCredential(ctx, server, credential.BearerToken, config.HTTPClient)
			switch {
			case validateErr == nil:
				fmt.Fprintln(output, "Credential status: valid")
				writeControlPlanePrincipal(output, principal)
			case isControlPlaneAuthUnauthorized(validateErr):
				fmt.Fprintln(output, "Credential status: invalid or revoked")
				writeControlPlanePrincipal(output, credential.Principal)
			default:
				return errors.Wrap(validateErr, "failed to validate control-plane credential")
			}
		}
	}

	if pendingFound {
		pendingStatus := "pending"
		if !pending.ExpiresAt.After(time.Now().UTC()) {
			pendingStatus = "expired"
		}
		fmt.Fprintf(output, "Pending login status: %s\n", pendingStatus)
		fmt.Fprintf(output, "Pending login code: %s\n", pending.UserCode)
		fmt.Fprintf(output, "Pending verification URL: %s\n", pending.VerificationURL)
		fmt.Fprintf(output, "Pending login expires: %s\n", formatControlPlaneAuthTime(pending.ExpiresAt))
	}
	return nil
}

// resolveControlPlaneAuthToken resolves control-plane API authentication without exposing stored credentials.
func resolveControlPlaneAuthToken(cmd *cobra.Command, server string) (token string, source string, err error) {
	if cmd != nil && cmd.Flags().Changed("auth-token") {
		value, flagErr := cmd.Flags().GetString("auth-token")
		if flagErr != nil {
			return "", controlPlaneAuthTokenSourceFlag, errors.Wrap(flagErr, "failed to read --auth-token")
		}
		return strings.TrimSpace(value), controlPlaneAuthTokenSourceFlag, nil
	}
	if environment := strings.TrimSpace(os.Getenv(controlPlaneAuthTokenEnv)); environment != "" {
		return environment, controlPlaneAuthTokenSourceEnvironment, nil
	}

	canonicalServer, store, err := prepareControlPlaneAuthState(server, nil)
	if err != nil {
		return "", controlPlaneAuthTokenSourceNone, err
	}
	credential, found, err := store.LoadCredential(canonicalServer)
	if err != nil {
		return "", controlPlaneAuthTokenSourceStored, errors.Wrap(err, "failed to load local control-plane credential")
	}
	if !found {
		return "", controlPlaneAuthTokenSourceNone, nil
	}
	if !credential.ExpiresAt.After(time.Now().UTC()) {
		return "", controlPlaneAuthTokenSourceStored, errors.Errorf("stored control-plane credential for %s expired at %s; run `kodelet auth login --server %s`", canonicalServer, formatControlPlaneAuthTime(credential.ExpiresAt), canonicalServer)
	}
	return credential.BearerToken, controlPlaneAuthTokenSourceStored, nil
}

func prepareControlPlaneAuthState(server string, store *userauth.Store) (string, *userauth.Store, error) {
	canonicalServer, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return "", nil, errors.Wrap(err, "invalid control-plane URL")
	}
	if store == nil {
		store, err = userauth.NewStore()
		if err != nil {
			return "", nil, errors.Wrap(err, "failed to initialize local control-plane authentication state")
		}
	}
	return canonicalServer, store, nil
}

func isControlPlaneAuthUnauthorized(err error) bool {
	var apiErr *userauth.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized
}

func controlPlaneAuthOutput(output io.Writer) io.Writer {
	if output == nil {
		return io.Discard
	}
	return output
}

func writeControlPlaneCredential(output io.Writer, credentialID string, principal userauth.PrincipalSnapshot, expiresAt time.Time) {
	fmt.Fprintf(output, "Credential ID: %s\n", credentialID)
	writeControlPlanePrincipal(output, principal)
	fmt.Fprintf(output, "Expires: %s\n", formatControlPlaneAuthTime(expiresAt))
}

func writeControlPlanePrincipal(output io.Writer, principal userauth.PrincipalSnapshot) {
	fmt.Fprintf(output, "Principal: %s\n", principal.ID)
	if principal.Name != "" {
		fmt.Fprintf(output, "Name: %s\n", principal.Name)
	}
	email := principal.Email
	if email == "" {
		email = "(not provided)"
	}
	roles := "(none)"
	if len(principal.Roles) > 0 {
		roles = strings.Join(principal.Roles, ", ")
	}
	fmt.Fprintf(output, "Email: %s\n", email)
	fmt.Fprintf(output, "Roles: %s\n", roles)
}

func formatControlPlaneAuthTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}
