package http

import (
	"net/http"

	"github.com/rendau/pulse_agent/internal/handler/http/dto"
	findingModel "github.com/rendau/pulse_agent/internal/usecase/finding/model"
)

// PathFindings — приём внешних находок об атаках
const PathFindings = "/v1/findings"

func (h *Handler) registerFinding(mux *http.ServeMux) {
	mux.HandleFunc("POST "+PathFindings, h.withClient(h.Finding))
}

// Finding — POST /v1/findings: находка об атаке (расписание Claude через pulse) — сразу
// уведомление security в ленту; об адресе, о котором уже сообщали за WATCH_ALERT_REPEAT, — нет.
func (h *Handler) Finding(w http.ResponseWriter, r *http.Request, client string) {
	if h.finding == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "findings need PG_DSN")
		return
	}
	req := &dto.FindingReq{}
	if !decode(w, r, req) {
		return
	}
	res, err := h.finding.Report(r.Context(), client, &findingModel.Finding{
		Key: req.Key, Kind: req.Kind, Severity: req.Severity, Title: req.Title, Text: req.Text, Source: req.Source,
	})
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.FindingRep{Id: res.Id, Deduplicated: res.Deduplicated})
}
