package integration

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The functional tests build the real binaries and run them as a user would.
// They take a while to build, so -short skips them.

// buildBinaries builds the server and the client into a temporary directory.
func buildBinaries(t *testing.T) (serverBin, clientBin string) {
	t.Helper()

	if testing.Short() {
		t.Skip("building the binaries takes a while, skipped with -short")
	}

	dir := t.TempDir()
	serverBin = filepath.Join(dir, "gophkeeper-server")
	clientBin = filepath.Join(dir, "gophkeeper")
	if runtime.GOOS == "windows" {
		serverBin += ".exe"
		clientBin += ".exe"
	}

	for bin, pkg := range map[string]string{serverBin: "../../cmd/gophkeeper-server", clientBin: "../../cmd/gophkeeper"} {
		// The test needs no git data in the binaries, and asking git for it
		// fails where the checkout belongs to another user, as in CI
		// containers.
		cmd := exec.Command("go", "build", "-buildvcs=false",
			"-ldflags", "-X main.buildVersion=v9.9.9 -X main.buildDate=2026-10-25",
			"-o", bin, pkg)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	return serverBin, clientBin
}

// freeAddr returns a loopback address with a port nobody listens on.
func freeAddr(t *testing.T) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	return addr
}

// lockedBuffer collects the output of a process; the process writes it
// while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends to the buffer.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// String returns what has been written so far.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// startServerBinary runs the server binary with the data in memory and
// waits until it accepts connections. It returns the process and its
// output.
func startServerBinary(t *testing.T, bin, addr string) (*exec.Cmd, *lockedBuffer) {
	t.Helper()

	output := &lockedBuffer{}
	cmd := exec.Command(bin, "-a", addr, "-http", "", "-k", "functional-key")
	cmd.Stdout = output
	cmd.Stderr = output
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return cmd, output
		}
		require.True(t, time.Now().Before(deadline), "the server has not started: %s", output.String())
		time.Sleep(50 * time.Millisecond)
	}
}

// client runs the client binary with the data directory of a device.
type client struct {
	bin     string
	server  string
	dataDir string
}

func (c client) run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin, append([]string{"--server", c.server, "--insecure", "--data-dir", c.dataDir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()

	return string(out), err
}

func (c client) must(t *testing.T, stdin string, args ...string) string {
	t.Helper()

	out, err := c.run(t, stdin, args...)
	require.NoError(t, err, "gophkeeper %s: %s", strings.Join(args, " "), out)

	return out
}

func TestBinaries(t *testing.T) {
	serverBin, clientBin := buildBinaries(t)
	addr := freeAddr(t)
	serverCmd, serverOutput := startServerBinary(t, serverBin, addr)

	laptop := client{bin: clientBin, server: addr, dataDir: t.TempDir()}
	phone := client{bin: clientBin, server: addr, dataDir: t.TempDir()}

	out := laptop.must(t, "", "version")
	assert.Contains(t, out, "GophKeeper client v9.9.9")
	assert.Contains(t, out, "Build date: 2026-10-25")

	laptop.must(t, masterPassword+"\n"+masterPassword+"\n", "register", "--login", "alice")
	laptop.must(t, "s3cret\n", "add", "credentials", "--name", "GitHub", "--login", "alice")
	laptop.must(t, "", "add", "card", "--name", "Visa", "--number", "4111 1111 1111 1111", "--cvv", "123")

	out = phone.must(t, masterPassword+"\n", "login", "--login", "alice")
	assert.Contains(t, out, "2 received")

	out = phone.must(t, "", "show", "github", "--reveal")
	assert.Contains(t, out, "s3cret")

	phone.must(t, "", "edit", "visa", "--cvv", "999")
	laptop.must(t, "", "sync")
	assert.Contains(t, laptop.must(t, "", "show", "visa", "-r"), "999")

	out, err := laptop.run(t, "", "show", "nothing")
	assert.Error(t, err)
	assert.Contains(t, out, "item not found")

	// The server stops gracefully on SIGTERM; Windows has no such signal.
	if runtime.GOOS != "windows" {
		require.NoError(t, serverCmd.Process.Signal(syscall.SIGTERM))
		require.NoError(t, serverCmd.Wait(), serverOutput.String())
		assert.Contains(t, serverOutput.String(), "shutdown signal received")
	}
}

func TestBinaries_ClientWithoutServer(t *testing.T) {
	_, clientBin := buildBinaries(t)

	c := client{bin: clientBin, server: freeAddr(t), dataDir: t.TempDir()}
	out, err := c.run(t, masterPassword+"\n", "login", "--login", "alice")
	assert.Error(t, err)
	assert.Contains(t, out, "server is unavailable")

	// The client tells that nobody is logged in instead of failing.
	assert.Contains(t, c.must(t, "", "status"), "Not logged in")

	_, err = os.Stat(filepath.Join(c.dataDir, "session.json"))
	assert.True(t, os.IsNotExist(err), "a failed login leaves no session")
}
