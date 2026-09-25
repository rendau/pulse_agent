// Package pulsekit — манифест сервиса для pulse (pulse/docs/service-manifest.md; копия
// pulse/internal/infra/pulsekit до модуля gotemplate — правки вносить в обе): сервис объявляет
// о себе сведения, зависимости (одной строкой рядом с созданием клиента) и диагностические ручки
// с типизированным ответом; пакет отдаёт /.well-known/pulse, /.well-known/pulse/status
// (фоновые проверки зависимостей) и сами ручки, схему ответа строит из Go-типа.
//
// Поля ответа с тегом `pulse:"personal=phone"` помечаются x-personal: значение отдаётся как есть,
// токеном его заменит pulse. Строковому параметру нужен Pattern, Enum или Personal — свободная
// строка запрещена стандартом (паника при регистрации: ошибка видна в тестах, а не после выкатки).
package pulsekit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ManifestVersion — версия стандарта манифеста.
const ManifestVersion = 1

const (
	// ManifestPath — путь манифеста; ручка состояния — ManifestPath + "/status"
	ManifestPath = "/.well-known/pulse"
	StatusPath   = ManifestPath + "/status"
)

// Service — кто я. Name, Title, Description, OwnerTeam, Criticality обязательны.
type Service struct {
	Name          string
	Title         string
	Description   string
	Aliases       []string
	OwnerTeam     string
	OwnerContacts []string
	Criticality   string // high | medium | low
	RepoUrl       string
	DocsUrl       string
}

// Build — из чего собран экземпляр (ldflags / переменные CI), не руками.
type Build struct {
	Version string
	Commit  string
	BuiltAt string
}

// Config — поведение фоновых проверок.
type Config struct {
	// CheckInterval — как часто проверять зависимости (по умолчанию 30 с)
	CheckInterval time.Duration
	// CheckTimeout — таймаут одной проверки (по умолчанию 5 с)
	CheckTimeout time.Duration
	// SlowAfter — проверка дольше этого — degraded (по умолчанию 2 с)
	SlowAfter time.Duration
}

// Check — проверка зависимости: nil — работает.
type Check func(ctx context.Context) error

// Kit — манифест, состояние и диагностические ручки сервиса.
type Kit struct {
	conf    Config
	service Service
	build   Build

	mu        sync.RWMutex
	deps      []*dependency
	endpoints []endpointDecl
	handlers  map[string]http.HandlerFunc
	checkedAt time.Time

	wg sync.WaitGroup
}

type dependency struct {
	Id       string
	Kind     string
	Target   string
	Critical bool
	check    Check

	status    string
	latencyMs int64
	message   string
}

var (
	idRe       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	secretRe   = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api_?key|apikey|private_?key|credential|dsn|cookie|session|authorization|signature)`)
	userinfoRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)
)

func New(conf Config, service Service, build Build) *Kit {
	if conf.CheckInterval <= 0 {
		conf.CheckInterval = 30 * time.Second
	}
	if conf.CheckTimeout <= 0 {
		conf.CheckTimeout = 5 * time.Second
	}
	if conf.SlowAfter <= 0 {
		conf.SlowAfter = 2 * time.Second
	}
	return &Kit{conf: conf, service: service, build: build, handlers: map[string]http.HandlerFunc{}}
}

// Depend объявляет зависимость: id — ^[a-z][a-z0-9_]*$, kind — postgres, redis, kafka, http…,
// target — имя сервиса в кластере или хост (без учётных данных), critical — без неё сервис не
// работает. check — «проверь, что работает» (Ping клиента).
func (k *Kit) Depend(id, kind, target string, critical bool, check Check) {
	if !idRe.MatchString(id) {
		panic(fmt.Sprintf("pulsekit: dependency id %q: expected %s", id, idRe))
	}
	if strings.Contains(target, "@") || userinfoRe.MatchString(target) {
		panic(fmt.Sprintf("pulsekit: dependency %s: target must be a host without credentials", id))
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.deps = append(k.deps, &dependency{Id: id, Kind: kind, Target: target, Critical: critical, check: check})
}

// Register добавляет в mux манифест, ручку состояния и диагностические ручки.
func (k *Kit) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+ManifestPath, func(w http.ResponseWriter, _ *http.Request) { writeJson(w, http.StatusOK, k.Manifest()) })
	mux.HandleFunc("GET "+StatusPath, func(w http.ResponseWriter, _ *http.Request) { writeJson(w, http.StatusOK, k.Status()) })
	k.mu.RLock()
	defer k.mu.RUnlock()
	for pattern, handler := range k.handlers {
		mux.HandleFunc("GET "+pattern, handler)
	}
}

// Start запускает фоновые проверки зависимостей (первая — сразу); Wait ждёт остановки после
// отмены ctx.
func (k *Kit) Start(ctx context.Context) {
	k.wg.Go(func() {
		k.checkAll(ctx)
		ticker := time.NewTicker(k.conf.CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				k.checkAll(ctx)
			}
		}
	})
}

func (k *Kit) Wait() {
	k.wg.Wait()
}

func (k *Kit) checkAll(ctx context.Context) {
	k.mu.RLock()
	deps := append([]*dependency{}, k.deps...)
	k.mu.RUnlock()

	type result struct {
		status, message string
		latency         int64
	}
	results := make([]result, len(deps))
	var wg sync.WaitGroup
	for i, d := range deps {
		wg.Go(func() {
			checkCtx, cancel := context.WithTimeout(ctx, k.conf.CheckTimeout)
			defer cancel()
			started := time.Now()
			err := d.check(checkCtx)
			took := time.Since(started)
			res := result{status: "ok", latency: took.Milliseconds()}
			switch {
			case err != nil:
				res.status, res.message = "down", describe(err)
			case took > k.conf.SlowAfter:
				res.status, res.message = "degraded", fmt.Sprintf("ответ дольше %s", k.conf.SlowAfter)
			}
			results[i] = res
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return // остановка сервиса: отменённые проверки — не состояние зависимостей
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	for i, d := range deps {
		d.status, d.message, d.latencyMs = results[i].status, results[i].message, results[i].latency
	}
	k.checkedAt = time.Now()
}

// describe — своё короткое сообщение вместо текста ошибки: драйверы кладут в него строки
// подключения с паролями и данные запросов.
func describe(err error) string {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "deadline") || strings.Contains(text, "timeout"):
		return "таймаут"
	case strings.Contains(text, "connection refused"):
		return "в соединении отказано"
	case strings.Contains(text, "no such host") || strings.Contains(text, "lookup"):
		return "хост не найден"
	case strings.Contains(text, "401") || strings.Contains(text, "403") || strings.Contains(text, "unauthorized") || strings.Contains(text, "authentication"):
		return "отказ в авторизации"
	case strings.Contains(text, "not configured"):
		return "не настроена"
	default:
		return "не отвечает"
	}
}

// Status — ответ ручки состояния: последний результат фоновых проверок.
func (k *Kit) Status() StatusRep {
	k.mu.RLock()
	defer k.mu.RUnlock()

	rep := StatusRep{Status: "ok", Dependencies: make([]DependencyStatusRep, 0, len(k.deps))}
	if !k.checkedAt.IsZero() {
		rep.CheckedAt = k.checkedAt.Format(time.RFC3339)
	}
	for _, d := range k.deps {
		status := d.status
		if status == "" {
			status = "ok" // ещё не проверяли: первая проверка идёт на старте
		}
		rep.Dependencies = append(rep.Dependencies, DependencyStatusRep{Id: d.Id, Status: status, LatencyMs: d.latencyMs, Message: d.message})
		switch {
		case status == "down" && d.Critical:
			rep.Status = "down"
		case status != "ok" && rep.Status == "ok":
			rep.Status = "degraded"
		}
	}
	return rep
}

// StatusRep — ответ /.well-known/pulse/status (docs/service-manifest.md, «Состояние»).
type StatusRep struct {
	Status       string                `json:"status"`
	CheckedAt    string                `json:"checked_at,omitempty"`
	Dependencies []DependencyStatusRep `json:"dependencies"`
}

type DependencyStatusRep struct {
	Id        string `json:"id"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Message   string `json:"message,omitempty"`
}

func writeJson(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
