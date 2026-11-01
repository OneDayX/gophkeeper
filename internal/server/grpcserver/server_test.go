package grpcserver

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/pb"
	"github.com/OneDayX/gophkeeper/internal/server/auth"
	"github.com/OneDayX/gophkeeper/internal/server/models"
	"github.com/OneDayX/gophkeeper/internal/server/repository"
	"github.com/OneDayX/gophkeeper/internal/server/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const itemID = "6f0d3c5e-8a3b-4d7e-9a43-1f1c2b7a9e01"

// testClients are the clients of a server started for one test.
type testClients struct {
	auth  pb.AuthServiceClient
	items pb.ItemServiceClient
	logs  *observer.ObservedLogs
}

// newTestServer serves the real services with memory repositories on an
// in-memory listener.
func newTestServer(t *testing.T) testClients {
	t.Helper()

	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core)

	tokens := auth.NewTokens("test-key", time.Hour)
	srv := New(
		service.NewUserService(repository.NewMemoryUserRepository(), tokens),
		service.NewItemService(repository.NewMemoryItemRepository()),
		tokens, log,
	)

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageSize)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return testClients{auth: pb.NewAuthServiceClient(conn), items: pb.NewItemServiceClient(conn), logs: logs}
}

// withToken returns a context that sends the token with the calls.
func withToken(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), auth.MetadataKey, auth.BearerHeader(token))
}

// register signs a user up and returns a context with their token.
func register(t *testing.T, c testClients, login string) context.Context {
	t.Helper()

	resp, err := c.auth.Register(context.Background(), &pb.RegisterRequest{Login: login, Password: "secret"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetToken())

	return withToken(resp.GetToken())
}

// listAll reads the whole ListItems stream.
func listAll(t *testing.T, c testClients, ctx context.Context, since int64) ([]*pb.Item, error) {
	t.Helper()

	stream, err := c.items.ListItems(ctx, &pb.ListItemsRequest{SinceVersion: since})
	require.NoError(t, err)

	var items []*pb.Item
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return items, nil
		}
		if err != nil {
			return nil, err
		}
		items = append(items, resp.GetItem())
	}
}

func TestAuthService(t *testing.T) {
	c := newTestServer(t)
	ctx := context.Background()

	reg, err := c.auth.Register(ctx, &pb.RegisterRequest{Login: "alice", Password: "secret"})
	require.NoError(t, err)
	assert.NotEmpty(t, reg.GetToken())

	_, err = c.auth.Register(ctx, &pb.RegisterRequest{Login: "alice", Password: "other"})
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	_, err = c.auth.Register(ctx, &pb.RegisterRequest{Login: "", Password: "secret"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	login, err := c.auth.Login(ctx, &pb.LoginRequest{Login: "alice", Password: "secret"})
	require.NoError(t, err)
	assert.NotEmpty(t, login.GetToken())

	// A wrong password and an unknown login look the same.
	_, wrongPassword := c.auth.Login(ctx, &pb.LoginRequest{Login: "alice", Password: "wrong"})
	_, unknownLogin := c.auth.Login(ctx, &pb.LoginRequest{Login: "bob", Password: "secret"})
	assert.Equal(t, codes.Unauthenticated, status.Code(wrongPassword))
	assert.Equal(t, status.Convert(wrongPassword).Message(), status.Convert(unknownLogin).Message())
}

func TestItemService_NeedsToken(t *testing.T) {
	c := newTestServer(t)

	_, err := c.items.GetItem(context.Background(), &pb.GetItemRequest{Id: itemID})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	_, err = c.items.GetItem(withToken("forged"), &pb.GetItemRequest{Id: itemID})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	_, err = listAll(t, c, context.Background(), 0)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	_, err = listAll(t, c, withToken("forged"), 0)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestItemService(t *testing.T) {
	c := newTestServer(t)
	alice := register(t, c, "alice")
	bob := register(t, c, "bob")

	created, err := c.items.CreateItem(alice, &pb.CreateItemRequest{Id: itemID, Type: pb.ItemType_ITEM_TYPE_CARD, Data: []byte("v1")})
	require.NoError(t, err)
	assert.Equal(t, itemID, created.GetId())
	assert.Equal(t, int64(1), created.GetVersion())
	assert.False(t, created.GetUpdatedAt().AsTime().IsZero())

	_, err = c.items.CreateItem(alice, &pb.CreateItemRequest{Id: itemID, Type: pb.ItemType_ITEM_TYPE_CARD, Data: []byte("again")})
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	_, err = c.items.CreateItem(alice, &pb.CreateItemRequest{Type: pb.ItemType_ITEM_TYPE_UNSPECIFIED, Data: []byte("x")})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	got, err := c.items.GetItem(alice, &pb.GetItemRequest{Id: itemID})
	require.NoError(t, err)
	assert.Equal(t, pb.ItemType_ITEM_TYPE_CARD, got.GetItem().GetType())
	assert.Equal(t, []byte("v1"), got.GetItem().GetData())

	// Bob does not see the items of Alice.
	_, err = c.items.GetItem(bob, &pb.GetItemRequest{Id: itemID})
	assert.Equal(t, codes.NotFound, status.Code(err))

	updated, err := c.items.UpdateItem(alice, &pb.UpdateItemRequest{Id: itemID, Data: []byte("v2"), Version: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.GetVersion())

	_, err = c.items.UpdateItem(alice, &pb.UpdateItemRequest{Id: itemID, Data: []byte("stale"), Version: 1})
	assert.Equal(t, codes.Aborted, status.Code(err))

	_, err = c.items.DeleteItem(alice, &pb.DeleteItemRequest{Id: itemID, Version: 1})
	assert.Equal(t, codes.Aborted, status.Code(err))

	deleted, err := c.items.DeleteItem(alice, &pb.DeleteItemRequest{Id: itemID, Version: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted.GetVersion())

	_, err = c.items.GetItem(alice, &pb.GetItemRequest{Id: itemID})
	assert.Equal(t, codes.NotFound, status.Code(err))

	// A server-generated ID for a client that does not choose one.
	generated, err := c.items.CreateItem(alice, &pb.CreateItemRequest{Type: pb.ItemType_ITEM_TYPE_TEXT, Data: []byte("note")})
	require.NoError(t, err)
	assert.NotEmpty(t, generated.GetId())

	items, err := listAll(t, c, alice, 0)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, itemID, items[0].GetId())
	assert.True(t, items[0].GetDeleted())
	assert.Empty(t, items[0].GetData())
	assert.Equal(t, generated.GetId(), items[1].GetId())
	assert.Equal(t, int64(4), items[1].GetVersion())

	items, err = listAll(t, c, alice, 3)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, pb.ItemType_ITEM_TYPE_TEXT, items[0].GetType())

	_, err = listAll(t, c, alice, -1)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	items, err = listAll(t, c, bob, 0)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestItemService_LargeItem(t *testing.T) {
	c := newTestServer(t)
	alice := register(t, c, "alice")

	// Larger than the default gRPC limit of 4 MiB.
	data := make([]byte, 6<<20)
	_, err := c.items.CreateItem(alice, &pb.CreateItemRequest{Id: itemID, Type: pb.ItemType_ITEM_TYPE_BINARY, Data: data})
	require.NoError(t, err)

	items, err := listAll(t, c, alice, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Len(t, items[0].GetData(), len(data))

	_, err = c.items.CreateItem(alice, &pb.CreateItemRequest{Type: pb.ItemType_ITEM_TYPE_BINARY, Data: make([]byte, models.MaxItemSize+1)})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestLogger(t *testing.T) {
	c := newTestServer(t)
	alice := register(t, c, "alice")

	_, _ = c.items.GetItem(alice, &pb.GetItemRequest{Id: itemID})
	_, _ = listAll(t, c, alice, 0)

	entries := c.logs.FilterMessage("gRPC call").AllUntimed()
	require.Len(t, entries, 3)

	fields := entries[1].ContextMap()
	assert.Equal(t, pb.ItemService_GetItem_FullMethodName, fields["method"])
	assert.Equal(t, codes.NotFound.String(), fields["code"])
	assert.Contains(t, fields, "duration")
	assert.Contains(t, fields, "peer")

	assert.Equal(t, pb.ItemService_ListItems_FullMethodName, entries[2].ContextMap()["method"])
}

func TestHandlersWithoutInterceptor(t *testing.T) {
	srv := NewItemServer(service.NewItemService(repository.NewMemoryItemRepository()), zap.NewNop())
	ctx := context.Background()

	_, err := srv.CreateItem(ctx, &pb.CreateItemRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = srv.UpdateItem(ctx, &pb.UpdateItemRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = srv.DeleteItem(ctx, &pb.DeleteItemRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = srv.GetItem(ctx, &pb.GetItemRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestStatusFromError(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	log := zap.New(core)

	err := statusFromError(log, errors.New("connection refused to 10.0.0.5"))
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.NotContains(t, err.Error(), "10.0.0.5", "internals do not leak to the client")
	assert.Equal(t, 1, logs.Len(), "but they are logged")

	assert.Equal(t, codes.Canceled, status.Code(statusFromError(log, context.Canceled)))
	assert.Equal(t, codes.DeadlineExceeded, status.Code(statusFromError(log, context.DeadlineExceeded)))
	assert.Equal(t, codes.Unavailable, status.Code(statusFromError(log, status.Error(codes.Unavailable, "gone"))))
}

func TestItemTypeConversion(t *testing.T) {
	for _, typ := range []models.ItemType{
		models.ItemTypeCredentials, models.ItemTypeText, models.ItemTypeBinary, models.ItemTypeCard, models.ItemTypeOTP,
	} {
		assert.Equal(t, typ, itemTypeFromProto(itemTypeToProto(typ)))
	}

	assert.Equal(t, models.ItemType(0), itemTypeFromProto(pb.ItemType(65537)), "no wrap-around into a valid type")
	assert.Equal(t, pb.ItemType_ITEM_TYPE_UNSPECIFIED, itemTypeToProto(42))
}
