package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/muesli/cancelreader"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var anthropicLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Connect an Anthropic subscription account to the daemon",
	Long:  "Open the daemon's authorization URL and paste the resulting code. The daemon exchanges and stores credentials. An omitted alias is derived from the account email.",
	Args:  cobra.NoArgs,
	RunE:  runRemoteAnthropicLogin,
}

func init() {
	anthropicLoginCmd.Flags().String("alias", "", "Account alias (default: email prefix)")
	anthropicLoginCmd.Flags().Bool("no-browser", false, "Print the authorization URL without opening a browser")
}

func runRemoteAnthropicLogin(cmd *cobra.Command, _ []string) error {
	alias, _ := cmd.Flags().GetString("alias")
	if alias != "" {
		if err := auth.ValidateAlias(alias); err != nil {
			return err
		}
	}
	client, err := remoteAdministrationClient(cmd)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	login, err := client.StartAnthropicLogin(ctx)
	if err != nil {
		return err
	}
	if login.ID == "" || login.Status != "pending" || login.AuthorizationURL == "" {
		return errors.New("daemon returned an invalid pending login; inspect daemon state before retrying")
	}
	completed := false
	defer func() {
		if !completed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := client.CancelAnthropicLogin(cleanup, login.ID); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "Could not cancel daemon login; inspect daemon state:", err)
			}
		}
	}()
	fmt.Fprintln(cmd.OutOrStdout(), "Authorize this daemon account at:", login.AuthorizationURL)
	noBrowser, _ := cmd.Flags().GetBool("no-browser")
	if !noBrowser {
		if err := osutil.OpenBrowser(login.AuthorizationURL); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "Could not open browser; visit the URL above.")
		}
	}
	fmt.Fprint(cmd.OutOrStdout(), "Enter the authorization code: ")
	code, err := readProviderInput(ctx, cmd.InOrStdin())
	if err != nil {
		return err
	}
	result, err := client.CompleteAnthropicLogin(ctx, login.ID, strings.TrimSpace(code), alias)
	if err != nil {
		return err
	}
	completed = true
	if result.Status != "connected" {
		return errors.Errorf("daemon login %s: %s", result.Status, result.Message)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), "Anthropic account connected on the daemon. Use 'kodelet anthropic accounts list' to inspect it.")
	return err
}

// CLI stdin is a cancellable file; finite in-memory readers also support tests.
func readProviderInput(ctx context.Context, input io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, ok := input.(cancelreader.File); ok {
		reader, err := cancelreader.NewReader(input)
		if err != nil {
			return "", err
		}
		defer reader.Close()
		finished := make(chan struct{})
		stop := context.AfterFunc(ctx, func() { reader.Cancel(); close(finished) })
		defer func() {
			if !stop() {
				<-finished
			}
		}()
		input = reader
	}
	line, err := bufio.NewReader(io.LimitReader(input, 8194)).ReadString('\n')
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if len(line) > 8193 {
		return "", errors.New("provider input exceeds 8192 bytes")
	}
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
