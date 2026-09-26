package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/samber/lo"

	"github.com/rendau/pulse_agent/internal/constant"
	pulseModel "github.com/rendau/pulse_agent/internal/service/pulse/model"
)

const connectTimeout = 15 * time.Second

// Service — MCP-клиент pulse. Сессия поднимается лениво при первом обращении и
// переподнимается, если pulse её потерял (рестарт пода, закрытое соединение), —
// бот стартует и отвечает «pulse недоступен», даже когда pulse лежит.
type Service struct {
	endpoint   string
	httpClient *http.Client
	client     *mcp.Client

	mu      sync.Mutex
	session *mcp.ClientSession
}

// New создаёт клиент; httpClient — из internal/infra/httpx, token — bearer
// (пусто — без авторизации, локальная разработка).
func New(endpoint, token string, httpClient *http.Client) *Service {
	if token != "" {
		httpClient.Transport = &bearerTransport{base: httpClient.Transport, token: token}
	}

	return &Service{
		endpoint:   endpoint,
		httpClient: httpClient,
		client: mcp.NewClient(
			&mcp.Implementation{Name: constant.ServiceName, Version: constant.Version},
			// пустые capabilities: roots/sampling/elicitation боту не нужны
			&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}},
		),
	}
}

func (s *Service) Catalog(ctx context.Context) (*pulseModel.Catalog, error) {
	var result *pulseModel.Catalog

	err := s.withSession(ctx, func(cs *mcp.ClientSession) error {
		catalog := &pulseModel.Catalog{}
		if init := cs.InitializeResult(); init != nil {
			catalog.Instructions = init.Instructions
		}

		for tool, err := range cs.Tools(ctx, nil) {
			if err != nil {
				return fmt.Errorf("list tools: %w", err)
			}

			schema, err := decodeSchema(tool.InputSchema)
			if err != nil {
				return fmt.Errorf("tool %s: input schema: %w", tool.Name, err)
			}
			catalog.Tools = append(catalog.Tools, pulseModel.Tool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: schema,
			})
		}

		result = catalog
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("pulse catalog: %w", err)
	}

	return result, nil
}

func (s *Service) Call(ctx context.Context, name string, arguments string) (*pulseModel.CallResult, error) {
	arguments = lo.CoalesceOrEmpty(strings.TrimSpace(arguments), "{}")
	if !json.Valid([]byte(arguments)) {
		// ошибка модели, а не pulse: вернуть ей, чтобы поправила вызов
		return &pulseModel.CallResult{Text: "invalid JSON in tool arguments", IsError: true}, nil
	}

	var result *pulseModel.CallResult

	err := s.withSession(ctx, func(cs *mcp.ClientSession) error {
		rep, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(arguments)})
		if err != nil {
			return err
		}

		result, err = decodeCallResult(rep)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pulse call %s: %w", name, err)
	}

	return result, nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session == nil {
		return nil
	}

	err := s.session.Close()
	s.session = nil
	return err
}

// withSession выполняет fn в текущей сессии; если сессия умерла на стороне pulse —
// переподключается и повторяет один раз.
func (s *Service) withSession(ctx context.Context, fn func(cs *mcp.ClientSession) error) error {
	cs, err := s.getSession(ctx)
	if err != nil {
		return err
	}

	err = fn(cs)
	if err == nil || !errors.Is(err, mcp.ErrConnectionClosed) && !errors.Is(err, mcp.ErrSessionMissing) {
		return err
	}

	s.dropSession(cs)

	cs, err = s.getSession(ctx)
	if err != nil {
		return err
	}

	return fn(cs)
}

func (s *Service) getSession(ctx context.Context) (*mcp.ClientSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session != nil {
		return s.session, nil
	}

	// сессия живёт дольше этого контекста: транспорт отвязывает её сам
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	cs, err := s.client.Connect(connectCtx, &mcp.StreamableClientTransport{
		Endpoint:   s.endpoint,
		HTTPClient: s.httpClient,
		// уведомления сервера не нужны: каталог перечитывается на каждый разбор
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", s.endpoint, err)
	}

	s.session = cs
	return cs, nil
}

func (s *Service) dropSession(cs *mcp.ClientSession) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session == cs {
		_ = cs.Close()
		s.session = nil
	}
}

// decodeSchema приводит схему инструмента (в SDK — any) к JSON-объекту.
func decodeSchema(schema any) (map[string]any, error) {
	if schema == nil {
		return map[string]any{"type": "object"}, nil
	}

	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}

	result := map[string]any{}
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// decodeCallResult собирает текст ответа инструмента; pulse кладёт JSON ответа
// в текстовый контент, structuredContent — запасной вариант.
func decodeCallResult(rep *mcp.CallToolResult) (*pulseModel.CallResult, error) {
	texts := lo.FilterMap(rep.Content, func(c mcp.Content, _ int) (string, bool) {
		text, ok := c.(*mcp.TextContent)
		if !ok {
			return "", false
		}
		return text.Text, true
	})

	result := &pulseModel.CallResult{Text: strings.Join(texts, "\n"), IsError: rep.IsError}

	if result.Text == "" && rep.StructuredContent != nil {
		raw, err := json.Marshal(rep.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("marshal structured content: %w", err)
		}
		result.Text = string(raw)
	}

	return result, nil
}

// bearerTransport добавляет Authorization ко всем запросам к pulse.
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}
