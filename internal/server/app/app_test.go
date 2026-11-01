package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/pb"
	"github.com/OneDayX/gophkeeper/internal/server/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// testConfig listens on free loopback ports and keeps the data in memory.
func testConfig() config.Config {
	return config.Config{
		GRPCAddress: "127.0.0.1:0",
		HTTPAddress: "127.0.0.1:0",
		AuthKey:     "test-key",
		TokenTTL:    time.Hour,
	}
}

// start runs the app until the test ends and waits for Run to return.
func start(t *testing.T, cfg config.Config) *App {
	t.Helper()

	a, err := New(context.Background(), cfg, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
		a.Close()
	})

	return a
}

// register signs a user up over gRPC.
func register(t *testing.T, addr string, creds credentials.TransportCredentials) {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	resp, err := pb.NewAuthServiceClient(conn).Register(context.Background(),
		&pb.RegisterRequest{Login: "alice", Password: "secret"})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetToken())
}

// post sends a JSON body and returns the status and the body.
func post(t *testing.T, client *http.Client, url, body string) (int, string) {
	t.Helper()

	resp, err := client.Post(url, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(data)
}

func TestApp(t *testing.T) {
	a := start(t, testConfig())

	register(t, a.GRPCAddr(), insecure.NewCredentials())

	// The gateway reaches the same server: the login is taken.
	code, _ := post(t, http.DefaultClient, "http://"+a.HTTPAddr()+"/api/v1/auth/register", `{"login":"alice","password":"secret"}`)
	assert.Equal(t, http.StatusConflict, code)

	code, body := post(t, http.DefaultClient, "http://"+a.HTTPAddr()+"/api/v1/auth/login", `{"login":"alice","password":"secret"}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "token")
}

func TestApp_WithoutGateway(t *testing.T) {
	cfg := testConfig()
	cfg.HTTPAddress = ""
	a := start(t, cfg)

	assert.Empty(t, a.HTTPAddr())
	register(t, a.GRPCAddr(), insecure.NewCredentials())
}

func TestApp_TLS(t *testing.T) {
	cfg := testConfig()
	cfg.TLSCertFile, cfg.TLSKeyFile = writeTestCert(t)
	a := start(t, cfg)

	pool := x509.NewCertPool()
	certPEM, err := os.ReadFile(cfg.TLSCertFile)
	require.NoError(t, err)
	require.True(t, pool.AppendCertsFromPEM(certPEM))
	tlsConfig := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}

	register(t, a.GRPCAddr(), credentials.NewTLS(tlsConfig))

	// The gateway serves HTTPS and calls gRPC over TLS itself.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	code, _ := post(t, client, "https://"+a.HTTPAddr()+"/api/v1/auth/login", `{"login":"alice","password":"secret"}`)
	assert.Equal(t, http.StatusOK, code)

	// A plain-text client is refused.
	conn, err := grpc.NewClient(a.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	_, err = pb.NewAuthServiceClient(conn).Login(context.Background(), &pb.LoginRequest{Login: "alice", Password: "secret"})
	assert.Error(t, err)
}

func TestApp_Postgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set, skipping database tests")
	}

	cfg := testConfig()
	cfg.DatabaseDSN = dsn
	a := start(t, cfg)
	require.NotNil(t, a.db)

	_, err := a.db.Pool().Exec(context.Background(), `TRUNCATE users, items RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	register(t, a.GRPCAddr(), insecure.NewCredentials())
}

func TestApp_RandomAuthKey(t *testing.T) {
	cfg := testConfig()
	cfg.AuthKey = ""
	a := start(t, cfg)

	register(t, a.GRPCAddr(), insecure.NewCredentials())
}

func TestNew_Errors(t *testing.T) {
	ctx := context.Background()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()

	cfg := testConfig()
	cfg.GRPCAddress = busy.Addr().String()
	_, err = New(ctx, cfg, zap.NewNop())
	assert.Error(t, err, "the gRPC port is taken")

	cfg = testConfig()
	cfg.HTTPAddress = busy.Addr().String()
	_, err = New(ctx, cfg, zap.NewNop())
	assert.Error(t, err, "the gateway port is taken")

	cfg = testConfig()
	cfg.TLSCertFile = filepath.Join(t.TempDir(), "missing.pem")
	cfg.TLSKeyFile = cfg.TLSCertFile
	_, err = New(ctx, cfg, zap.NewNop())
	assert.Error(t, err, "no certificate")

	cfg = testConfig()
	cfg.DatabaseDSN = "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"
	_, err = New(ctx, cfg, zap.NewNop())
	assert.Error(t, err, "no database")
}

func TestApp_ServeError(t *testing.T) {
	a, err := New(context.Background(), testConfig(), zap.NewNop())
	require.NoError(t, err)
	defer a.Close()

	// A closed listener makes Serve fail at once.
	require.NoError(t, a.grpcLis.Close())

	assert.Error(t, a.Run(context.Background()))
}

func TestGatewayTarget(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close()
	assert.Equal(t, lis.Addr().String(), gatewayTarget(lis))

	all, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer all.Close()
	_, port, err := net.SplitHostPort(all.Addr().String())
	require.NoError(t, err)
	assert.Equal(t, "localhost:"+port, gatewayTarget(all))
}

// writeTestCert writes a self-signed certificate for localhost and its key
// and returns the paths of the PEM files.
func writeTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certFile = filepath.Join(dir, "server.pem")
	keyFile = filepath.Join(dir, "server.key")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))

	return certFile, keyFile
}
