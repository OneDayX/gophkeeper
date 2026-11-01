// Package gateway serves the REST API of GophKeeper. grpc-gateway turns the
// HTTP requests, as the Swagger description lays them out, into gRPC calls to
// the server, so both protocols share the handlers, the token check and the
// call log.
package gateway

import (
	"context"
	_ "embed" // for the go:embed directive
	"fmt"
	"net/http"

	"github.com/OneDayX/gophkeeper/api/openapi"
	"github.com/OneDayX/gophkeeper/internal/pb"
	"github.com/OneDayX/gophkeeper/internal/server/grpcserver"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

// maxBodySize caps a request body. The largest valid one carries an item of
// the largest size, base64-encoded in JSON.
const maxBodySize = grpcserver.MaxMessageSize * 3 / 2

// docsPage shows the Swagger description with Swagger UI.
//
//go:embed docs.html
var docsPage []byte

// New returns an HTTP handler that serves the REST API under /api/, calling
// the gRPC server over conn, the Swagger description at /swagger.json and a
// page to explore it at /docs.
func New(ctx context.Context, conn *grpc.ClientConn) (http.Handler, error) {
	api := runtime.NewServeMux()

	if err := pb.RegisterAuthServiceHandler(ctx, api, conn); err != nil {
		return nil, fmt.Errorf("register auth service: %w", err)
	}
	if err := pb.RegisterItemServiceHandler(ctx, api, conn); err != nil {
		return nil, fmt.Errorf("register item service: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", limitBody(api))
	mux.HandleFunc("GET /swagger.json", serve("application/json", openapi.Spec))
	mux.HandleFunc("GET /docs", serve("text/html; charset=utf-8", docsPage))

	return mux, nil
}

// limitBody keeps a client from making the gateway read an endless body
// into memory.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
		next.ServeHTTP(w, r)
	})
}

// serve returns a handler that answers with the content.
func serve(contentType string, content []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(content)
	}
}
