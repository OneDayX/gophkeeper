// Package cli is the command-line interface of the GophKeeper client, built
// with cobra. Every command is a short run: it loads the session, opens the
// vault, does its job and exits. `gophkeeper tui` starts the interactive
// terminal interface instead.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/app"
	"github.com/OneDayX/gophkeeper/internal/client/tui"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/spf13/cobra"
)

// Environment variables that set the defaults of the global flags.
const (
	envServer   = "GOPHKEEPER_SERVER"
	envCACert   = "GOPHKEEPER_CA_CERT"
	envInsecure = "GOPHKEEPER_INSECURE"
	envDataDir  = "GOPHKEEPER_DATA_DIR"
)

const (
	// defaultServer is the server address unless told otherwise.
	defaultServer = "localhost:3200"

	// requestTimeout bounds the talk of one command to the server.
	requestTimeout = time.Minute
)

// BuildInfo is the version information of the binary, set at build time.
type BuildInfo struct {
	// Version is the version, such as v1.0.0.
	Version string
	// Date is when the binary was built.
	Date string
	// Commit is the commit the binary was built from.
	Commit string
}

// String formats the build info for `gophkeeper version`.
func (b BuildInfo) String() string {
	return fmt.Sprintf("GophKeeper client %s\nBuild date: %s\nBuild commit: %s\n", b.Version, b.Date, b.Commit)
}

// tuiRunner starts the interactive interface; tests replace it.
type tuiRunner func(ctx context.Context, a *app.App) error

// cli holds what the commands share: the settings from the global flags and
// the input the prompts read.
type cli struct {
	info    BuildInfo
	cfg     app.Config
	offline bool
	runTUI  tuiRunner

	// in reads the lines of the input; it is made on the first prompt, so
	// that all prompts of a command share its buffer.
	in *bufio.Reader
}

// NewRootCommand returns the gophkeeper command with all its subcommands.
func NewRootCommand(info BuildInfo) *cobra.Command {
	return newRootCommand(info, tui.Run)
}

// newRootCommand is NewRootCommand with the TUI to start.
func newRootCommand(info BuildInfo, runTUI tuiRunner) *cobra.Command {
	c := &cli{info: info, runTUI: runTUI}

	root := &cobra.Command{
		Use:   "gophkeeper",
		Short: "GophKeeper keeps your passwords, texts, files, bank cards and OTP secrets",
		Long: `GophKeeper keeps your logins and passwords, texts, files, bank cards and
OTP generators, encrypted with a key derived from your master password, and
syncs them between your devices through the GophKeeper server.

The master password never leaves the device: the server stores only data it
cannot decrypt. Changes are saved locally first and sent to the server at
once, or with the next sync if the server cannot be reached.

Start with "gophkeeper register" or "gophkeeper login", or run
"gophkeeper tui" for the interactive interface.`,
		Version:       info.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(info.String())

	flags := root.PersistentFlags()
	flags.StringVar(&c.cfg.Server, "server", envOr(envServer, defaultServer),
		"server address (host:port) to register and log in at; env "+envServer)
	flags.StringVar(&c.cfg.CACert, "ca-cert", os.Getenv(envCACert),
		"PEM file of the CA that signed the server certificate; env "+envCACert)
	flags.BoolVar(&c.cfg.Insecure, "insecure", envBool(envInsecure),
		"connect to a remote server without TLS, for debugging only (a server on this machine needs no TLS); env "+envInsecure)
	flags.StringVar(&c.cfg.DataDir, "data-dir", envOr(envDataDir, defaultDataDir()),
		"directory for the session and the local vault; env "+envDataDir)
	flags.BoolVar(&c.offline, "offline", false,
		"do not sync after a change, keep it on this device until the next sync")

	root.AddCommand(
		c.versionCommand(),
		c.registerCommand(),
		c.loginCommand(),
		c.logoutCommand(),
		c.statusCommand(),
		c.syncCommand(),
		c.listCommand(),
		c.showCommand(),
		c.otpCommand(),
		c.addCommand(),
		c.editCommand(),
		c.deleteCommand(),
		c.tuiCommand(),
	)

	return root
}

// versionCommand prints the build info.
func (c *cli) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and the build date of the client",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), c.info.String())
			return err
		},
	}
}

// tuiCommand starts the interactive interface.
func (c *cli) tuiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Start the interactive terminal interface",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.withApp(func(a *app.App) error {
				return c.runTUI(cmd.Context(), a)
			})
		},
	}
}

// withApp runs fn with the client and closes it afterwards.
func (c *cli) withApp(fn func(a *app.App) error) error {
	a, err := app.New(c.cfg)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	return fn(a)
}

// withVault runs fn with the vault of the logged-in user.
func (c *cli) withVault(fn func(v *vault.Vault) error) error {
	return c.withApp(func(a *app.App) error {
		v, err := a.Vault()
		if err != nil {
			return err
		}

		return fn(v)
	})
}

// syncAfterChange sends a change to the server right away. The change is
// saved on the device either way, so a failure is only a warning.
func (c *cli) syncAfterChange(cmd *cobra.Command, v *vault.Vault) {
	if c.offline {
		return
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
	defer cancel()

	if _, err := v.Sync(ctx); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Saved on this device, but not synced: %v\nRun `gophkeeper sync` later.\n", err)
	}
}

// defaultDataDir returns the per-user configuration directory of the
// platform: ~/.config on Linux, ~/Library/Application Support on macOS and
// %AppData% on Windows.
func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "gophkeeper")
	}

	return ".gophkeeper"
}

// envOr returns the environment variable, or def if it is not set.
func envOr(key, def string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}

	return def
}

// envBool reports whether the environment variable is set to a true value
// such as 1 or true.
func envBool(key string) bool {
	value, err := strconv.ParseBool(os.Getenv(key))
	return err == nil && value
}
