package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRun(t *testing.T) {
	args := os.Args
	t.Cleanup(func() { os.Args = args })

	os.Args = []string{"gophkeeper", "version"}
	assert.Equal(t, 0, run())

	os.Args = []string{"gophkeeper", "no-such-command"}
	assert.Equal(t, 1, run())
}
