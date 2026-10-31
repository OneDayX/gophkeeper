// Package remote talks to the GophKeeper server over gRPC. It turns the
// protocol messages into client models and the gRPC status codes into the
// errors of the models package.
package remote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/OneDayX/gophkeeper/internal/client/models"
	"github.com/OneDayX/gophkeeper/internal/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// maxMessageSize is the largest message in bytes the client sends and
// receives. It matches the limit of the server: an item of the largest size
// with room for the rest of the message.
const maxMessageSize = 13 << 20 // 13 MiB

// Options tell how to reach the server. A server on this machine is reached
// without TLS unless CACert is set; any other one only over TLS unless
// Insecure is set.
type Options struct {
	// Address is the host:port of the gRPC server.
	Address string
	// CACert is the PEM file of the CA that has signed the server
	// certificate. Empty means the CAs the system trusts.
	CACert string
	// Insecure turns TLS off for a server on another machine too, for
	// debugging.
	Insecure bool
}

// Client is a connection to the server. It is safe for concurrent use.
type Client struct {
	conn  *grpc.ClientConn
	auth  pb.AuthServiceClient
	items pb.ItemServiceClient

	mu    sync.RWMutex
	token string
}

// New prepares a connection to the server. It does not connect yet, the
// first call does, so it succeeds offline too.
func New(opts Options) (*Client, error) {
	creds, err := transportCredentials(opts)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(opts.Address,
		grpc.WithTransportCredentials(creds),
		// Without it, resolving a host name also makes a DNS TXT query for a
		// gRPC service config, which GophKeeper does not use; a slow DNS
		// server, such as the one of WSL, then delays every command by its
		// 5-second timeout.
		grpc.WithDisableServiceConfig(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMessageSize),
			grpc.MaxCallSendMsgSize(maxMessageSize),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", opts.Address, err)
	}

	return &Client{
		conn:  conn,
		auth:  pb.NewAuthServiceClient(conn),
		items: pb.NewItemServiceClient(conn),
	}, nil
}

// transportCredentials returns TLS with the CA from the options or the
// system ones. There is no TLS if the options say so, or if the server is on
// this machine and no CA is given: the traffic never leaves the computer,
// and a local server has no certificate a system CA would trust anyway.
func transportCredentials(opts Options) (credentials.TransportCredentials, error) {
	if opts.Insecure || (opts.CACert == "" && isLoopback(opts.Address)) {
		return insecure.NewCredentials(), nil
	}

	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if opts.CACert != "" {
		data, err := os.ReadFile(opts.CACert)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate: %w", err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("no certificates in %s", opts.CACert)
		}
		config.RootCAs = pool
	}

	return credentials.NewTLS(config), nil
}

// isLoopback reports whether the address points to this machine: localhost
// or a loopback IP such as 127.0.0.1 or ::1.
func isLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}

	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Close closes the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// SetToken sets the token the item calls are authenticated with.
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.token = token
}

// Register creates an account and returns its token. It returns
// models.ErrLoginTaken if the login is taken.
func (c *Client) Register(ctx context.Context, login, password string) (string, error) {
	resp, err := c.auth.Register(ctx, &pb.RegisterRequest{Login: login, Password: password})
	if status.Code(err) == codes.AlreadyExists {
		return "", fmt.Errorf("%w: %q", models.ErrLoginTaken, login)
	}
	if err != nil {
		return "", convertError(err)
	}

	return resp.GetToken(), nil
}

// Login checks the credentials and returns a token. It returns
// models.ErrInvalidCredentials if the server does not accept them.
func (c *Client) Login(ctx context.Context, login, password string) (string, error) {
	resp, err := c.auth.Login(ctx, &pb.LoginRequest{Login: login, Password: password})
	if status.Code(err) == codes.Unauthenticated {
		return "", models.ErrInvalidCredentials
	}
	if err != nil {
		return "", convertError(err)
	}

	return resp.GetToken(), nil
}

// CreateItem stores a new item on the server and returns it with the version
// and time the server has given it. It returns models.ErrConflict if the
// server already has an item with this ID.
func (c *Client) CreateItem(ctx context.Context, item models.Item) (models.Item, error) {
	resp, err := c.items.CreateItem(c.withToken(ctx), &pb.CreateItemRequest{
		Id:   item.ID,
		Type: pb.ItemType(item.Type),
		Data: item.Data,
	})
	if err != nil {
		return models.Item{}, convertError(err)
	}

	item.ID = resp.GetId()
	item.Version = resp.GetVersion()
	item.UpdatedAt = resp.GetUpdatedAt().AsTime()

	return item, nil
}

// UpdateItem sends the new data of an item; item.Version is the version the
// change is based on. It returns the item with the new version, or
// models.ErrConflict if the item has changed on the server since, or
// models.ErrNotFound if the server has no such item.
func (c *Client) UpdateItem(ctx context.Context, item models.Item) (models.Item, error) {
	resp, err := c.items.UpdateItem(c.withToken(ctx), &pb.UpdateItemRequest{
		Id:      item.ID,
		Data:    item.Data,
		Version: item.Version,
	})
	if err != nil {
		return models.Item{}, convertError(err)
	}

	item.Version = resp.GetVersion()
	item.UpdatedAt = resp.GetUpdatedAt().AsTime()

	return item, nil
}

// DeleteItem deletes an item at the version and returns the version of the
// tombstone. It fails like UpdateItem.
func (c *Client) DeleteItem(ctx context.Context, id string, version int64) (int64, error) {
	resp, err := c.items.DeleteItem(c.withToken(ctx), &pb.DeleteItemRequest{Id: id, Version: version})
	if err != nil {
		return 0, convertError(err)
	}

	return resp.GetVersion(), nil
}

// ListItems calls fn for every item changed on the server after the version
// since, in the order of versions. Deleted items come as tombstones with
// Deleted set. It stops at the first error of fn and returns it.
func (c *Client) ListItems(ctx context.Context, since int64, fn func(models.Item) error) error {
	// Cancelling the call releases the stream if fn stops early.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := c.items.ListItems(c.withToken(ctx), &pb.ListItemsRequest{SinceVersion: since})
	if err != nil {
		return convertError(err)
	}

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return convertError(err)
		}

		if err := fn(itemFromProto(resp.GetItem())); err != nil {
			return err
		}
	}
}

// withToken returns a context that sends the token with the call.
func (c *Client) withToken(ctx context.Context) context.Context {
	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

// itemFromProto turns a protocol item into a models one.
func itemFromProto(item *pb.Item) models.Item {
	return models.Item{
		ID:        item.GetId(),
		Type:      models.ItemType(item.GetType()),
		Data:      item.GetData(),
		Version:   item.GetVersion(),
		Deleted:   item.GetDeleted(),
		UpdatedAt: item.GetUpdatedAt().AsTime(),
	}
}

// convertError turns a gRPC status into an error of the models package that
// keeps the message of the server.
func convertError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}

	var kind error
	switch st.Code() {
	case codes.Unauthenticated:
		kind = models.ErrUnauthenticated
	case codes.AlreadyExists, codes.Aborted:
		kind = models.ErrConflict
	case codes.NotFound:
		kind = models.ErrNotFound
	case codes.InvalidArgument:
		kind = models.ErrRejected
	case codes.Unavailable, codes.DeadlineExceeded:
		kind = models.ErrUnavailable
	default:
		return fmt.Errorf("server error: %s: %s", st.Code(), st.Message())
	}

	return fmt.Errorf("%w: %s", kind, st.Message())
}
