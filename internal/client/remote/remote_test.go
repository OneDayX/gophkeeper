package remote

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/server/auth"
	"github.com/OneDayX/gophkeeper/internal/server/grpcserver"
	"github.com/OneDayX/gophkeeper/internal/server/repository"
	"github.com/OneDayX/gophkeeper/internal/server/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const itemID = "6f0d3c5e-8a3b-4d7e-9a43-1f1c2b7a9e01"

// startServer serves the real server code with memory repositories on a
// free loopback port and returns its address.
func startServer(t *testing.T, opts ...grpc.ServerOption) string {
	t.Helper()

	tokens := auth.NewTokens("test-key", time.Hour)
	srv := grpcserver.New(
		service.NewUserService(repository.NewMemoryUserRepository(), tokens),
		service.NewItemService(repository.NewMemoryItemRepository()),
		tokens, zap.NewNop(), opts...,
	)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return lis.Addr().String()
}

// newClient connects to the server without TLS.
func newClient(t *testing.T, addr string) *Client {
	t.Helper()

	c, err := New(Options{Address: addr, Insecure: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	return c
}

// list collects what ListItems gives.
func list(t *testing.T, c *Client, since int64) []models.Item {
	t.Helper()

	var items []models.Item
	require.NoError(t, c.ListItems(context.Background(), since, func(item models.Item) error {
		items = append(items, item)
		return nil
	}))

	return items
}

func TestAuth(t *testing.T) {
	c := newClient(t, startServer(t))
	ctx := context.Background()

	token, err := c.Register(ctx, "alice", "auth-key")
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	_, err = c.Register(ctx, "alice", "auth-key")
	assert.ErrorIs(t, err, models.ErrLoginTaken)

	_, err = c.Register(ctx, "", "auth-key")
	assert.ErrorIs(t, err, models.ErrRejected)

	token, err = c.Login(ctx, "alice", "auth-key")
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	_, err = c.Login(ctx, "alice", "wrong")
	assert.ErrorIs(t, err, models.ErrInvalidCredentials)

	_, err = c.Login(ctx, "", "")
	assert.ErrorIs(t, err, models.ErrRejected)
}

func TestItems(t *testing.T) {
	c := newClient(t, startServer(t))
	ctx := context.Background()

	_, err := c.CreateItem(ctx, models.Item{ID: itemID, Type: models.ItemTypeCard, Data: []byte("v1")})
	assert.ErrorIs(t, err, models.ErrUnauthenticated, "no token yet")

	token, err := c.Register(ctx, "alice", "auth-key")
	require.NoError(t, err)
	c.SetToken(token)

	created, err := c.CreateItem(ctx, models.Item{ID: itemID, Type: models.ItemTypeCard, Data: []byte("v1")})
	require.NoError(t, err)
	assert.Equal(t, int64(1), created.Version)
	assert.False(t, created.UpdatedAt.IsZero())

	_, err = c.CreateItem(ctx, models.Item{ID: itemID, Type: models.ItemTypeCard, Data: []byte("again")})
	assert.ErrorIs(t, err, models.ErrConflict)

	created.Data = []byte("v2")
	updated, err := c.UpdateItem(ctx, created)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)

	_, err = c.UpdateItem(ctx, created)
	assert.ErrorIs(t, err, models.ErrConflict, "version 1 is stale")

	_, err = c.UpdateItem(ctx, models.Item{ID: "6f0d3c5e-8a3b-4d7e-9a43-1f1c2b7a9e02", Data: []byte("x"), Version: 1})
	assert.ErrorIs(t, err, models.ErrNotFound)

	items := list(t, c, 0)
	require.Len(t, items, 1)
	assert.Equal(t, models.Item{
		ID:        itemID,
		Type:      models.ItemTypeCard,
		Data:      []byte("v2"),
		Version:   2,
		UpdatedAt: updated.UpdatedAt,
	}, items[0])

	_, err = c.DeleteItem(ctx, itemID, 1)
	assert.ErrorIs(t, err, models.ErrConflict)

	version, err := c.DeleteItem(ctx, itemID, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), version)

	items = list(t, c, 2)
	require.Len(t, items, 1)
	assert.True(t, items[0].Deleted)

	assert.Empty(t, list(t, c, 3))

	stop := errors.New("stop")
	err = c.ListItems(ctx, 0, func(models.Item) error { return stop })
	assert.ErrorIs(t, err, stop)

	err = c.ListItems(ctx, -1, func(models.Item) error { return nil })
	assert.ErrorIs(t, err, models.ErrRejected)
}

func TestUnavailable(t *testing.T) {
	// Nothing listens on the port of a closed listener.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	c := newClient(t, addr)

	_, err = c.Login(context.Background(), "alice", "auth-key")
	assert.ErrorIs(t, err, models.ErrUnavailable)

	err = c.ListItems(context.Background(), 0, func(models.Item) error { return nil })
	assert.ErrorIs(t, err, models.ErrUnavailable)
}

func TestTLS(t *testing.T) {
	certFile, keyFile := writeTestCert(t)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)

	addr := startServer(t, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})))

	c, err := New(Options{Address: addr, CACert: certFile})
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Register(context.Background(), "alice", "auth-key")
	require.NoError(t, err)

	// The system CAs do not trust a self-signed certificate.
	untrusting, err := New(Options{Address: addr})
	require.NoError(t, err)
	defer untrusting.Close()
	_, err = untrusting.Login(context.Background(), "alice", "auth-key")
	assert.ErrorIs(t, err, models.ErrUnavailable)
}

func TestNew_BadCA(t *testing.T) {
	_, err := New(Options{Address: "localhost:3200", CACert: filepath.Join(t.TempDir(), "missing.pem")})
	assert.Error(t, err)

	notPEM := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("not a certificate"), 0o600))
	_, err = New(Options{Address: "localhost:3200", CACert: notPEM})
	assert.Error(t, err)
}

func TestConvertError(t *testing.T) {
	plain := errors.New("plain")
	assert.Equal(t, plain, convertError(plain))

	err := convertError(status.Error(codes.Internal, "internal error"))
	assert.EqualError(t, err, "server error: Internal: internal error")
}

// writeTestCert writes a self-signed certificate for 127.0.0.1 and its key
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
