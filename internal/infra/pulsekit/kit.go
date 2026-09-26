// Package pulsekit — манифест сервиса для pulse (pulse/docs/service-manifest.md; копия
// pulse/internal/infra/pulsekit; третья копия — в gotemplate, правки вносить во все три): сервис объявляет
// о себе сведения, зависимости (одной строкой рядом с созданием клиента), свои метрики,
// узнаваемые ошибки в логах, показатели состояния и диагностические ручки с типизированным
// ответом; пакет отдаёт /.well-known/pulse, /.well-known/pulse/status (фоновые проверки
// зависимостей и показателей) и сами ручки, схему ответа строит из Go-типа.
//
// Поля ответа с тегом `pulse:"personal=phone"` помечаются x-personal: значение отдаётся как есть,
// от модели его прячет pulse_agent. Строковому параметру нужен Pattern, Enum или Personal —
// свободная строка запрещена стандартом. Нарушения стандарта в объявлениях — паника при
// регистрации: ошибка видна в тестах, а не после выкатки. Значения, которые приходят из
// окружения (адреса зависимостей), не роняют сервис: небезопасный target заменяется на unknown.
package pulsekit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ManifestVersion — версия стандарта манифеста.
const ManifestVersion = 1

const (
	// ManifestPath — путь манифеста; ручка состояния — ManifestPath + "/status"
	ManifestPath = "/.well-known/pulse"
	StatusPath   = ManifestPath + "/status"
)

// лимиты стандарта: длиннее — pulse обрезает, поэтому pulsekit отказывает сразу
const (
	maxTitleChars = 100
	maxTextChars  = 500
	maxMetrics    = 20
	maxGauges     = 20
)

// Service — кто я. Name, Title (≤ 100), Description (≤ 500), OwnerTeam, Criticality обязательны.
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
	// Runbooks — инструкции для дежурных (http(s)-ссылки)
	Runbooks []Runbook
}

type Runbook struct {
	Title string
	Url   string
}

// Build — из чего собран экземпляр (ldflags / переменные CI), не руками.
type Build struct {
	Version string
	Commit  string
	BuiltAt string
}

// Config — поведение фоновых проверок.
type Config struct {
	// CheckInterval — как часто проверять зависимости и читать показатели (по умолчанию 30 с)
	CheckInterval time.Duration
	// CheckTimeout — таймаут одной проверки (по умолчанию 5 с)
	CheckTimeout time.Duration
	// SlowAfter — проверка дольше этого — degraded (по умолчанию 2 с)
	SlowAfter time.Duration
}

// Check — проверка зависимости: nil — работает.
type Check func(ctx context.Context) error

// Metric — своя метрика сервиса (стандарт, «metrics[]»): PromQL с плейсхолдерами {namespace},
// {pod_regex}, {service}; Unit — count, ratio, seconds, bytes, rps; Direction —
// higher_is_better или lower_is_better.
type Metric struct {
	Id        string
	Title     string
	PromQL    string
	Unit      string
	Direction string
}

// Kit — манифест, состояние и диагностические ручки сервиса.
type Kit struct {
	conf    Config
	service Service
	build   Build

	mu            sync.RWMutex
	deps          []*dependency
	gauges        []*gauge
	metrics       []Metric
	errorPatterns []errorPattern
	endpoints     []endpointDecl
	handlers      map[string]http.HandlerFunc
	checkedAt     time.Time

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

type errorPattern struct {
	Name    string
	Pattern string
}

var (
	idRe       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	secretRe   = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api_?key|apikey|private_?key|credential|dsn|cookie|session|authorization|signature)`)
	userinfoRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

	criticalities   = []string{"high", "medium", "low"}
	dependencyKinds = []string{"postgres", "redis", "kafka", "rabbitmq", "clickhouse", "elasticsearch", "s3", "http", "grpc", "other"}
	metricUnits     = []string{"", "count", "ratio", "seconds", "bytes", "rps"}
	directions      = []string{"", "higher_is_better", "lower_is_better"}
	personalKinds   = []string{"phone", "email", "iin", "customer_id", "name", "address", "document", "card", "other"}
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
	validateService(service)
	return &Kit{conf: conf, service: service, build: build, handlers: map[string]http.HandlerFunc{}}
}

func validateService(s Service) {
	switch {
	case s.Name == "" || s.Title == "" || s.Description == "" || s.OwnerTeam == "":
		panic("pulsekit: service Name, Title, Description and OwnerTeam are required")
	case !slices.Contains(criticalities, s.Criticality):
		panic(fmt.Sprintf("pulsekit: service criticality %q: expected high, medium or low", s.Criticality))
	}
	checkText("service title", s.Title, maxTitleChars)
	checkText("service description", s.Description, maxTextChars)
	for _, r := range s.Runbooks {
		if u, err := url.Parse(r.Url); r.Title == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			panic(fmt.Sprintf("pulsekit: runbook %q: Title and http(s) Url are required", r.Title))
		}
		checkText("runbook title", r.Title, maxTitleChars)
	}
}

// checkText — текст не длиннее лимита стандарта: pulse обрезал бы его молча.
func checkText(what, text string, limit int) {
	if n := utf8.RuneCountInString(strings.TrimSpace(text)); n > limit {
		panic(fmt.Sprintf("pulsekit: %s is %d characters, the standard allows %d (pulse would cut it)", what, n, limit))
	}
}

// Depend объявляет зависимость: id — ^[a-z][a-z0-9_]*$, kind — postgres, redis, kafka, http…,
// target — имя сервиса в кластере или хост без учётных данных (адрес из конфигурации — через
// Host), critical — без неё сервис не работает. check — «проверь, что работает» (Ping клиента).
// target с учётными данными или похожий на строку подключения не попадает в манифест: вместо
// него — unknown и предупреждение в лог (адрес приходит из окружения — ронять сервис нельзя).
func (k *Kit) Depend(id, kind, target string, critical bool, check Check) {
	if !idRe.MatchString(id) {
		panic(fmt.Sprintf("pulsekit: dependency id %q: expected %s", id, idRe))
	}
	if !slices.Contains(dependencyKinds, kind) {
		panic(fmt.Sprintf("pulsekit: dependency %s: kind %q: expected one of %s", id, kind, strings.Join(dependencyKinds, ", ")))
	}
	target = strings.TrimSpace(target)
	if target == "" || strings.ContainsAny(target, "@=") || userinfoRe.MatchString(target) || strings.Contains(strings.ToLower(target), "password") {
		slog.Warn("pulsekit: dependency target is empty or looks like a connection string — replaced with unknown (use pulsekit.Host)", "dependency", id)
		target = "unknown"
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.deps = append(k.deps, &dependency{Id: id, Kind: kind, Target: target, Critical: critical, check: check})
}

// Metric объявляет свою метрику (если стандартных rps, ошибок и задержки недостаточно).
func (k *Kit) Metric(m Metric) {
	switch {
	case !idRe.MatchString(m.Id) || strings.TrimSpace(m.PromQL) == "" || m.Title == "":
		panic(fmt.Sprintf("pulsekit: metric %q: Id (%s), Title and PromQL are required", m.Id, idRe))
	case !slices.Contains(metricUnits, m.Unit):
		panic(fmt.Sprintf("pulsekit: metric %s: unit %q: expected count, ratio, seconds, bytes or rps", m.Id, m.Unit))
	case !slices.Contains(directions, m.Direction):
		panic(fmt.Sprintf("pulsekit: metric %s: direction %q: expected higher_is_better or lower_is_better", m.Id, m.Direction))
	}
	checkText("metric "+m.Id+" title", m.Title, maxTitleChars)
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.metrics) >= maxMetrics {
		panic(fmt.Sprintf("pulsekit: more than %d metrics", maxMetrics))
	}
	k.metrics = append(k.metrics, m)
}

// ErrorPattern объявляет узнаваемую ошибку в логах: name — как её назовёт агент, pattern —
// регэксп RE2 (его исполняет pulse на строках логов сервиса).
func (k *Kit) ErrorPattern(name, pattern string) {
	if name == "" || pattern == "" {
		panic("pulsekit: error pattern: name and pattern are required")
	}
	regexp.MustCompile(pattern)
	checkText("error pattern name", name, maxTitleChars)
	k.mu.Lock()
	defer k.mu.Unlock()
	k.errorPatterns = append(k.errorPatterns, errorPattern{Name: name, Pattern: pattern})
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

// Start запускает фоновые проверки зависимостей и чтение показателей (первые — сразу); Wait
// ждёт остановки после отмены ctx.
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
	gauges := append([]*gauge{}, k.gauges...)
	k.mu.RUnlock()

	type result struct {
		status, message string
		latency         int64
	}
	results := make([]result, len(deps))
	readings := make([]gaugeReading, len(gauges))
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
				res.status, res.message = "down", Describe(err)
			case took > k.conf.SlowAfter:
				res.status, res.message = "degraded", fmt.Sprintf("ответ дольше %s", k.conf.SlowAfter)
			}
			results[i] = res
		})
	}
	for i, g := range gauges {
		wg.Go(func() {
			readCtx, cancel := context.WithTimeout(ctx, k.conf.CheckTimeout)
			defer cancel()
			readings[i] = g.read(readCtx)
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
	for i, g := range gauges {
		g.last = readings[i]
	}
	k.checkedAt = time.Now()
}

// Describe — короткое сообщение вместо текста ошибки: драйверы кладут в него строки
// подключения с паролями и данные запросов. Годится для своих message и Error.Message.
func Describe(err error) string {
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

// Status — ответ ручки состояния: последний результат фоновых проверок. down — упала
// критичная зависимость; degraded — что-то ещё не ok (зависимость или показатель).
func (k *Kit) Status() StatusRep {
	k.mu.RLock()
	defer k.mu.RUnlock()

	rep := StatusRep{Status: "ok", Dependencies: make([]DependencyStatusRep, 0, len(k.deps))}
	if !k.checkedAt.IsZero() {
		rep.CheckedAt = k.checkedAt.Format(time.RFC3339)
	}
	worse := func(status string, critical bool) {
		switch {
		case status == "down" && critical:
			rep.Status = "down"
		case status != "ok" && status != "" && rep.Status == "ok":
			rep.Status = "degraded"
		}
	}
	for _, d := range k.deps {
		status := d.status
		if status == "" {
			status = "ok" // ещё не проверяли: первая проверка идёт на старте
		}
		rep.Dependencies = append(rep.Dependencies, DependencyStatusRep{Id: d.Id, Status: status, LatencyMs: d.latencyMs, Message: d.message})
		worse(status, d.Critical)
	}
	for _, g := range k.gauges {
		if g.last.value == nil {
			continue // ещё не прочитан или чтение не удалось
		}
		rep.Gauges = append(rep.Gauges, GaugeRep{Id: g.id, Title: g.title, Value: g.last.value, Unit: g.unit, Status: g.last.status})
		worse(g.last.status, false)
	}
	return rep
}

// StatusRep — ответ /.well-known/pulse/status (docs/service-manifest.md, «Состояние»).
type StatusRep struct {
	Status       string                `json:"status"`
	CheckedAt    string                `json:"checked_at,omitempty"`
	Dependencies []DependencyStatusRep `json:"dependencies"`
	Gauges       []GaugeRep            `json:"gauges,omitempty"`
}

type DependencyStatusRep struct {
	Id        string `json:"id"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Message   string `json:"message,omitempty"`
}

type GaugeRep struct {
	Id     string `json:"id"`
	Title  string `json:"title"`
	Value  any    `json:"value"` // число или время RFC 3339
	Unit   string `json:"unit,omitempty"`
	Status string `json:"status,omitempty"`
}

func writeJson(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
