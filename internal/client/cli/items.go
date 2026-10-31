package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/OneDayX/gophkeeper/internal/client/view"
	"github.com/spf13/cobra"
)

// syncCommand syncs the vault with the server.
func (c *cli) syncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Send the local changes to the server and fetch the changes from other devices",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.withVault(func(v *vault.Vault) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
				defer cancel()

				result, err := v.Sync(ctx)
				if err != nil {
					return err
				}

				fmt.Fprintln(cmd.OutOrStdout(), view.Sync(result))
				return nil
			})
		},
	}
}

// listCommand prints the entries.
func (c *cli) listCommand() *cobra.Command {
	var typeName string

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the entries of the vault",
		Long: `List the entries of the vault. The secrets are not shown; use
"gophkeeper show" for that. The list comes from this device: run
"gophkeeper sync" first to see the changes made on other devices.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var filter models.ItemType
			if typeName != "" {
				var err error
				if filter, err = models.ParseItemType(typeName); err != nil {
					return err
				}
			}

			return c.withVault(func(v *vault.Vault) error {
				entries, err := v.List()
				if err != nil {
					return err
				}

				return printList(cmd.OutOrStdout(), entries, filter)
			})
		},
	}
	cmd.Flags().StringVarP(&typeName, "type", "t", "", "show only the entries of this type: credentials, text, binary, card or otp")

	return cmd
}

// showCommand prints one entry.
func (c *cli) showCommand() *cobra.Command {
	var (
		reveal bool
		out    string
	)

	cmd := &cobra.Command{
		Use:     "show <id or name>",
		Aliases: []string{"get"},
		Short:   "Show an entry; the secrets are hidden unless --reveal is set",
		Long: `Show an entry. The entry is found by its ID, the beginning of its ID
or its name. Passwords, card numbers and other secrets are hidden unless
--reveal is set. The data of a binary entry is saved with --out.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.withVault(func(v *vault.Vault) error {
				entry, err := v.Find(args[0])
				if err != nil {
					return err
				}

				if out != "" {
					return saveBinary(cmd, entry, out)
				}

				return printEntry(cmd.OutOrStdout(), entry, reveal, time.Now())
			})
		},
	}
	cmd.Flags().BoolVarP(&reveal, "reveal", "r", false, "show the secrets")
	cmd.Flags().StringVarP(&out, "out", "o", "", `save the data of a binary entry to this file, "-" for stdout`)

	return cmd
}

// otpCommand prints the current one-time password.
func (c *cli) otpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "otp <id or name>",
		Short: "Print the current one-time password of an OTP entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.withVault(func(v *vault.Vault) error {
				entry, err := v.Find(args[0])
				if err != nil {
					return err
				}

				generator, ok := entry.Secret.Payload.(models.OTP)
				if !ok {
					return fmt.Errorf("%s is a %s entry, not an OTP one", entry.Secret.Name, entry.Secret.Type())
				}

				code, remaining, err := generator.Code(time.Now())
				if err != nil {
					return err
				}

				// The code alone goes to stdout, so that scripts can take it.
				fmt.Fprintln(cmd.OutOrStdout(), code)
				fmt.Fprintf(cmd.ErrOrStderr(), "valid for %s\n", view.Remaining(remaining))
				return nil
			})
		},
	}
}

// addCommand adds an entry; it has a subcommand per type.
func (c *cli) addCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add an entry: credentials, text, binary, card or otp",
	}

	descriptions := map[models.ItemType]string{
		models.ItemTypeCredentials: "Add a login and password pair; the password is asked for if not set",
		models.ItemTypeText:        "Add arbitrary text",
		models.ItemTypeBinary:      "Add a file, up to 8 MiB",
		models.ItemTypeCard:        "Add bank card details",
		models.ItemTypeOTP:         "Add a generator of one-time passwords (TOTP)",
	}

	for _, t := range models.ItemTypes {
		var f secretFlags

		sub := &cobra.Command{
			Use:   t.String(),
			Short: descriptions[t],
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				payload, err := c.payload(cmd, &f, t, nil)
				if err != nil {
					return err
				}
				s := models.Secret{Name: f.name, Metadata: f.meta, Payload: payload}

				return c.withVault(func(v *vault.Vault) error {
					entry, err := v.Add(s)
					if err != nil {
						return err
					}

					fmt.Fprintf(cmd.OutOrStdout(), "Added %s (%s).\n", entry.Secret.Name, entry.ID[:8])
					c.syncAfterChange(cmd, v)
					return nil
				})
			},
		}
		f.bindCommon(sub.Flags())
		f.bind(sub.Flags(), t)
		_ = sub.MarkFlagRequired("name")

		cmd.AddCommand(sub)
	}

	return cmd
}

// editCommand changes an entry.
func (c *cli) editCommand() *cobra.Command {
	var f secretFlags

	cmd := &cobra.Command{
		Use:   "edit <id or name>",
		Short: "Change an entry; only the flags given change it",
		Long: `Change an entry. Only the flags given on the command line change it,
and only the flags of its type apply: --login and --password to credentials,
--text to text, --file to binary, --number, --holder, --expiry and --cvv to
a card, --secret, --uri, --issuer, --account, --algorithm, --digits and
--period to OTP. --name and --meta apply to all.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !localFlagsSet(cmd) {
				return errors.New("nothing to change: give the flags of the fields to change")
			}

			return c.withVault(func(v *vault.Vault) error {
				entry, err := v.Find(args[0])
				if err != nil {
					return err
				}

				t := entry.Secret.Type()
				if err := checkFlags(cmd, t); err != nil {
					return err
				}

				s := entry.Secret
				if cmd.Flags().Changed("name") {
					s.Name = f.name
				}
				if cmd.Flags().Changed("meta") {
					s.Metadata = f.meta
				}
				if s.Payload, err = c.payload(cmd, &f, t, entry.Secret.Payload); err != nil {
					return err
				}

				updated, err := v.Update(entry.ID, s)
				if err != nil {
					return err
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Updated %s (%s).\n", updated.Secret.Name, updated.ID[:8])
				c.syncAfterChange(cmd, v)
				return nil
			})
		},
	}
	f.bindCommon(cmd.Flags())
	for _, t := range models.ItemTypes {
		f.bind(cmd.Flags(), t)
	}

	return cmd
}

// deleteCommand deletes an entry.
func (c *cli) deleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <id or name>",
		Aliases: []string{"rm"},
		Short:   "Delete an entry on all devices",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.withVault(func(v *vault.Vault) error {
				entry, err := v.Find(args[0])
				if err != nil {
					return err
				}

				if err := v.Delete(entry.ID); err != nil {
					return err
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s (%s).\n", entry.Secret.Name, entry.ID[:8])
				c.syncAfterChange(cmd, v)
				return nil
			})
		},
	}
}

// saveBinary writes the data of a binary entry to a file, or to stdout for
// "-".
func saveBinary(cmd *cobra.Command, entry vault.Entry, path string) error {
	binary, ok := entry.Secret.Payload.(models.Binary)
	if !ok {
		return fmt.Errorf("%s is a %s entry, only binary entries can be saved to a file", entry.Secret.Name, entry.Secret.Type())
	}

	if path == "-" {
		_, err := cmd.OutOrStdout().Write(binary.Data)
		return err
	}

	// WriteFile keeps the permissions of an existing file, so tighten them too.
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(path, binary.Data, 0o600); err != nil {
		return err
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Saved %d bytes to %s.\n", len(binary.Data), path)
	return nil
}
