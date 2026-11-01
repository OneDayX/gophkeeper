package config

import (
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(nil)
	require.NoError(t, err)

	assert.Equal(t, defaultGRPCAddress, cfg.GRPCAddress)
	assert.Equal(t, defaultHTTPAddress, cfg.HTTPAddress)
	assert.Equal(t, defaultTokenTTL, cfg.TokenTTL)
	assert.Empty(t, cfg.DatabaseDSN)
	assert.False(t, cfg.TLSEnabled())
}

func TestLoad_Flags(t *testing.T) {
	cfg, err := Load([]string{
		"-a", ":4000", "-http", "", "-d", "postgres://db", "-k", "key",
		"-token-ttl", "1h", "-tls-cert", "cert.pem", "-tls-key", "key.pem",
	})
	require.NoError(t, err)

	assert.Equal(t, Config{
		GRPCAddress: ":4000",
		HTTPAddress: "",
		DatabaseDSN: "postgres://db",
		AuthKey:     "key",
		TokenTTL:    time.Hour,
		TLSCertFile: "cert.pem",
		TLSKeyFile:  "key.pem",
	}, cfg)
	assert.True(t, cfg.TLSEnabled())
}

func TestLoad_EnvOverridesFlags(t *testing.T) {
	t.Setenv("GRPC_ADDRESS", "localhost:7000")
	t.Setenv("DATABASE_DSN", "postgres://env")
	t.Setenv("TOKEN_TTL", "30m")

	cfg, err := Load([]string{"-a", ":9090", "-d", "postgres://flag", "-token-ttl", "1h"})
	require.NoError(t, err)

	assert.Equal(t, "localhost:7000", cfg.GRPCAddress)
	assert.Equal(t, "postgres://env", cfg.DatabaseDSN)
	assert.Equal(t, 30*time.Minute, cfg.TokenTTL)
}

func TestLoad_Errors(t *testing.T) {
	_, err := Load([]string{"-a", ""})
	assert.ErrorIs(t, err, ErrNoGRPCAddress)

	_, err = Load([]string{"-tls-cert", "cert.pem"})
	assert.ErrorIs(t, err, ErrTLSPair)

	_, err = Load([]string{"-token-ttl", "0s"})
	assert.ErrorIs(t, err, ErrBadTokenTTL)

	_, err = Load([]string{"-unknown"})
	assert.Error(t, err)

	_, err = Load([]string{"-h"})
	assert.ErrorIs(t, err, flag.ErrHelp)

	t.Setenv("TOKEN_TTL", "soon")
	_, err = Load(nil)
	assert.Error(t, err)
}
