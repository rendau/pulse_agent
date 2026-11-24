package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/rendau/pulse_agent/internal/errs"
	"github.com/rendau/pulse_agent/internal/handler/http/dto"
	blockModel "github.com/rendau/pulse_agent/internal/usecase/block/model"
)

func (h *Handler) registerBlock(mux *http.ServeMux) {
	mux.HandleFunc("POST "+PathNotifications+"/{id}/block", h.withClient(h.Block))
}

// Block — POST /v1/notifications/{id}/block: решение админа по уведомлению security (кнопка в
// боте) — заблокировать адрес в ruto или нет. Только системам из BLOCK_CLIENTS. ruto недоступен,
// блокировка не настроена или нет хранилища — 503 unavailable.
func (h *Handler) Block(w http.ResponseWriter, r *http.Request, client string) {
	if _, ok := h.blockClients[client]; !ok {
		writeError(w, http.StatusForbidden, codeForbidden, "block is not allowed for "+client)
		return
	}
	if h.block == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "blocking needs PG_DSN and RUTO_API_KEY")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "id: expected positive integer")
		return
	}
	req := &dto.BlockReq{}
	if !decode(w, r, req) {
		return
	}

	res, err := h.block.Decide(r.Context(), &blockModel.Request{
		Client: client, ConversationId: req.ConversationId, NotificationId: id,
		Action: req.Action, Duration: req.Duration, User: blockModel.User{Id: req.User.Id, Name: req.User.Name},
	})
	if errors.Is(err, errs.ServiceNA) {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, err.Error())
		return
	}
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, dto.EncodeBlock(res, h.loc))
}
