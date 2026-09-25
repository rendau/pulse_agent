package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_agent/internal/infra/httpx"
)

type resolveIn struct {
	Query string `json:"query" jsonschema:"имя или описание сервиса"`
}

type resolveOut struct {
	Name string `json:"name"`
}

// newPulseHandler — MCP-сервер как у pulse: instructions, типизированный инструмент,
// ошибка уровня инструмента. stateless — как в проде; stateful — чтобы проверить
// переподключение после рестарта (сервер забывает сессию).
func newPulseHandler(stateless bool) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "pulse", Version: "test"},
		&mcp.ServerOptions{Instructions: "сначала resolve_service"})

	mcp.AddTool(server, &mcp.Tool{Name: "resolve_service", Description: "находит сервис"},
		func(_ context.Context, _ *mcp.CallToolRequest, in resolveIn) (*mcp.CallToolResult, resolveOut, error) {
			if in.Query == "unknown" {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: "service not found; similar: caravan"}},
				}, resolveOut{}, nil
			}
			return nil, resolveOut{Name: in.Query}, nil
		})

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: stateless})
}

// testServer — pulse за bearer-токеном; handler можно подменить («рестарт»).
func testServer(t *testing.T, handler http.Handler) (*httptest.Server, *atomic.Pointer[http.Handler]) {
	current := &atomic.Pointer[http.Handler]{}
	current.Store(&handler)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		(*current.Load()).ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	return srv, current
}

func TestCatalogAndCall(t *testing.T) {
	srv, _ := testServer(t, newPulseHandler(true))
	svc := New(srv.URL, "secret", httpx.New(httpx.Config{}))
	t.Cleanup(func() { _ = svc.Close() })
	ctx := context.Background()

	catalog, err := svc.Catalog(ctx)
	require.NoError(t, err)

	assert.Equal(t, "сначала resolve_service", catalog.Instructions)
	require.Len(t, catalog.Tools, 1)
	tool := catalog.Tools[0]
	assert.Equal(t, "resolve_service", tool.Name)
	assert.Equal(t, "находит сервис", tool.Description)
	assert.Equal(t, "object", tool.InputSchema["type"])
	assert.Contains(t, tool.InputSchema["properties"], "query")

	res, err := svc.Call(ctx, "resolve_service", `{"query":"caravan"}`)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"name":"caravan"}`, res.Text)

	res, err = svc.Call(ctx, "resolve_service", `{"query":"unknown"}`)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Equal(t, "service not found; similar: caravan", res.Text)

	// кривой JSON от модели — ошибка для модели, а не для разбора
	res, err = svc.Call(ctx, "resolve_service", `{"query":`)
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestUnauthorized(t *testing.T) {
	srv, _ := testServer(t, newPulseHandler(true))
	svc := New(srv.URL, "wrong", httpx.New(httpx.Config{}))

	_, err := svc.Catalog(context.Background())
	require.Error(t, err)
}

func TestReconnectAfterRestart(t *testing.T) {
	srv, current := testServer(t, newPulseHandler(false))
	svc := New(srv.URL, "secret", httpx.New(httpx.Config{}))
	t.Cleanup(func() { _ = svc.Close() })
	ctx := context.Background()

	_, err := svc.Call(ctx, "resolve_service", `{"query":"a"}`)
	require.NoError(t, err)

	// «рестарт» pulse: новый сервер не знает старую сессию
	restarted := newPulseHandler(false)
	current.Store(&restarted)

	res, err := svc.Call(ctx, "resolve_service", `{"query":"b"}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"b"}`, res.Text)
}
