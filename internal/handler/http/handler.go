// Package http — JSON API агента для систем-клиентов (бот, service-desk, разбор алертов):
// POST /v1/ask, /v1/reset, /v1/eval. У каждой системы свой bearer-ключ (API_KEYS): по нему
// известно, кто спрашивает, — журнал, метрики и свои беседы. Ключ разработчика (DEBUG_TOKEN)
// открывает ещё /debug/* (debug.go): мониторинг и прогон эталонных вопросов.
package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/internal/errs"
	"github.com/mechta-market/pulse_agent/internal/handler/http/dto"
	askModel "github.com/mechta-market/pulse_agent/internal/usecase/ask/model"
)

// пути API
const (
	PathAsk   = "/v1/ask"
	PathReset = "/v1/reset"
	PathEval  = "/v1/eval"
)

// системы-клиенты, которых нет в API_KEYS
const (
	// ClientDebug — разработчик с DEBUG_TOKEN
	ClientDebug = "debug"
	// ClientEval — вопросы прогона эталонов (в журнале и метриках отдельно от систем)
	ClientEval = "eval"
)

const maxBodyBytes = 64 * 1024

// коды ошибок (dto.ErrorRep.Code)
const (
	codeInvalidRequest = "invalid_request"
	codeUnauthorized   = "unauthorized"
	codeBusy           = "busy"
	codeForbidden      = "forbidden"
	codeNotFound       = "not_found" // только /debug/journal/{id}
	codeTimeout        = "timeout"
	codeCanceled       = "canceled"
	codeInternal       = "internal"
)

// Config — доступ: ключи систем, ключ разработчика, кому можно запускать прогон.
type Config struct {
	// Keys — имя системы → её ключ (API_KEYS)
	Keys map[string]string
	// DebugToken — ключ разработчика: /debug/* и /v1/* (как система ClientDebug); пусто — /debug закрыт
	DebugToken string
	// EvalClients — системы, которым можно запускать прогон (/v1/eval), кроме разработчика
	EvalClients []string
	// EvalTimeout — потолок времени на прогон
	EvalTimeout time.Duration
}

type Handler struct {
	ask         AskUsecaseI
	monitor     MonitorUsecaseI
	keeper      EvalKeeperI
	keys        map[string][]byte // клиент → ключ
	evalClients map[string]struct{}
	evalTimeout time.Duration
	loc         *time.Location
}

func New(cfg Config, ask AskUsecaseI, monitor MonitorUsecaseI, keeper EvalKeeperI, loc *time.Location) *Handler {
	keys := lo.MapValues(cfg.Keys, func(key string, _ string) []byte { return []byte(key) })
	if cfg.DebugToken != "" {
		keys[ClientDebug] = []byte(cfg.DebugToken)
	}
	return &Handler{
		ask:         ask,
		monitor:     monitor,
		keeper:      keeper,
		keys:        keys,
		evalClients: lo.SliceToMap(append(cfg.EvalClients, ClientDebug), func(c string) (string, struct{}) { return c, struct{}{} }),
		evalTimeout: lo.Ternary(cfg.EvalTimeout > 0, cfg.EvalTimeout, 20*time.Minute),
		loc:         loc,
	}
}

// Register добавляет ручки в mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+PathAsk, h.withClient(h.Ask))
	mux.HandleFunc("POST "+PathReset, h.withClient(h.Reset))
	mux.HandleFunc("POST "+PathEval, h.withClient(h.Eval))
	h.registerDebug(mux)
}

// withClient — ключ из Authorization: Bearer → имя системы-клиента.
func (h *Handler) withClient(next func(w http.ResponseWriter, r *http.Request, client string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		client := ""
		if ok && got != "" {
			for name, key := range h.keys {
				if subtle.ConstantTimeCompare([]byte(got), key) == 1 {
					client = name
				}
			}
		}
		if client == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="pulse_agent"`)
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "unknown or missing API key")
			return
		}
		next(w, r, client)
	}
}

// Ask — POST /v1/ask: вопрос → ответ (синхронно, до AGENT_TIMEOUT).
func (h *Handler) Ask(w http.ResponseWriter, r *http.Request, client string) {
	req := &dto.AskReq{}
	if !decode(w, r, req) {
		return
	}
	req.Charts = lo.CoalesceOrEmpty(strings.TrimSpace(req.Charts), dto.ChartsAll)
	if !lo.Contains([]string{dto.ChartsAll, dto.ChartsPng, dto.ChartsData, dto.ChartsNone}, req.Charts) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "charts: expected all, png, data or none")
		return
	}

	rep, err := h.answer(r.Context(), client, req)
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, rep)
}

// answer — вопрос системы client: ответ API (тот же путь — у прогона эталонов в сервисе).
func (h *Handler) answer(ctx context.Context, client string, req *dto.AskReq) (*dto.AskRep, error) {
	started := time.Now()

	if req.Reset {
		if err := h.ask.Reset(ctx, client, req.ConversationId); err != nil {
			return nil, err
		}
	}

	answer, err := h.ask.Ask(ctx, &askModel.Question{
		Client:         client,
		ConversationId: req.ConversationId,
		User:           askModel.User{Id: req.User.Id, Name: req.User.Name},
		Text:           req.Question,
		Format:         strings.TrimSpace(req.Format),
		Charts:         lo.CoalesceOrEmpty(req.Charts, dto.ChartsAll) != dto.ChartsNone,
		ResponseSchema: req.ResponseSchema,
	})
	if err != nil {
		return nil, err
	}

	rep := dto.EncodeAskRep(answer, req, h.loc)
	rep.DurationMs = time.Since(started).Milliseconds()
	return rep, nil
}

// Eval — POST /v1/eval: прогон эталонных вопросов в сервисе (системы из EVAL_CLIENTS и
// разработчик); синхронно, до нескольких минут.
func (h *Handler) Eval(w http.ResponseWriter, r *http.Request, client string) {
	if _, ok := h.evalClients[client]; !ok {
		writeError(w, http.StatusForbidden, codeForbidden, "eval is not allowed for "+client)
		return
	}
	req := &dto.EvalReq{}
	if !decode(w, r, req) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.evalTimeout)
	defer cancel()

	report, err := h.keeper.Run(ctx, func(ctx context.Context, q *dto.AskReq) (*dto.AskRep, error) {
		return h.answer(ctx, ClientEval, q)
	}, "in-process ("+client+")", req.Only)
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.EvalRep{Text: report.Text(h.keeper.Baseline()), Report: report})
}

// Reset — POST /v1/reset: забыть историю беседы.
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request, client string) {
	req := &dto.ResetReq{}
	if !decode(w, r, req) {
		return
	}
	if err := h.ask.Reset(r.Context(), client, req.ConversationId); err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.ResetRep{Reset: true})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func writeFail(w http.ResponseWriter, r *http.Request, client string, err error) {
	switch {
	case errors.Is(err, errs.InvalidRequest):
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error())
	case errors.Is(err, errs.ObjectNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, errs.Busy):
		writeError(w, http.StatusConflict, codeBusy, lo.Ternary(err.Error() == errs.Busy.Error(),
			"previous question in this conversation is still running", err.Error()))
	case r.Context().Err() != nil:
		// клиент ушёл или сервис останавливается
		writeError(w, http.StatusServiceUnavailable, codeCanceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, codeTimeout, err.Error())
	default:
		slog.Error("question failed", "client", client, "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJson(w, status, &dto.ErrorRep{Code: code, Error: msg})
}

func writeJson(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("http: write response", "error", err)
	}
}
