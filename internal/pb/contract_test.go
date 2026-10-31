package pb

// The code of this package is generated, but the REST mapping it implements
// is a contract with the clients of the API, written down in the Swagger
// description. These tests pin it: every route must turn into the right gRPC
// request, with the path, query and body fields where the description puts
// them.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

// recorder implements both services and remembers the last request and the
// authorization it came with.
type recorder struct {
	UnimplementedAuthServiceServer
	UnimplementedItemServiceServer

	mu    sync.Mutex
	last  proto.Message
	token string
}

func (r *recorder) record(ctx context.Context, req proto.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.last = req
	r.token = ""
	if values := metadata.ValueFromIncomingContext(ctx, "authorization"); len(values) > 0 {
		r.token = values[0]
	}
}

func (r *recorder) request() (proto.Message, string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.last, r.token
}

func (r *recorder) Register(ctx context.Context, req *RegisterRequest) (*RegisterResponse, error) {
	r.record(ctx, req)
	return &RegisterResponse{Token: "token"}, nil
}

func (r *recorder) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	r.record(ctx, req)
	return &LoginResponse{Token: "token"}, nil
}

func (r *recorder) CreateItem(ctx context.Context, req *CreateItemRequest) (*CreateItemResponse, error) {
	r.record(ctx, req)
	return &CreateItemResponse{Id: req.GetId(), Version: 1}, nil
}

func (r *recorder) UpdateItem(ctx context.Context, req *UpdateItemRequest) (*UpdateItemResponse, error) {
	r.record(ctx, req)
	return &UpdateItemResponse{Version: req.GetVersion() + 1}, nil
}

func (r *recorder) DeleteItem(ctx context.Context, req *DeleteItemRequest) (*DeleteItemResponse, error) {
	r.record(ctx, req)
	return &DeleteItemResponse{Version: req.GetVersion() + 1}, nil
}

func (r *recorder) GetItem(ctx context.Context, req *GetItemRequest) (*GetItemResponse, error) {
	r.record(ctx, req)
	return &GetItemResponse{Item: &Item{Id: req.GetId(), Type: ItemType_ITEM_TYPE_TEXT, Data: []byte("x"), Version: 1}}, nil
}

func (r *recorder) ListItems(req *ListItemsRequest, stream grpc.ServerStreamingServer[ListItemsResponse]) error {
	r.record(stream.Context(), req)
	for _, id := range []string{"a", "b"} {
		if err := stream.Send(&ListItemsResponse{Item: &Item{Id: id}}); err != nil {
			return err
		}
	}
	return nil
}

// route is one call of the REST API and the gRPC request it must become.
type route struct {
	method, path, body string
	want               proto.Message
}

var routes = []route{
	{http.MethodPost, "/api/v1/auth/register", `{"login":"alice","password":"secret"}`,
		&RegisterRequest{Login: "alice", Password: "secret"}},
	{http.MethodPost, "/api/v1/auth/login", `{"login":"alice","password":"secret"}`,
		&LoginRequest{Login: "alice", Password: "secret"}},
	{http.MethodPost, "/api/v1/items", `{"id":"i1","type":"ITEM_TYPE_CARD","data":"ZGF0YQ=="}`,
		&CreateItemRequest{Id: "i1", Type: ItemType_ITEM_TYPE_CARD, Data: []byte("data")}},
	{http.MethodPut, "/api/v1/items/i1", `{"data":"ZGF0YQ==","version":"7"}`,
		&UpdateItemRequest{Id: "i1", Data: []byte("data"), Version: 7}},
	{http.MethodDelete, "/api/v1/items/i1?version=7", "",
		&DeleteItemRequest{Id: "i1", Version: 7}},
	{http.MethodGet, "/api/v1/items/i1", "",
		&GetItemRequest{Id: "i1"}},
}

// call sends a request to the gateway with a token and returns the status
// and the body.
func call(t *testing.T, gateway http.Handler, method, path, body string) (int, string) {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, req)

	data, err := io.ReadAll(rec.Body)
	require.NoError(t, err)

	return rec.Code, string(data)
}

// checkRoutes calls every route and checks the request the service gets.
func checkRoutes(t *testing.T, gateway http.Handler, r *recorder) {
	t.Helper()

	for _, rt := range routes {
		code, body := call(t, gateway, rt.method, rt.path, rt.body)
		require.Equal(t, http.StatusOK, code, "%s %s: %s", rt.method, rt.path, body)

		got, token := r.request()
		assert.True(t, proto.Equal(rt.want, got), "%s %s: got %v", rt.method, rt.path, got)
		assert.Equal(t, "Bearer token", token, "the Authorization header reaches the service")
	}
}

// TestGateway_InProcess maps the routes onto the services directly.
func TestGateway_InProcess(t *testing.T) {
	r := &recorder{}
	ctx := context.Background()

	mux := runtime.NewServeMux()
	require.NoError(t, RegisterAuthServiceHandlerServer(ctx, mux, r))
	require.NoError(t, RegisterItemServiceHandlerServer(ctx, mux, r))

	checkRoutes(t, mux, r)

	// Streaming needs a real gRPC connection.
	code, _ := call(t, mux, http.MethodGet, "/api/v1/items?sinceVersion=3", "")
	assert.Equal(t, http.StatusNotImplemented, code)

	// A bad body or path value is the client's mistake.
	code, _ = call(t, mux, http.MethodPost, "/api/v1/items", "{not json")
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = call(t, mux, http.MethodDelete, "/api/v1/items/i1?version=seven", "")
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestGateway_OverGRPC maps the routes onto a gRPC server, the way the
// GophKeeper server runs the gateway.
func TestGateway_OverGRPC(t *testing.T) {
	r := &recorder{}
	ctx := context.Background()

	srv := grpc.NewServer()
	RegisterAuthServiceServer(srv, r)
	RegisterItemServiceServer(srv, r)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dial := []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	mux := runtime.NewServeMux()
	require.NoError(t, RegisterAuthServiceHandlerFromEndpoint(ctx, mux, "passthrough:///bufnet", dial))
	require.NoError(t, RegisterItemServiceHandlerFromEndpoint(ctx, mux, "passthrough:///bufnet", dial))

	checkRoutes(t, mux, r)

	// The stream comes as one JSON object per line.
	code, body := call(t, mux, http.MethodGet, "/api/v1/items?sinceVersion=3", "")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, 2, strings.Count(strings.TrimSpace(body), "\n")+1)
	got, _ := r.request()
	assert.True(t, proto.Equal(&ListItemsRequest{SinceVersion: 3}, got))
}

// TestServices_Unimplemented checks the stubs a partial server falls back on.
func TestServices_Unimplemented(t *testing.T) {
	ctx := context.Background()
	auth := UnimplementedAuthServiceServer{}
	items := UnimplementedItemServiceServer{}

	_, err := auth.Register(ctx, nil)
	assert.Error(t, err)
	_, err = auth.Login(ctx, nil)
	assert.Error(t, err)
	_, err = items.CreateItem(ctx, nil)
	assert.Error(t, err)
	_, err = items.UpdateItem(ctx, nil)
	assert.Error(t, err)
	_, err = items.DeleteItem(ctx, nil)
	assert.Error(t, err)
	_, err = items.GetItem(ctx, nil)
	assert.Error(t, err)
	assert.Error(t, items.ListItems(nil, nil))
}

// TestMessages checks the JSON names of the API, which the Swagger
// description and the REST clients rely on.
func TestMessages(t *testing.T) {
	item := &Item{Id: "i1", Type: ItemType_ITEM_TYPE_OTP, Data: []byte("x"), Version: 5, Deleted: true}

	marshaler := &runtime.JSONPb{}
	data, err := marshaler.Marshal(item)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"i1","type":"ITEM_TYPE_OTP","data":"eA==","version":"5","deleted":true}`, string(data))

	var decoded Item
	require.NoError(t, marshaler.Unmarshal(data, &decoded))
	assert.True(t, proto.Equal(item, &decoded))

	assert.Equal(t, "ITEM_TYPE_OTP", ItemType_ITEM_TYPE_OTP.String())
	assert.Equal(t, "", (*Item)(nil).GetId(), "getters are safe on nil")
}
