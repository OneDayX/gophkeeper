// Package config reads the GophKeeper server settings from command-line
// flags and environment variables. A variable that is set wins over its flag.
package config

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	// defaultGRPCAddress is where the gRPC server listens unless told
	// otherwise.
	defaultGRPCAddress = "localhost:3200"

	// defaultHTTPAddress is where the REST gateway listens unless told
	// otherwise.
	defaultHTTPAddress = "localhost:3201"

	// defaultTokenTTL is how long a user stays signed in.
	defaultTokenTTL = 24 * time.Hour
)

var (
	// ErrNoGRPCAddress means the gRPC address is set to an empty string.
	ErrNoGRPCAddress = errors.New("gRPC address must not be empty")

	// ErrTLSPair means only one of the TLS certificate and key is set.
	ErrTLSPair = errors.New("TLS needs both a certificate and a key: set TLS_CERT and TLS_KEY or -tls-cert and -tls-key")

	// ErrBadTokenTTL means the token lifetime is not positive.
	ErrBadTokenTTL = errors.New("token TTL must be positive")
)

// Config holds the server settings.
type Config struct {
	// GRPCAddress is the host:port the gRPC server listens on.
	GRPCAddress string `env:"GRPC_ADDRESS"`
	// HTTPAddress is the host:port of the REST gateway and the Swagger
	// description. An empty address turns the gateway off.
	HTTPAddress string `env:"HTTP_ADDRESS"`
	// DatabaseDSN is the PostgreSQL connection string. When it is empty, the
	// data is kept in memory and lost on restart.
	DatabaseDSN string `env:"DATABASE_DSN"`
	// AuthKey signs the auth tokens. When it is empty, a random key is made
	// on every start, so tokens do not survive a restart.
	AuthKey string `env:"AUTH_KEY"`
	// TokenTTL is how long an auth token stays valid.
	TokenTTL time.Duration `env:"TOKEN_TTL"`
	// TLSCertFile is the PEM certificate of the server. With TLSKeyFile it
	// turns TLS on for both gRPC and the gateway.
	TLSCertFile string `env:"TLS_CERT"`
	// TLSKeyFile is the PEM private key of TLSCertFile.
	TLSKeyFile string `env:"TLS_KEY"`
}

// TLSEnabled reports whether the server is configured to serve over TLS.
func (c Config) TLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// Load parses the command-line arguments, without the program name, and then
// the environment. It fails on an unknown flag and on an inconsistent
// setting; -h makes it return flag.ErrHelp after printing the usage.
func Load(args []string) (Config, error) {
	cfg := Config{
		GRPCAddress: defaultGRPCAddress,
		HTTPAddress: defaultHTTPAddress,
		TokenTTL:    defaultTokenTTL,
	}

	fs := flag.NewFlagSet("gophkeeper-server", flag.ContinueOnError)
	fs.StringVar(&cfg.GRPCAddress, "a", cfg.GRPCAddress, "address of the gRPC server (host:port)")
	fs.StringVar(&cfg.HTTPAddress, "http", cfg.HTTPAddress, "address of the REST gateway (host:port), empty to turn it off")
	fs.StringVar(&cfg.DatabaseDSN, "d", cfg.DatabaseDSN, "PostgreSQL connection string (in-memory storage if empty)")
	fs.StringVar(&cfg.AuthKey, "k", cfg.AuthKey, "key to sign auth tokens with (random if empty)")
	fs.DurationVar(&cfg.TokenTTL, "token-ttl", cfg.TokenTTL, "how long an auth token is valid")
	fs.StringVar(&cfg.TLSCertFile, "tls-cert", cfg.TLSCertFile, "PEM certificate of the server, turns TLS on")
	fs.StringVar(&cfg.TLSKeyFile, "tls-key", cfg.TLSKeyFile, "PEM private key of the server certificate")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment variables: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validate checks the settings that depend on each other.
func (c Config) validate() error {
	switch {
	case c.GRPCAddress == "":
		return ErrNoGRPCAddress
	case (c.TLSCertFile == "") != (c.TLSKeyFile == ""):
		return ErrTLSPair
	case c.TokenTTL <= 0:
		return ErrBadTokenTTL
	default:
		return nil
	}
}
