// Package service — клиент чёрного списка ruto-core: POST /api/blocked_client с ключом ruto
// (scope blocklist — ключ ничего другого в ruto не может).
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rendau/pulse_agent/internal/errs"
	"github.com/rendau/pulse_agent/internal/infra/httpx"
	rutoblockModel "github.com/rendau/pulse_agent/internal/service/rutoblock/model"
)

const (
	blockPath    = "/api/blocked_client"
	maxBodyBytes = 64 << 10
)

type Service struct {
	baseUrl    string
	key        string
	httpClient *http.Client
}

// New — клиент основного HTTP-порта ruto-core (RUTO_API_URL, http://ruto-core.<namespace>) с
// ключом RUTO_API_KEY.
func New(baseUrl, key string) *Service {
	return &Service{
		baseUrl:    strings.TrimRight(baseUrl, "/"),
		key:        key,
		httpClient: httpx.New(httpx.Config{Timeout: 10 * time.Second}),
	}
}

func (s *Service) Block(ctx context.Context, req *rutoblockModel.BlockReq) (*rutoblockModel.Block, error) {
	rep := &blockRep{}
	if err := s.sendRequest(ctx, http.MethodPost, blockPath, encodeBlockReq(req), rep); err != nil {
		return nil, err
	}
	return rep.decode(), nil
}

// sendRequest — единственная точка отправки запросов: JSON-тело, ключ, статус, десериализация.
func (s *Service) sendRequest(ctx context.Context, method, path string, reqObj, repObj any) error {
	target := s.baseUrl + path

	raw, err := json.Marshal(reqObj)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.key)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return fmt.Errorf("%w: %s: %w", errs.ServiceNA, target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("%w: read body: %w", errs.ServiceNA, err)
	}
	switch {
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden:
		return &rutoblockModel.RefusedError{Message: errorMessage(body)}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("%w: %s: status %d: %s", errs.ServiceNA, target, resp.StatusCode, errorMessage(body))
	}
	if err = json.Unmarshal(body, repObj); err != nil {
		return fmt.Errorf("decode %s: %w", target, err)
	}
	return nil
}

// errorMessage — текст отказа ruto ({"code","message"}), иначе начало тела.
func errorMessage(body []byte) string {
	rep := &errorRep{}
	if json.Unmarshal(body, rep) == nil && rep.Message != "" {
		return truncate(rep.Message, 500)
	}
	return truncate(string(body), 200)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// транспортные модели

type blockReq struct {
	Ip              string `json:"ip"`
	Reason          string `json:"reason"`
	DurationSeconds int64  `json:"duration_seconds"`
}

func encodeBlockReq(v *rutoblockModel.BlockReq) blockReq {
	return blockReq{Ip: v.Ip, Reason: v.Reason, DurationSeconds: int64(v.Duration / time.Second)}
}

// int64 ruto отдаёт строками (protojson), нулевые и false-поля может опустить
type blockRep struct {
	Id            string `json:"id"`
	Ip            string `json:"ip"`
	Source        string `json:"source"`
	Active        bool   `json:"active"`
	ExpiresAtUnix string `json:"expires_at_unix"`
}

func (r *blockRep) decode() *rutoblockModel.Block {
	result := &rutoblockModel.Block{Id: r.Id, Ip: r.Ip, Source: r.Source, Active: r.Active}
	if v, err := strconv.ParseInt(r.ExpiresAtUnix, 10, 64); err == nil && v > 0 {
		result.ExpiresAt = time.Unix(v, 0)
	}
	return result
}

type errorRep struct {
	Message string `json:"message"`
}
