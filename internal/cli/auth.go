package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/xnmp/gtasks-cli/internal/auth"
)

// AuthEnv locates credential files; resolved lazily so commands that fail to
// find a config dir only fail when they actually need it.
type AuthEnv struct {
	Paths func() (auth.Paths, error)
}

func authCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in to or out of Google",
	}
	var noBrowser bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Authorise gtasks with your Google account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := env.Auth.Paths()
			if err != nil {
				return err
			}
			cfg, err := auth.LoadConfig(paths.Credentials)
			if err != nil {
				return err
			}
			opts := auth.LoginOptions{Out: env.Err, Timeout: 5 * time.Minute, OpenBrowser: openBrowser}
			if noBrowser {
				opts.OpenBrowser = nil
			}
			// Accepting a pasted redirect URL rescues logins where the browser
			// runs on another machine or no opener exists.
			if noBrowser || env.IsTerminal() {
				opts.Paste = env.In
			}
			tok, err := auth.Login(cmd.Context(), cfg, opts)
			if err != nil {
				return err
			}
			if err := auth.SaveToken(paths.Token, tok); err != nil {
				return err
			}
			fmt.Fprintf(env.Out, "Logged in. Token saved to %s\n", paths.Token)
			return nil
		},
	}
	login.Flags().BoolVar(&noBrowser, "no-browser", false,
		"don't open a browser; print the URL and accept the pasted redirect URL (for SSH/headless)")

	logout := &cobra.Command{
		Use:   "logout",
		Short: "Revoke and delete the saved token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := env.Auth.Paths()
			if err != nil {
				return err
			}
			tok, err := auth.LoadToken(paths.Token)
			if errors.Is(err, auth.ErrNotLoggedIn) {
				fmt.Fprintln(env.Out, "Not logged in.")
				return nil
			}
			if err == nil {
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				defer cancel()
				if rerr := auth.Revoke(ctx, tok); rerr != nil {
					fmt.Fprintf(env.Err, "warning: could not revoke token at Google: %v\n", rerr)
				}
			}
			if err := os.Remove(paths.Token); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete token: %w", err)
			}
			fmt.Fprintln(env.Out, "Logged out.")
			return nil
		},
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show where credentials live and whether you are logged in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := env.Auth.Paths()
			if err != nil {
				return err
			}
			fmt.Fprintf(env.Out, "OAuth client: %s (%s)\n", paths.Credentials, presence(paths.Credentials))
			fmt.Fprintf(env.Out, "Token:        %s (%s)\n", paths.Token, presence(paths.Token))
			if _, err := auth.LoadToken(paths.Token); err != nil {
				fmt.Fprintf(env.Out, "Status:       %v\n", err)
				return nil
			}
			fmt.Fprintln(env.Out, "Status:       token saved (validity is checked on the next request)")
			return nil
		},
	}

	cmd.AddCommand(login, logout, status)
	return cmd
}

func presence(path string) string {
	if _, err := os.Stat(path); err != nil {
		return "missing"
	}
	return "present"
}

// openBrowser opens url with whatever opener this platform provides,
// detected at runtime (wslview on WSL, xdg-open on Linux/BSD).
func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		// explorer.exe is reachable from WSL via interop when wslu is absent.
		for _, c := range []string{"wslview", "xdg-open", "explorer.exe"} {
			if _, err := exec.LookPath(c); err == nil {
				name = c
				break
			}
		}
		if name == "" {
			return errors.New("no browser opener found; open the URL yourself")
		}
	}
	return exec.Command(name, append(args, url)...).Start()
}
