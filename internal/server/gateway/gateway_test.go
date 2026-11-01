package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OneDayX/gophkeeper/internal/server/auth"
	"github.com/OneDayX/gophkeeper/internal/server/grpcserver"
	"github.com/OneDayX/gophkeeper/internal/server/repository"
	"github.com/OneDayX/gophkeeper/internal/server/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const itemID = "6f0d3c5e-8a3b-4d7e-9a43-1f1c2b7a9e01"

// newTestGateway serves the gateway in front of a real gRPC server with
// memory repositories.
func newTestGateway(t *testing.T) *httptest.Server {
	t.Helper()

	tokens := auth.NewTokens("test-key", time.Hour)
	srv := grpcserver.New(
		service.NewUserService(repository.NewMemoryUserRepository(), tokens),
		service.NewItemService(repository.NewMemoryItemRepository()),
		tokens, zap.NewNop(),
	)

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	handler, err := New(context.Background(), conn)
	require.NoError(t, err)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return ts
}

// send makes a request with the token and returns the status and the body.
func send(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", auth.BearerHeader(token))
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(data)
}

// field decodes a JSON object and returns one of its fields.
func field(t *testing.T, body, name string) any {
	t.Helper()

	var object map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &object), body)

	return object[name]
}

func TestGateway_Auth(t *testing.T) {
	ts := newTestGateway(t)

	code, body := send(t, http.MethodPost, ts.URL+"/api/v1/auth/register", "", `{"login":"alice","password":"secret"}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotEmpty(t, field(t, body, "token"))

	code, _ = send(t, http.MethodPost, ts.URL+"/api/v1/auth/register", "", `{"login":"alice","password":"secret"}`)
	assert.Equal(t, http.StatusConflict, code)

	code, _ = send(t, http.MethodPost, ts.URL+"/api/v1/auth/register", "", `{"login":""}`)
	assert.Equal(t, http.StatusBadRequest, code)

	code, body = send(t, http.MethodPost, ts.URL+"/api/v1/auth/login", "", `{"login":"alice","password":"secret"}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotEmpty(t, field(t, body, "token"))

	code, _ = send(t, http.MethodPost, ts.URL+"/api/v1/auth/login", "", `{"login":"alice","password":"wrong"}`)
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestGateway_Items(t *testing.T) {
	ts := newTestGateway(t)
	items := ts.URL + "/api/v1/items"

	_, body := send(t, http.MethodPost, ts.URL+"/api/v1/auth/register", "", `{"login":"alice","password":"secret"}`)
	token := field(t, body, "token").(string)

	code, _ := send(t, http.MethodGet, items, "", "")
	assert.Equal(t, http.StatusUnauthorized, code, "the token is required")

	data := base64.StdEncoding.EncodeToString([]byte("encrypted"))
	code, body = send(t, http.MethodPost, items, token, `{"id":"`+itemID+`","type":"ITEM_TYPE_TEXT","data":"`+data+`"}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, itemID, field(t, body, "id"))
	assert.Equal(t, "1", field(t, body, "version"), "int64 is a string in protobuf JSON")

	code, body = send(t, http.MethodGet, items+"/"+itemID, token, "")
	require.Equal(t, http.StatusOK, code, body)
	item := field(t, body, "item").(map[string]any)
	assert.Equal(t, "ITEM_TYPE_TEXT", item["type"])
	assert.Equal(t, data, item["data"])

	code, body = send(t, http.MethodPut, items+"/"+itemID, token, `{"data":"`+data+`","version":"1"}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "2", field(t, body, "version"))

	code, _ = send(t, http.MethodPut, items+"/"+itemID, token, `{"data":"`+data+`","version":"1"}`)
	assert.Equal(t, http.StatusConflict, code, "a stale version")

	// The stream comes as one JSON object per line.
	code, body = send(t, http.MethodGet, items+"?sinceVersion=0", token, "")
	require.Equal(t, http.StatusOK, code, body)
	lines := 0
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		lines++
		result := field(t, scanner.Text(), "result").(map[string]any)
		assert.Equal(t, itemID, result["item"].(map[string]any)["id"])
	}
	assert.Equal(t, 1, lines)

	code, body = send(t, http.MethodDelete, items+"/"+itemID+"?version=2", token, "")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "3", field(t, body, "version"))

	code, _ = send(t, http.MethodGet, items+"/"+itemID, token, "")
	assert.Equal(t, http.StatusNotFound, code)
}

func TestGateway_Docs(t *testing.T) {
	ts := newTestGateway(t)

	resp, err := http.Get(ts.URL + "/swagger.json")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var spec struct {
		Swagger string         `json:"swagger"`
		Paths   map[string]any `json:"paths"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&spec))
	assert.Equal(t, "2.0", spec.Swagger)
	assert.Contains(t, spec.Paths, "/api/v1/items/{id}")

	code, body := send(t, http.MethodGet, ts.URL+"/docs", "", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "SwaggerUIBundle")
}
