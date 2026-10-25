// Package grpcserver serves the GophKeeper protocol over gRPC: AuthService
// signs users up and in, ItemService keeps their encrypted items. The
// handlers turn protobuf messages into calls of the services and service
// errors into gRPC status codes; the interceptors check tokens and log calls.
package grpcserver

import (
	"context"

	"github.com/OneDayX/gophkeeper/internal/pb"
	"github.com/OneDayX/gophkeeper/internal/server/models"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// MaxMessageSize is the largest message in bytes the server receives and
// sends: an item of models.MaxItemSize with room for the rest of the message.
const MaxMessageSize = models.MaxItemSize + 1<<20

type userService interface {
	Register(ctx context.Context, login, password string) (string, error)
	Login(ctx context.Context, login, password string) (string, error)
}

type itemService interface {
	Create(ctx context.Context, userID int64, item models.Item) (models.Item, error)
	Update(ctx context.Context, userID int64, item models.Item) (models.Item, error)
	Delete(ctx context.Context, userID int64, id string, version int64) (int64, error)
	Get(ctx context.Context, userID int64, id string) (models.Item, error)
	List(ctx context.Context, userID int64, since int64, fn func(models.Item) error) error
}

type tokenParser interface {
	Parse(token string) (int64, error)
}

// New returns a gRPC server with both services behind the call log and the
// token check. opts add server options, such as the TLS credentials.
func New(users userService, items itemService, tokens tokenParser, log *zap.Logger, opts ...grpc.ServerOption) *grpc.Server {
	// The log comes first, so that the calls refused for a bad token are
	// logged too.
	opts = append([]grpc.ServerOption{
		grpc.ChainUnaryInterceptor(UnaryLogger(log), UnaryAuth(tokens)),
		grpc.ChainStreamInterceptor(StreamLogger(log), StreamAuth(tokens)),
		grpc.MaxRecvMsgSize(MaxMessageSize),
		grpc.MaxSendMsgSize(MaxMessageSize),
	}, opts...)

	srv := grpc.NewServer(opts...)
	pb.RegisterAuthServiceServer(srv, NewAuthServer(users, log))
	pb.RegisterItemServiceServer(srv, NewItemServer(items, log))

	return srv
}
