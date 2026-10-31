package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRun(t *testing.T) {
	args := os.Args
	t.Cleanup(func() { os.Args = args })

	// -h prints the flags and is not a failure.
	os.Args = []string{"gophkeeper-server", "-h"}
	assert.NoError(t, run())

	os.Args = []string{"gophkeeper-server", "-unknown"}
	assert.Error(t, run())

	// An address it cannot listen on fails the start.
	os.Args = []string{"gophkeeper-server", "-a", "256.0.0.1:1", "-http", ""}
	assert.Error(t, run())
}
