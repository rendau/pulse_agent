package http

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blockModel "github.com/rendau/pulse_agent/internal/domain/block/model"
	"github.com/rendau/pulse_agent/internal/errs"
	usecaseBlockModel "github.com/rendau/pulse_agent/internal/usecase/block/model"
)

type fakeBlock struct {
	got []*usecaseBlockModel.Request
	res *usecaseBlockModel.Result
	err error
}

func (f *fakeBlock) Decide(_ context.Context, req *usecaseBlockModel.Request) (*usecaseBlockModel.Result, error) {
	f.got = append(f.got, req)
	return f.res, f.err
}

const blockBody = `{"conversation_id":"tg:-100","action":"block","duration":"24h","user":{"id":"42","name":"Иван"}}`

func newBlockHandler(t *testing.T, block *fakeBlock) *Handler {
	h := newHandler(t, &fakeAsk{})
	h.blockClients = map[string]struct{}{"pulse_bot": {}}
	if block != nil {
		h.block = block
	}
	return h
}

func TestBlock(t *testing.T) {
	at := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)
	block := &fakeBlock{res: &usecaseBlockModel.Result{Decision: &blockModel.Decision{
		Id: 1, NotificationId: 57, Action: "block", Duration: "24h", Ip: "203.0.113.7/32", Until: new(at.Add(24 * time.Hour)),
		ById: "42", ByName: "Иван", At: at,
	}}}
	h := newBlockHandler(t, block)

	rec, rep := serveMethod(t, h, http.MethodPost, "/v1/notifications/57/block", "k-bot", blockBody)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]any{
		"notification_id": float64(57), "ip": "203.0.113.7/32", "action": "block",
		"until": "2026-11-25T10:00:00+05:00", "by": map[string]any{"id": "42", "name": "Иван"},
		"at": "2026-11-24T10:00:00+05:00", "repeat": false, "refused": "",
	}, rep)
	assert.Equal(t, &usecaseBlockModel.Request{Client: "pulse_bot", ConversationId: "tg:-100", NotificationId: 57,
		Action: "block", Duration: "24h", User: usecaseBlockModel.User{Id: "42", Name: "Иван"}}, block.got[0])

	// skip и отказ ruto: until пустой
	block.res = &usecaseBlockModel.Result{Decision: &blockModel.Decision{NotificationId: 57, Action: "skip", Ip: "203.0.113.7", ByName: "Иван", At: at}, Repeat: true}
	_, rep = serveMethod(t, h, http.MethodPost, "/v1/notifications/57/block", "k-bot", `{"conversation_id":"tg:-100","action":"skip","user":{"name":"Иван"}}`)
	assert.Equal(t, "", rep["until"])
	assert.Equal(t, true, rep["repeat"])
	assert.Equal(t, map[string]any{"name": "Иван"}, rep["by"])

	block.res = &usecaseBlockModel.Result{Decision: &blockModel.Decision{NotificationId: 57, Action: "block", Ip: "203.0.113.7", Refused: "private network", At: at}}
	_, rep = serveMethod(t, h, http.MethodPost, "/v1/notifications/57/block", "k-bot", blockBody)
	assert.Equal(t, "private network", rep["refused"])
	assert.Equal(t, "", rep["until"])
}

func TestBlock_Access(t *testing.T) {
	block := &fakeBlock{}
	h := newBlockHandler(t, block)

	rec, _ := serveMethod(t, h, http.MethodPost, "/v1/notifications/57/block", "", blockBody)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	for _, key := range []string{"k-sd", "k-debug"} {
		rec, rep := serveMethod(t, h, http.MethodPost, "/v1/notifications/57/block", key, blockBody)
		assert.Equal(t, http.StatusForbidden, rec.Code, key)
		assert.Equal(t, "forbidden", rep["code"], key+": не в BLOCK_CLIENTS (разработчику тоже нельзя)")
	}
	assert.Empty(t, block.got)

	empty := newHandler(t, &fakeAsk{})
	empty.block = block
	rec, _ = serveMethod(t, empty, http.MethodPost, "/v1/notifications/57/block", "k-bot", blockBody)
	assert.Equal(t, http.StatusForbidden, rec.Code, "BLOCK_CLIENTS пуст — никому")

	off := newBlockHandler(t, nil)
	rec, rep := serveMethod(t, off, http.MethodPost, "/v1/notifications/57/block", "k-bot", blockBody)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "unavailable", rep["code"], "нет RUTO_API_KEY или хранилища")
}

func TestBlock_Errors(t *testing.T) {
	block := &fakeBlock{}
	h := newBlockHandler(t, block)

	for _, path := range []string{"/v1/notifications/abc/block", "/v1/notifications/0/block", "/v1/notifications/-1/block"} {
		rec, rep := serveMethod(t, h, http.MethodPost, path, "k-bot", blockBody)
		assert.Equal(t, http.StatusBadRequest, rec.Code, path)
		assert.Equal(t, "invalid_request", rep["code"], path)
	}
	rec, rep := serveMethod(t, h, http.MethodPost, "/v1/notifications/57/block", "k-bot", `{"conversation_id":"tg:-100","action":"block","ttl":"1h"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "неизвестное поле")
	assert.Equal(t, "invalid_request", rep["code"])
	assert.Empty(t, block.got)

	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: duration %q", errs.InvalidRequest, "1h"), http.StatusBadRequest, "invalid_request"},
		{fmt.Errorf("notify.Notification: %w", fmt.Errorf("%w: notification 57", errs.ObjectNotFound)), http.StatusNotFound, "not_found"},
		{fmt.Errorf("ruto.Block: %w: status 502", errs.ServiceNA), http.StatusServiceUnavailable, "unavailable"},
		{fmt.Errorf("block.Record: boom"), http.StatusInternalServerError, "internal"},
	}
	for _, c := range cases {
		block.err = c.err
		rec, rep := serveMethod(t, h, http.MethodPost, "/v1/notifications/57/block", "k-bot", blockBody)
		assert.Equal(t, c.status, rec.Code, c.err.Error())
		assert.Equal(t, c.code, rep["code"], c.err.Error())
	}

	// ack по-прежнему своя ручка
	rec, _ = serveMethod(t, h, http.MethodPost, PathAck, "k-bot", `{"conversation_id":"tg:-100","last_id":1}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "без хранилища — 503, а не ручка блокировки")
}
