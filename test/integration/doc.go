// Package integration tests GophKeeper as a whole: the real server, with
// PostgreSQL when TEST_DATABASE_DSN is set and in memory otherwise, and real
// clients on several "devices" talking to it over the network. The
// functional tests in binaries_test.go build the server and the client and
// run them the way a user would; -short skips them.
//
// The package holds tests only.
package integration
