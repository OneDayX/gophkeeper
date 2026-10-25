// Package app assembles the GophKeeper server out of its parts and runs it:
// the gRPC server and, unless turned off, the REST gateway in front of it.
package app

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/OneDayX/gophkeeper/internal/server/auth"
	"github.com/OneDayX/gophkeeper/internal/server/config"
	"github.com/OneDayX/gophkeeper/internal/server/database"
	"github.com/OneDayX/gophkeeper/internal/server/gateway"
	"github.com/OneDayX/gophkeeper/internal/server/grpcserver"
	"github.com/OneDayX/gophkeeper/internal/server/repository"
	"github.com/OneDayX/gophkeeper/internal/server/service"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// shutdownTimeout is how long the calls in progress get to finish.
	shutdownTimeout = 10 * time.Second

	// readHeaderTimeout keeps a slow client from holding a connection to the
	// gateway open without ever sending a request.
	readHeaderTimeout = 5 * time.Second
)

// App is a configured GophKeeper server with its listeners bound.
type App struct {
	log *zap.Logger

	// db is nil when the data is kept in memory.
	db *database.DB

	grpc    *grpc.Server
	grpcLis net.Listener

	// The gateway fields are nil when the gateway is turned off.
	http        *http.Server
	httpLis     net.Listener
	gatewayConn *grpc.ClientConn
}

// New opens the storage, wires the components together and binds the
// listeners, so a busy port fails here rather than in Run. Close releases
// what it has opened.
func New(ctx context.Context, cfg config.Config, log *zap.Logger) (*App, error) {
	a := &App{log: log}

	if err := a.init(ctx, cfg); err != nil {
		a.Close()
		return nil, err
	}

	return a, nil
}

// init does the work of New; on failure New closes what it has opened.
func (a *App) init(ctx context.Context, cfg config.Config) error {
	key := cfg.AuthKey
	if key == "" {
		key = rand.Text()
		a.log.Warn("no auth key is set, tokens will not survive a restart")
	}
	tokens := auth.NewTokens(key, cfg.TokenTTL)

	var (
		users *service.UserService
		items *service.ItemService
	)
	if cfg.DatabaseDSN != "" {
		db, err := database.New(ctx, cfg.DatabaseDSN)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		a.db = db

		users = service.NewUserService(repository.NewUserRepository(db.Pool()), tokens)
		items = service.NewItemService(repository.NewItemRepository(db.Pool()))
	} else {
		a.log.Warn("no database is set, the data is kept in memory and will be lost on restart")

		users = service.NewUserService(repository.NewMemoryUserRepository(), tokens)
		items = service.NewItemService(repository.NewMemoryItemRepository())
	}

	var tlsConfig *tls.Config
	if cfg.TLSEnabled() {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("load TLS certificate: %w", err)
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}

	var serverOpts []grpc.ServerOption
	if tlsConfig != nil {
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}
	a.grpc = grpcserver.New(users, items, tokens, a.log, serverOpts...)

	var err error
	a.grpcLis, err = net.Listen("tcp", cfg.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}

	if cfg.HTTPAddress == "" {
		return nil
	}

	return a.initGateway(ctx, cfg, tlsConfig)
}

// initGateway sets up the REST gateway. It reaches the gRPC server over the
// network like any client, so that the calls pass the same interceptors.
func (a *App) initGateway(ctx context.Context, cfg config.Config, tlsConfig *tls.Config) error {
	creds := insecure.NewCredentials()
	if tlsConfig != nil {
		// The gateway trusts the certificate of its own server.
		var err error
		creds, err = credentials.NewClientTLSFromFile(cfg.TLSCertFile, "")
		if err != nil {
			return fmt.Errorf("load TLS certificate for the gateway: %w", err)
		}
	}

	conn, err := grpc.NewClient(gatewayTarget(a.grpcLis),
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(grpcserver.MaxMessageSize),
			grpc.MaxCallSendMsgSize(grpcserver.MaxMessageSize),
		),
	)
	if err != nil {
		return fmt.Errorf("connect the gateway to gRPC: %w", err)
	}
	a.gatewayConn = conn

	handler, err := gateway.New(ctx, conn)
	if err != nil {
		return err
	}

	a.httpLis, err = net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		return fmt.Errorf("listen for the gateway: %w", err)
	}

	a.http = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		TLSConfig:         tlsConfig,
	}

	return nil
}

// GRPCAddr returns the address the gRPC server listens on.
func (a *App) GRPCAddr() string {
	return a.grpcLis.Addr().String()
}

// HTTPAddr returns the address the REST gateway listens on, or an empty
// string if the gateway is turned off.
func (a *App) HTTPAddr() string {
	if a.httpLis == nil {
		return ""
	}

	return a.httpLis.Addr().String()
}

// Run serves gRPC and the gateway until ctx is cancelled or a server fails.
// Then it lets the calls in progress finish and returns the server error, if
// any.
func (a *App) Run(ctx context.Context) error {
	// Buffered with a slot per server, so the goroutines can report the error
	// and exit even when nobody is waiting for it anymore.
	serveErr := make(chan error, 2)

	a.log.Info("starting gRPC server", zap.String("addr", a.GRPCAddr()))
	go func() {
		// Serve returns nil after a stop, so only a real failure is an error.
		if err := a.grpc.Serve(a.grpcLis); err != nil {
			serveErr <- fmt.Errorf("gRPC server: %w", err)
		}
	}()

	if a.http != nil {
		a.log.Info("starting REST gateway", zap.String("addr", a.HTTPAddr()))
		go func() {
			if err := a.serveHTTP(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("REST gateway: %w", err)
			}
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
		a.log.Info("shutdown signal received")
	case runErr = <-serveErr:
		a.log.Error("server error", zap.Error(runErr))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// The gateway goes first: its requests in progress are gRPC calls.
	if a.http != nil {
		if err := a.http.Shutdown(shutdownCtx); err != nil {
			a.log.Error("failed to shut down the REST gateway", zap.Error(err))
		}
	}
	stopGRPC(shutdownCtx, a.grpc)

	return runErr
}

// serveHTTP serves the gateway, over TLS if it is configured.
func (a *App) serveHTTP() error {
	if a.http.TLSConfig != nil {
		// The certificate is in TLSConfig already.
		return a.http.ServeTLS(a.httpLis, "", "")
	}

	return a.http.Serve(a.httpLis)
}

// Close releases the listeners, the gateway connection and the database
// connections. Call it after Run returns.
func (a *App) Close() {
	// The servers close their listeners when they stop; these are for an App
	// that has never run, and a second close does no harm.
	if a.grpcLis != nil {
		_ = a.grpcLis.Close()
	}
	if a.httpLis != nil {
		_ = a.httpLis.Close()
	}
	if a.gatewayConn != nil {
		_ = a.gatewayConn.Close()
	}
	if a.db != nil {
		a.db.Close()
	}
}

// stopGRPC lets the calls in progress finish, but cuts them off when ctx is
// done: GracefulStop alone would wait for them forever.
func stopGRPC(ctx context.Context, srv *grpc.Server) {
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-ctx.Done():
		srv.Stop()
	}
}

// gatewayTarget returns the address the gateway reaches the gRPC server at:
// the address of the listener, with localhost in place of an unspecified
// host such as 0.0.0.0.
func gatewayTarget(lis net.Listener) string {
	addr, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		return lis.Addr().String()
	}

	host := addr.IP.String()
	if addr.IP.IsUnspecified() {
		host = "localhost"
	}

	return net.JoinHostPort(host, strconv.Itoa(addr.Port))
}
