package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/client/app"
	"github.com/OneDayX/gophkeeper/internal/client/crypt"
	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/client/vault"
	serverapp "github.com/OneDayX/gophkeeper/internal/server/app"
	"github.com/OneDayX/gophkeeper/internal/server/config"
	"github.com/OneDayX/gophkeeper/internal/server/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const masterPassword = "correct horse battery staple"

// server is a running GophKeeper server.
type server struct {
	grpcAddr string
	httpAddr string
}

// startServer runs the server until the test ends. With TEST_DATABASE_DSN
// it keeps the data in PostgreSQL, emptied first.
func startServer(t *testing.T) server {
	t.Helper()
	ctx := context.Background()

	cfg := config.Config{
		GRPCAddress: "127.0.0.1:0",
		HTTPAddress: "127.0.0.1:0",
		AuthKey:     "integration-key",
		TokenTTL:    time.Hour,
		DatabaseDSN: os.Getenv("TEST_DATABASE_DSN"),
	}
	if cfg.DatabaseDSN != "" {
		db, err := database.New(ctx, cfg.DatabaseDSN)
		require.NoError(t, err)
		_, err = db.Pool().Exec(ctx, `TRUNCATE users, items RESTART IDENTITY CASCADE`)
		require.NoError(t, err)
		db.Close()
	}

	srv, err := serverapp.New(ctx, cfg, zap.NewNop())
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
		srv.Close()
	})

	return server{grpcAddr: srv.GRPCAddr(), httpAddr: srv.HTTPAddr()}
}

// device is a client on one machine of the user.
type device struct {
	app   *app.App
	vault *vault.Vault
}

// login logs the user in on a fresh device, registering them if asked.
func login(t *testing.T, s server, user string, register bool) *device {
	t.Helper()
	ctx := context.Background()

	a, err := app.New(app.Config{DataDir: t.TempDir(), Server: s.grpcAddr})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })

	if register {
		require.NoError(t, a.Register(ctx, user, masterPassword))
	} else {
		require.NoError(t, a.Login(ctx, user, masterPassword))
	}

	v, err := a.Vault()
	require.NoError(t, err)

	return &device{app: a, vault: v}
}

func (d *device) sync(t *testing.T) vault.SyncResult {
	t.Helper()

	result, err := d.vault.Sync(context.Background())
	require.NoError(t, err)

	return result
}

func (d *device) find(t *testing.T, name string) vault.Entry {
	t.Helper()

	e, err := d.vault.Find(name)
	require.NoError(t, err)

	return e
}

// TestNewUserScenario follows the scenario of a new user from the task: get
// the client, register, add data, sync; then the scenario of an existing
// user on another device: log in, sync, ask for the data, see it.
func TestNewUserScenario(t *testing.T) {
	s := startServer(t)

	laptop := login(t, s, "alice", true)
	secrets := []models.Secret{
		{Name: "GitHub", Metadata: "github.com", Payload: models.Credentials{Login: "alice", Password: "s3cret"}},
		{Name: "Diary", Payload: models.Text{Content: "dear diary,\ntoday I wrote a password manager"}},
		{Name: "SSH key", Payload: models.Binary{FileName: "id_ed25519", Data: []byte{0, 1, 2, 3, 255}}},
		{Name: "Visa", Metadata: "Bank of Gophers\nsupport: 8-800-000-00-00", Payload: models.Card{Number: "4111111111111111", Holder: "ALICE", Expiry: "12/30", CVV: "123"}},
		{Name: "GitHub 2FA", Metadata: "recovery codes: 1111-2222 3333-4444", Payload: models.OTP{Secret: "JBSWY3DPEHPK3PXP", Issuer: "GitHub"}},
	}
	for _, secret := range secrets {
		_, err := laptop.vault.Add(secret)
		require.NoError(t, err)
	}
	assert.Equal(t, vault.SyncResult{Pushed: len(secrets)}, laptop.sync(t))

	phone := login(t, s, "alice", false)
	assert.Equal(t, vault.SyncResult{Pulled: len(secrets)}, phone.sync(t))

	for _, want := range secrets {
		got := phone.find(t, want.Name)
		assert.Equal(t, want, got.Secret, want.Name)
		assert.False(t, got.Pending)
	}

	// Both devices show the same OTP code.
	now := time.Now()
	laptopCode, _, err := laptop.find(t, "GitHub 2FA").Secret.Payload.(models.OTP).Code(now)
	require.NoError(t, err)
	phoneCode, _, err := phone.find(t, "GitHub 2FA").Secret.Payload.(models.OTP).Code(now)
	require.NoError(t, err)
	assert.Equal(t, laptopCode, phoneCode)
}

// TestChangesBetweenDevices edits, deletes and conflicts across devices.
func TestChangesBetweenDevices(t *testing.T) {
	s := startServer(t)
	laptop := login(t, s, "alice", true)
	phone := login(t, s, "alice", false)

	diary, err := laptop.vault.Add(models.Secret{Name: "Diary", Payload: models.Text{Content: "v1"}})
	require.NoError(t, err)
	laptop.sync(t)
	phone.sync(t)

	// An edit travels.
	_, err = phone.vault.Update(diary.ID, models.Secret{Name: "Diary", Payload: models.Text{Content: "v2 from the phone"}})
	require.NoError(t, err)
	phone.sync(t)
	laptop.sync(t)
	assert.Equal(t, models.Text{Content: "v2 from the phone"}, laptop.find(t, "Diary").Secret.Payload)

	// Simultaneous edits: the second device to sync keeps its version as a copy.
	_, err = laptop.vault.Update(diary.ID, models.Secret{Name: "Diary", Payload: models.Text{Content: "laptop edit"}})
	require.NoError(t, err)
	_, err = phone.vault.Update(diary.ID, models.Secret{Name: "Diary", Payload: models.Text{Content: "phone edit"}})
	require.NoError(t, err)
	laptop.sync(t)
	assert.Equal(t, 1, phone.sync(t).Conflicts)
	laptop.sync(t)

	for _, d := range []*device{laptop, phone} {
		entries, err := d.vault.List()
		require.NoError(t, err)
		require.Len(t, entries, 2)
		assert.Equal(t, models.Text{Content: "laptop edit"}, entries[0].Secret.Payload)
		assert.Equal(t, models.Text{Content: "phone edit"}, entries[1].Secret.Payload)
		assert.True(t, strings.HasPrefix(entries[1].Secret.Name, "Diary (conflict "))
	}

	// A deletion travels.
	require.NoError(t, laptop.vault.Delete(diary.ID))
	laptop.sync(t)
	phone.sync(t)
	_, err = phone.vault.Get(diary.ID)
	assert.ErrorIs(t, err, models.ErrNotFound)

	// Another user sees none of it.
	bob := login(t, s, "bob", true)
	assert.Equal(t, vault.SyncResult{}, bob.sync(t))
}

// TestWrongPassword checks that a wrong master password gets nowhere.
func TestWrongPassword(t *testing.T) {
	s := startServer(t)
	login(t, s, "alice", true)

	a, err := app.New(app.Config{DataDir: t.TempDir(), Server: s.grpcAddr})
	require.NoError(t, err)
	defer a.Close()

	err = a.Login(context.Background(), "alice", "not the master password")
	assert.ErrorIs(t, err, models.ErrInvalidCredentials)
}

// TestServerSeesOnlyCiphertext looks at the data from the side of the server,
// through its REST API: the secrets are encrypted, and the master password
// is not what the server knows.
func TestServerSeesOnlyCiphertext(t *testing.T) {
	s := startServer(t)
	laptop := login(t, s, "alice", true)

	_, err := laptop.vault.Add(models.Secret{Name: "Bank", Payload: models.Credentials{Login: "alice", Password: "visible-only-to-alice"}})
	require.NoError(t, err)
	laptop.sync(t)

	base := "http://" + s.httpAddr + "/api/v1"

	// The server has never seen the master password: it does not work.
	status, _ := postJSON(t, base+"/auth/login", "", map[string]string{"login": "alice", "password": masterPassword})
	assert.Equal(t, http.StatusUnauthorized, status)

	// It knows the key derived from it, which a client computes.
	keys := crypt.DeriveKeys("alice", masterPassword)
	status, body := postJSON(t, base+"/auth/login", "", map[string]string{"login": "alice", "password": keys.Auth})
	require.Equal(t, http.StatusOK, status, body)
	var auth struct{ Token string }
	require.NoError(t, json.Unmarshal([]byte(body), &auth))

	req, err := http.NewRequest(http.MethodGet, base+"/items", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+auth.Token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	var line struct {
		Result struct {
			Item struct {
				Type string
				Data string
			}
		}
	}
	require.NoError(t, json.Unmarshal(raw, &line))
	assert.Equal(t, "ITEM_TYPE_CREDENTIALS", line.Result.Item.Type, "the type is not a secret")

	data, err := base64.StdEncoding.DecodeString(line.Result.Item.Data)
	require.NoError(t, err)
	for _, plaintext := range []string{"visible-only-to-alice", "Bank", "alice"} {
		assert.NotContains(t, string(data), plaintext)
	}
}

// postJSON posts a JSON body and returns the status and the body.
func postJSON(t *testing.T, url, token string, body any) (int, string) {
	t.Helper()

	payload, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(data)
}

// flakyNetwork is a TCP proxy in front of the server that can cut the
// connections off, like a device losing the network.
type flakyNetwork struct {
	lis    net.Listener
	target string

	mu    sync.Mutex
	down  bool
	conns []net.Conn
}

// newFlakyNetwork starts a proxy to the target address.
func newFlakyNetwork(t *testing.T, target string) *flakyNetwork {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	n := &flakyNetwork{lis: lis, target: target}
	go n.serve()
	t.Cleanup(func() {
		_ = lis.Close()
		n.setDown(true)
	})

	return n
}

// addr returns the address the clients connect to.
func (n *flakyNetwork) addr() string {
	return n.lis.Addr().String()
}

// serve passes the connections through while the network is up.
func (n *flakyNetwork) serve() {
	for {
		client, err := n.lis.Accept()
		if err != nil {
			return
		}

		n.mu.Lock()
		server, err := net.Dial("tcp", n.target)
		if n.down || err != nil {
			n.mu.Unlock()
			_ = client.Close()
			continue
		}
		n.conns = append(n.conns, client, server)
		n.mu.Unlock()

		go pipe(server, client)
		go pipe(client, server)
	}
}

// pipe copies one way and closes both ends when either is done.
func pipe(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
	_ = src.Close()
}

// setDown cuts the network off, dropping the open connections, or brings it
// back.
func (n *flakyNetwork) setDown(down bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.down = down
	if down {
		for _, c := range n.conns {
			_ = c.Close()
		}
		n.conns = nil
	}
}

// TestOfflineChangesReachServer works without the network and syncs later.
// Every app below is a new run of the client, like a CLI command.
func TestOfflineChangesReachServer(t *testing.T) {
	s := startServer(t)
	network := newFlakyNetwork(t, s.grpcAddr)
	dataDir := t.TempDir()
	ctx := context.Background()

	a, err := app.New(app.Config{DataDir: dataDir, Server: network.addr()})
	require.NoError(t, err)
	require.NoError(t, a.Register(ctx, "alice", masterPassword))
	require.NoError(t, a.Close())

	network.setDown(true)

	offline, err := app.New(app.Config{DataDir: dataDir})
	require.NoError(t, err)
	v, err := offline.Vault()
	require.NoError(t, err)
	_, err = v.Add(models.Secret{Name: "Written on a plane", Payload: models.Text{Content: "no network"}})
	require.NoError(t, err)
	_, err = v.Sync(ctx)
	assert.ErrorIs(t, err, models.ErrUnavailable)
	require.NoError(t, offline.Close())

	network.setDown(false)

	online, err := app.New(app.Config{DataDir: dataDir})
	require.NoError(t, err)
	defer online.Close()
	v, err = online.Vault()
	require.NoError(t, err)
	result, err := v.Sync(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Pushed)

	phone := login(t, s, "alice", false)
	phone.sync(t)
	phone.find(t, "Written on a plane")
}
