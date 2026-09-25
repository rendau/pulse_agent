package http

import (
	"net/http"
	"strconv"

	"github.com/samber/lo"

	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	"github.com/mechta-market/pulse_agent/internal/handler/http/dto"
)

// ручки разработчика (DEBUG_TOKEN): мониторинг и прогон эталонных вопросов
const (
	PathDebugEval     = "/debug/eval"
	PathDebugEvalLast = "/debug/eval/last"
	PathDebugRecent   = "/debug/recent"
	PathDebugStats    = "/debug/stats"
	PathDebugInfo     = "/debug/info"
)

func (h *Handler) registerDebug(mux *http.ServeMux) {
	mux.HandleFunc("POST "+PathDebugEval, h.withDebug(h.Eval))
	mux.HandleFunc("GET "+PathDebugEvalLast, h.withDebug(h.EvalLast))
	mux.HandleFunc("GET "+PathDebugRecent, h.withDebug(h.Recent))
	mux.HandleFunc("GET "+PathDebugStats, h.withDebug(h.Stats))
	mux.HandleFunc("GET "+PathDebugInfo, h.withDebug(h.Info))
}

// withDebug — только ключ разработчика.
func (h *Handler) withDebug(next func(w http.ResponseWriter, r *http.Request, client string)) http.HandlerFunc {
	return h.withClient(func(w http.ResponseWriter, r *http.Request, client string) {
		if client != ClientDebug {
			writeError(w, http.StatusForbidden, codeForbidden, "debug token required")
			return
		}
		next(w, r, client)
	})
}

// EvalLast — GET /debug/eval/last: последний прогон (с рестарта) и идёт ли прогон сейчас.
func (h *Handler) EvalLast(w http.ResponseWriter, _ *http.Request, _ string) {
	last, running := h.keeper.Last()
	rep := &dto.EvalRep{Running: running}
	if last != nil {
		rep.Text, rep.Report = last.Text(h.keeper.Baseline()), last
	}
	writeJson(w, http.StatusOK, rep)
}

// Recent — GET /debug/recent?client=&outcome=&limit=: последние вопросы, новые — первыми.
func (h *Handler) Recent(w http.ResponseWriter, r *http.Request, client string) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, err := h.monitor.Recent(r.Context(), journalModel.Filter{Client: q.Get("client"), Outcome: q.Get("outcome"), Limit: limit})
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, lo.Map(entries, dto.EncodeJournalEntry(h.loc)))
}

// Stats — GET /debug/stats: сводка по журналу по системам и инструментам.
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request, client string) {
	stats, err := h.monitor.Stats(r.Context())
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, dto.EncodeStats(stats, h.loc))
}

// Info — GET /debug/info: версия, модель, лимиты, клиенты, инструменты pulse.
func (h *Handler) Info(w http.ResponseWriter, r *http.Request, _ string) {
	writeJson(w, http.StatusOK, dto.EncodeInfo(h.monitor.Info(r.Context()), h.loc))
}
