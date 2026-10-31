package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	"github.com/OneDayX/gophkeeper/internal/client/view"
)

// printEntry prints an entry field by field. The secrets are hidden unless
// reveal is set; an OTP entry shows the code valid at now.
func printEntry(w io.Writer, e vault.Entry, reveal bool, now time.Time) error {
	fields, masked := view.Fields(e, reveal, now)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, f := range fields {
		if !f.Multiline {
			fmt.Fprintf(tw, "%s:\t%s\n", f.Label, f.Value)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	// Values of many lines go below the table.
	for _, f := range fields {
		if f.Multiline {
			fmt.Fprintf(w, "\n%s:\n%s\n", f.Label, f.Value)
		}
	}

	if _, ok := e.Secret.Payload.(models.Binary); ok {
		fmt.Fprintln(w, "\nSave the file with --out FILE.")
	}
	if masked {
		fmt.Fprintln(w, "\nSecrets are hidden; show them with --reveal.")
	}

	return nil
}

// printList prints the entries as a table, only those of the type unless
// it is zero.
func printList(w io.Writer, entries []vault.Entry, filter models.ItemType) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tNAME\tUPDATED")

	shown := 0
	for _, e := range entries {
		if filter != 0 && e.Secret.Type() != filter {
			continue
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s", e.ID[:8], e.Secret.Type(), e.Secret.Name, view.Time(e.UpdatedAt))
		if e.Pending {
			fmt.Fprint(tw, "\tnot synced")
		}
		fmt.Fprintln(tw)
		shown++
	}

	if shown == 0 {
		_, err := fmt.Fprintln(w, `No entries. Add one with "gophkeeper add".`)
		return err
	}

	return tw.Flush()
}
