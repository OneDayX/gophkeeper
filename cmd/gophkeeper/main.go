// Command gophkeeper is the GophKeeper client. It keeps logins and passwords,
// texts, files, bank cards and OTP generators encrypted on the device and
// syncs them between the devices of the user through the GophKeeper server.
//
// `gophkeeper help` lists the commands, `gophkeeper tui` starts the
// interactive interface, and `gophkeeper version` prints the version and the
// build date of the binary.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/OneDayX/gophkeeper/internal/client/cli"
)

// Build info, set at build time with
// -ldflags "-X main.buildVersion=... -X main.buildDate=... -X main.buildCommit=...".
var (
	buildVersion = "N/A"
	buildDate    = "N/A"
	buildCommit  = "N/A"
)

func main() {
	os.Exit(run())
}

// run executes the command line and returns the exit code; os.Exit in main
// alone would skip the deferred calls.
func run() int {
	// ctx is cancelled on Ctrl+C, so that a command stops waiting for the
	// server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := cli.NewRootCommand(cli.BuildInfo{Version: buildVersion, Date: buildDate, Commit: buildCommit})
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	return 0
}
