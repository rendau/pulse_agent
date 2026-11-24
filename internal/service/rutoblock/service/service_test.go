package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_agent/internal/errs"
	rutoblockModel "github.com/rendau/pulse_agent/internal/service/rutoblock/model"
)

func TestBlock(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/blocked_client", r.URL.Path)
		assert.Equal(t, "Bearer rtk_k1", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"id":"b1","ip":"203.0.113.7/32","source":"agent","active":true,"expires_at_unix":"1795600000","created_at_unix":"1795513600"}`))
	}))
	defer ts.Close()

	rep, err := New(ts.URL+"/", "rtk_k1").Block(context.Background(), &rutoblockModel.BlockReq{
		Ip: "203.0.113.7", Reason: "pulse: перебор паролей", Duration: 24 * time.Hour,
	})
	require.NoError(t, err)
	assert.Equal(t, &rutoblockModel.Block{Id: "b1", Ip: "203.0.113.7/32", Source: "agent", Active: true,
		ExpiresAt: time.Unix(1795600000, 0)}, rep)
	assert.Equal(t, map[string]any{"ip": "203.0.113.7", "reason": "pulse: перебор паролей", "duration_seconds": float64(86400)}, got)
}

func TestBlockWithoutEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"b2","ip":"203.0.113.0/24","source":"admin","active":true}`))
	}))
	defer ts.Close()

	rep, err := New(ts.URL, "rtk_k1").Block(context.Background(), &rutoblockModel.BlockReq{Ip: "203.0.113.9", Reason: "x", Duration: time.Hour})
	require.NoError(t, err)
	assert.True(t, rep.ExpiresAt.IsZero())
	assert.Equal(t, "admin", rep.Source)
}

func TestBlockErrors(t *testing.T) {
	status := http.StatusForbidden
	body := `{"code":"no_permission","message":"10.1.2.3/32 overlaps the private or special range 10.0.0.0/8"}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	s := New(ts.URL, "rtk_k1")
	req := &rutoblockModel.BlockReq{Ip: "10.1.2.3", Reason: "x", Duration: time.Hour}

	_, err := s.Block(context.Background(), req)
	require.ErrorIs(t, err, rutoblockModel.ErrRefused)
	assert.Contains(t, err.Error(), "overlaps the private or special range")

	refused, ok := errors.AsType[*rutoblockModel.RefusedError](err)
	require.True(t, ok)
	assert.Equal(t, "10.1.2.3/32 overlaps the private or special range 10.0.0.0/8", refused.Message, "текст ruto — как есть")

	status, body = http.StatusBadRequest, `{"code":"invalid_request","message":"network 203.0.0.0/16 is too wide"}`
	_, err = s.Block(context.Background(), req)
	require.ErrorIs(t, err, rutoblockModel.ErrRefused)

	status, body = http.StatusBadGateway, `upstream down`
	_, err = s.Block(context.Background(), req)
	require.ErrorIs(t, err, errs.ServiceNA)
	require.NotErrorIs(t, err, rutoblockModel.ErrRefused)
	assert.Contains(t, err.Error(), "status 502: upstream down")

	status, body = http.StatusUnauthorized, `{"code":"not_authorized","message":"not authorized"}`
	_, err = s.Block(context.Background(), req)
	require.ErrorIs(t, err, errs.ServiceNA)
	require.NotErrorIs(t, err, rutoblockModel.ErrRefused)
	assert.Contains(t, err.Error(), "status 401")
	assert.NotContains(t, err.Error(), "rtk_k1")
}

func TestBlockUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close()

	_, err := New(url, "rtk_k1").Block(context.Background(), &rutoblockModel.BlockReq{Ip: "203.0.113.7", Reason: "x", Duration: time.Hour})
	require.ErrorIs(t, err, errs.ServiceNA)
	assert.NotContains(t, err.Error(), "rtk_k1")
}
