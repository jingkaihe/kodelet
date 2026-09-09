package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func runRemoteProviderDeviceLogin(cmd *cobra.Command, provider string) error {
	if cmd.Flags().Changed("device-auth") {
		if enabled, _ := cmd.Flags().GetBool("device-auth"); !enabled {
			return errors.New("sign-in uses a browser verification code; omit --device-auth=false")
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
	login, err := client.StartProviderDeviceLogin(ctx, provider)
	if err != nil {
		return err
	}
	if login.ID == "" {
		return errors.New("could not start sign-in because the server returned no login ID; check the server logs")
	}
	completed := false
	defer func() {
		if !completed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := client.CancelProviderDeviceLogin(cleanup, provider, login.ID); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "Could not cancel sign-in; check the server logs:", err)
			}
		}
	}()
	if login.Status != "pending" || login.VerificationURL == "" || login.UserCode == "" {
		return errors.New("could not start sign-in because the server returned incomplete verification details")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Open %s and enter code: %s\nNever share this device code. Waiting for sign-in...\n", login.VerificationURL, login.UserCode)
	noBrowser, _ := cmd.Flags().GetBool("no-browser")
	if !noBrowser {
		if err := osutil.OpenBrowser(login.VerificationURL); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "Could not open browser; visit the URL above.")
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		status, err := client.ProviderDeviceLogin(ctx, provider, login.ID)
		if err != nil {
			return err
		}
		switch status.Status {
		case "connected":
			completed = true
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s subscription connected.\n", provider)
			return err
		case "failed", "canceled":
			completed = true
			return errors.Errorf("sign-in %s: %s", status.Status, status.Message)
		case "pending", "starting":
		default:
			return errors.New("the server returned an unrecognized sign-in status; check the server logs")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
