package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/lumen-fx/registry/cli/internal"
	"github.com/spf13/cobra"
)

var loginRegistry string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Save an API token for publishing",
	Long: `Reads an API token and saves it for the publish and release commands.

Mint the token in the registry's web UI: sign in with GitHub, open Account,
and create a token. Then paste it here.

A job with no terminal to paste into sets LPM_TOKEN instead, and every command
that needs a token uses it. A token saved here outranks LPM_TOKEN.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(cmd.OutOrStdout(), "Paste an API token from %s (mint one under Account):\n", loginRegistry)

		scanner := bufio.NewScanner(cmd.InOrStdin())
		if !scanner.Scan() {
			return fmt.Errorf("no token was entered")
		}
		token := strings.TrimSpace(scanner.Text())
		if token == "" {
			return fmt.Errorf("no token was entered")
		}

		cfg := internal.Config{Registry: strings.TrimSuffix(loginRegistry, "/"), Token: token}
		user, err := internal.NewClient(cfg).Me()
		if err != nil {
			return fmt.Errorf("the token did not authenticate: %w", err)
		}

		if err := internal.SaveConfig(cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Signed in to %s as %s\n", cfg.Registry, user.Username)
		return nil
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Forget the saved API token",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := internal.DeleteConfig(); err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Signed out")
		if internal.EnvToken() != "" {
			fmt.Fprintln(out, "LPM_TOKEN is still set and still signs you in; unset it to finish.")
		}
		return nil
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show which account the token belongs to",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := authedClient()
		if err != nil {
			return err
		}

		user, err := client.Me()
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s on %s\n", user.Username, client.Registry)
		return nil
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginRegistry, "registry", internal.DefaultRegistry, "registry to sign in to")
	rootCmd.AddCommand(loginCmd, logoutCmd, whoamiCmd)
}
