package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/OneDayX/gophkeeper/internal/client/app"
	"github.com/OneDayX/gophkeeper/internal/client/view"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// errPasswordsDiffer means the repeated master password is not the same.
var errPasswordsDiffer = errors.New("the passwords do not match")

// registerCommand creates an account.
func (c *cli) registerCommand() *cobra.Command {
	var login string

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Create an account on the server and log in to it",
		Long: `Create an account on the server and log in to it.

The master password is asked for twice and never leaves this device: the
server gets a key derived from it. It cannot be recovered: forget it, and
the secrets are lost.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			login, err := c.askLogin(cmd, login)
			if err != nil {
				return err
			}

			password, err := c.readPassword(cmd, "Master password: ")
			if err != nil {
				return err
			}
			// Checked before the second prompt, not to make one type it twice
			// in vain; Register checks it again.
			if len([]rune(password)) < app.MinPasswordLength {
				return app.ErrWeakPassword
			}
			again, err := c.readPassword(cmd, "Repeat the master password: ")
			if err != nil {
				return err
			}
			if password != again {
				return errPasswordsDiffer
			}

			return c.withApp(func(a *app.App) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
				defer cancel()

				if err := a.Register(ctx, login, password); err != nil {
					return err
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Registered and logged in as %s at %s.\n", login, a.Server())
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&login, "login", "l", "", "login of the new account (asked for if not set)")

	return cmd
}

// loginCommand logs in and fetches the secrets.
func (c *cli) loginCommand() *cobra.Command {
	var login string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to an existing account and fetch its secrets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			login, err := c.askLogin(cmd, login)
			if err != nil {
				return err
			}

			password, err := c.readPassword(cmd, "Master password: ")
			if err != nil {
				return err
			}

			return c.withApp(func(a *app.App) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
				defer cancel()

				if err := a.Login(ctx, login, password); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s at %s.\n", login, a.Server())

				v, err := a.Vault()
				if err != nil {
					return err
				}
				result, err := v.Sync(ctx)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Could not sync: %v\n", err)
					return nil
				}

				fmt.Fprintln(cmd.OutOrStdout(), view.Sync(result))
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&login, "login", "l", "", "login of the account (asked for if not set)")

	return cmd
}

// logoutCommand forgets the session.
func (c *cli) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out: remove the token and the key from this device",
		Long: `Log out: remove the token and the encryption key from this device.

The local vault stays, encrypted; changes that have not been synced yet are
sent after the next login of the same user on this device.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.withApp(func(a *app.App) error {
				if !a.LoggedIn() {
					fmt.Fprintln(cmd.OutOrStdout(), "Not logged in.")
					return nil
				}

				// A vault held by a running TUI cannot be checked; that is fine.
				if v, err := a.Vault(); err == nil {
					if pending, err := v.Pending(); err == nil && pending > 0 {
						fmt.Fprintf(cmd.ErrOrStderr(),
							"%d change(s) are not synced yet; they will be sent after you log in again on this device.\n", pending)
					}
				}

				user := a.User()
				if err := a.Logout(); err != nil {
					return err
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Logged out %s.\n", user)
				return nil
			})
		},
	}
}

// statusCommand tells who is logged in and what is not synced.
func (c *cli) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show who is logged in and how many changes are not synced",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.withApp(func(a *app.App) error {
				out := cmd.OutOrStdout()
				if !a.LoggedIn() {
					fmt.Fprintf(out, "Not logged in. Server: %s\nData directory: %s\n", a.Server(), c.cfg.DataDir)
					return nil
				}

				v, err := a.Vault()
				if err != nil {
					return err
				}
				entries, err := v.List()
				if err != nil {
					return err
				}
				pending, err := v.Pending()
				if err != nil {
					return err
				}

				fmt.Fprintf(out, "Logged in as %s at %s\n", a.User(), a.Server())
				fmt.Fprintf(out, "Entries: %d, changes not synced: %d\n", len(entries), pending)
				fmt.Fprintf(out, "Data directory: %s\n", c.cfg.DataDir)
				return nil
			})
		},
	}
}

// askLogin returns the login from the flag, or asks for it.
func (c *cli) askLogin(cmd *cobra.Command, login string) (string, error) {
	if login == "" {
		c.prompt(cmd, "Login: ")

		var err error
		if login, err = c.readLine(cmd); err != nil {
			return "", err
		}
	}

	login = strings.TrimSpace(login)
	if login == "" {
		return "", app.ErrEmptyCredentials
	}

	return login, nil
}

// readPassword asks for a password without echoing it when the input is a
// terminal, and reads a line otherwise, so that scripts can pipe it in.
func (c *cli) readPassword(cmd *cobra.Command, prompt string) (string, error) {
	if f, ok := terminal(cmd.InOrStdin()); ok {
		fmt.Fprint(cmd.ErrOrStderr(), prompt)
		password, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}

		return string(password), nil
	}

	return c.readLine(cmd)
}

// prompt shows a prompt when a person is typing the input.
func (c *cli) prompt(cmd *cobra.Command, text string) {
	if _, ok := terminal(cmd.InOrStdin()); ok {
		fmt.Fprint(cmd.ErrOrStderr(), text)
	}
}

// readLine reads one line of the input without the line break.
func (c *cli) readLine(cmd *cobra.Command) (string, error) {
	if c.in == nil {
		c.in = bufio.NewReader(cmd.InOrStdin())
	}

	line, err := c.in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", fmt.Errorf("read input: %w", err)
	}

	return strings.TrimRight(line, "\r\n"), nil
}

// terminal returns the input as a file if it is a terminal.
func terminal(in io.Reader) (*os.File, bool) {
	f, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil, false
	}

	return f, true
}
