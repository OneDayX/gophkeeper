package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// readFromInput is the flag value that makes a command read the value from
// the input instead: a prompt for a password, stdin for a text.
const readFromInput = "-"

// secretFlags are the flags add and edit fill a secret from.
type secretFlags struct {
	name string
	meta string

	login    string
	password string

	text string

	file string

	number string
	holder string
	expiry string
	cvv    string

	secret    string
	uri       string
	issuer    string
	account   string
	algorithm string
	digits    int
	period    int
}

// typeFlags lists the flags that belong to each type.
var typeFlags = map[models.ItemType][]string{
	models.ItemTypeCredentials: {"login", "password"},
	models.ItemTypeText:        {"text"},
	models.ItemTypeBinary:      {"file"},
	models.ItemTypeCard:        {"number", "holder", "expiry", "cvv"},
	models.ItemTypeOTP:         {"secret", "uri", "issuer", "account", "algorithm", "digits", "period"},
}

// bindCommon adds the flags all types share.
func (f *secretFlags) bindCommon(fs *pflag.FlagSet) {
	fs.StringVarP(&f.name, "name", "n", "", "name of the entry")
	fs.StringVarP(&f.meta, "meta", "m", "", "metadata: the site, bank or person, notes, activation codes...")
}

// bind adds the flags of the type.
func (f *secretFlags) bind(fs *pflag.FlagSet, t models.ItemType) {
	switch t {
	case models.ItemTypeCredentials:
		fs.StringVar(&f.login, "login", "", "login, email or phone of the account")
		fs.StringVar(&f.password, "password", "", `password; "-" asks for it (add asks when it is not set)`)
	case models.ItemTypeText:
		fs.StringVar(&f.text, "text", "", `the text; "-" reads it from stdin`)
	case models.ItemTypeBinary:
		fs.StringVar(&f.file, "file", "", "path of the file to keep, up to 8 MiB")
	case models.ItemTypeCard:
		fs.StringVar(&f.number, "number", "", "card number")
		fs.StringVar(&f.holder, "holder", "", "name on the card")
		fs.StringVar(&f.expiry, "expiry", "", "expiry date as MM/YY")
		fs.StringVar(&f.cvv, "cvv", "", "security code from the back of the card")
	case models.ItemTypeOTP:
		fs.StringVar(&f.secret, "secret", "", "shared secret in base32, as the service shows it")
		fs.StringVar(&f.uri, "uri", "", "otpauth:// URI from the QR code, instead of the separate flags")
		fs.StringVar(&f.issuer, "issuer", "", "service the codes are for")
		fs.StringVar(&f.account, "account", "", "account at the service")
		fs.StringVar(&f.algorithm, "algorithm", "", "SHA1, SHA256 or SHA512 (default SHA1)")
		fs.IntVar(&f.digits, "digits", 0, "length of a code, 6 to 8 (default 6)")
		fs.IntVar(&f.period, "period", 0, "seconds a code lives (default 30)")
	}
}

// localFlagsSet reports whether any flag of the command itself is set; the
// global flags, such as --server, do not count.
func localFlagsSet(cmd *cobra.Command) bool {
	set := false
	cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		set = set || f.Changed
	})

	return set
}

// checkFlags refuses the flags of other types than t.
func checkFlags(cmd *cobra.Command, t models.ItemType) error {
	for _, other := range models.ItemTypes {
		if other == t {
			continue
		}

		for _, name := range typeFlags[other] {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--%s does not apply to a %s entry", name, t)
			}
		}
	}

	return nil
}

// payload builds the payload of the type out of base and the flags given on
// the command line. base is nil when a new entry is added.
func (c *cli) payload(cmd *cobra.Command, f *secretFlags, t models.ItemType, base models.Payload) (models.Payload, error) {
	changed := cmd.Flags().Changed

	switch t {
	case models.ItemTypeCredentials:
		p, _ := base.(models.Credentials)
		if changed("login") {
			p.Login = f.login
		}
		switch {
		case (base == nil && !changed("password")) || f.password == readFromInput:
			password, err := c.readPassword(cmd, "Password: ")
			if err != nil {
				return nil, err
			}
			p.Password = password
		case changed("password"):
			p.Password = f.password
		}
		return p, nil

	case models.ItemTypeText:
		p, _ := base.(models.Text)
		if changed("text") {
			p.Content = f.text
			if f.text == readFromInput {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return nil, fmt.Errorf("read text: %w", err)
				}
				p.Content = strings.TrimSuffix(string(data), "\n")
			}
		}
		return p, nil

	case models.ItemTypeBinary:
		p, _ := base.(models.Binary)
		if changed("file") {
			var err error
			if p, err = models.BinaryFromFile(f.file); err != nil {
				return nil, err
			}
		}
		return p, nil

	case models.ItemTypeCard:
		p, _ := base.(models.Card)
		if changed("number") {
			p.Number = models.NormalizeCardNumber(f.number)
		}
		if changed("holder") {
			p.Holder = f.holder
		}
		if changed("expiry") {
			p.Expiry = f.expiry
		}
		if changed("cvv") {
			p.CVV = f.cvv
		}
		return p, nil

	case models.ItemTypeOTP:
		p, _ := base.(models.OTP)
		if changed("uri") {
			var err error
			if p, err = models.OTPFromURI(f.uri); err != nil {
				return nil, err
			}
		}
		if changed("secret") {
			p.Secret = f.secret
		}
		if changed("issuer") {
			p.Issuer = f.issuer
		}
		if changed("account") {
			p.Account = f.account
		}
		if changed("algorithm") {
			p.Algorithm = strings.ToUpper(f.algorithm)
		}
		if changed("digits") {
			p.Digits = f.digits
		}
		if changed("period") {
			p.Period = f.period
		}
		return p, nil

	default:
		return nil, fmt.Errorf("%w: %s", models.ErrUnknownType, t)
	}
}
