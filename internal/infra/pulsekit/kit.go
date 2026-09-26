// Package pulsekit — манифест сервиса для pulse (стандарт — github.com/mechta-market/pulse,
// docs/service-manifest.md). Эталон пакета — gotemplate (internal/infra/pulsekit): в сервис он
// копируется целиком и на месте не правится. Сервис объявляет
// о себе сведения, зависимости (одной строкой рядом с созданием клиента), свои метрики,
// узнаваемые ошибки в логах, показатели состояния и диагностические ручки с типизированным
// ответом; пакет отдаёт /.well-known/pulse, /.well-known/pulse/status (фоновые проверки
// зависимостей и показателей) и сами ручки, схему ответа строит из Go-типа.
//
// Поля ответа с тегом `pulse:"personal=phone"` помечаются x-personal: значение отдаётся как есть,
// от модели его прячет pulse_agent. Строковому параметру нужен Pattern, Enum или Personal —
// свободная строка запрещена стандартом. Нарушения стандарта в объявлениях — паника при
// Нарушение стандарта при объявлении сервис не роняет: оно пишется в лог и в Problems(), а
// неправильный элемент (ручка, метрика, зависимость) не публикуется — остальной манифест
// работает. Длинные тексты — только предупреждение (Warnings()): pulse их обрежет. Тест
// манифеста сервиса требует пустой Problems() — ошибка видна в тестах, а не после выкатки.
package pulsekit

import (
	"context"
	"encoding/json"
	"errors"
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

// лимиты стандарта: длиннее — pulse обрезает (pulsekit предупреждает)
const (
	maxTitleChars        = 100
	maxTextChars         = 500
	maxEndpointDescChars = 1000 // описание ручки: на нём держится выбор агента
	maxMetrics           = 20
	maxGauges            = 20
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
	// Domain — бизнес-смысл для агента: за что отвечает, объекты и статусы, типичные вопросы
	Domain *Domain
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
	problems      []string
	warnings      []string

	wg sync.WaitGroup
}

type dependency struct {
	Id       string
	Kind     string
	Target   string
	Critical bool
	affects  string
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
	k := &Kit{conf: conf, build: build, handlers: map[string]http.HandlerFunc{}}
	k.service = k.validateService(service)
	return k
}

// validateService — сведения о сервисе: без обязательных pulse не примет манифест (в Problems);
// неправильная инструкция не публикуется.
func (k *Kit) validateService(s Service) Service {
	if s.Name == "" || s.Title == "" || s.Description == "" || s.OwnerTeam == "" {
		k.problem("service Name, Title, Description and OwnerTeam are required — pulse will not accept the manifest")
	}
	if !slices.Contains(criticalities, s.Criticality) {
		k.problem(fmt.Sprintf("service criticality %q: expected high, medium or low — pulse will not accept the manifest", s.Criticality))
	}
	k.checkText("service title", s.Title, maxTitleChars)
	k.checkText("service description", s.Description, maxTextChars)
	runbooks := make([]Runbook, 0, len(s.Runbooks))
	for _, r := range s.Runbooks {
		if u, err := url.Parse(r.Url); r.Title == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			k.problem(fmt.Sprintf("runbook %q: Title and http(s) Url are required — not published", r.Title))
			continue
		}
		k.checkText("runbook title", r.Title, maxTitleChars)
		runbooks = append(runbooks, r)
	}
	s.Runbooks = runbooks
	s.Domain = k.validateDomain(s.Domain)
	return s
}

// Problems — нарушения стандарта при объявлении: такие элементы не опубликованы. Тест манифеста
// сервиса требует пустой список.
func (k *Kit) Problems() []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return append(slices.Clone(k.problems), k.domainProblems()...)
}

// Warnings — тексты длиннее лимитов стандарта: опубликованы, pulse их обрежет.
func (k *Kit) Warnings() []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return slices.Clone(k.warnings)
}

func (k *Kit) problem(msg string) {
	slog.Error("pulsekit: manifest problem, the item is not published", "problem", msg)
	k.mu.Lock()
	defer k.mu.Unlock()
	k.problems = append(k.problems, msg)
}

func (k *Kit) warn(msg string) {
	slog.Warn("pulsekit: manifest warning", "warning", msg)
	k.mu.Lock()
	defer k.mu.Unlock()
	k.warnings = append(k.warnings, msg)
}

// violation — нарушение стандарта внутри объявления: прерывает его (fail) и попадает в
// Problems через catch; сервис при этом не падает.
type violation string

func fail(format string, args ...any) {
	panic(violation(fmt.Sprintf(format, args...)))
}

// catch — в начале каждого объявления: нарушение — в Problems, элемент не публикуется. Другие
// паники (ошибка в самом pulsekit) не глотаются.
func (k *Kit) catch() {
	if r := recover(); r != nil {
		v, ok := r.(violation)
		if !ok {
			panic(r)
		}
		k.problem(string(v))
	}
}

// cut — текст не длиннее n символов.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// checkText — текст длиннее лимита стандарта: предупреждение (pulse его обрежет).
func (k *Kit) checkText(what, text string, limit int) {
	if n := utf8.RuneCountInString(strings.TrimSpace(text)); n > limit {
		k.warn(fmt.Sprintf("%s is %d characters, the standard allows %d — pulse will cut it", what, n, limit))
	}
}

// Depend объявляет зависимость: id — ^[a-z][a-z0-9_]*$, kind — postgres, redis, kafka, http…,
// target — имя сервиса в кластере или хост без учётных данных (адрес из конфигурации — через
// Host), critical — без неё сервис не работает. check — «проверь, что работает» (Ping клиента).
// target с учётными данными или похожий на строку подключения не попадает в манифест: вместо
// него — unknown и предупреждение в лог (адрес приходит из окружения — ронять сервис нельзя).
// Проверка может вернуть Problem — свой статус и сообщение (так делает Passive).
func (k *Kit) Depend(id, kind, target string, critical bool, check Check) (decl *DependencyDecl) {
	d := &dependency{Id: id, Kind: kind, Target: strings.TrimSpace(target), Critical: critical, check: check}
	decl = &DependencyDecl{k: k, d: d} // и у неопубликованной: .Affects после неё не падает
	defer k.catch()
	switch {
	case !idRe.MatchString(id):
		fail("dependency id %q: expected %s — not published", id, idRe)
	case !slices.Contains(dependencyKinds, kind):
		fail("dependency %s: kind %q: expected one of %s — not published", id, kind, strings.Join(dependencyKinds, ", "))
	case check == nil:
		fail("dependency %s: check is nil — not published", id)
	}
	if d.Target == "" || strings.ContainsAny(d.Target, "@=") || userinfoRe.MatchString(d.Target) || strings.Contains(strings.ToLower(d.Target), "password") {
		slog.Warn("pulsekit: dependency target is empty or looks like a connection string — replaced with unknown (use pulsekit.Host)", "dependency", id)
		d.Target = "unknown"
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.deps = append(k.deps, d)
	return decl
}

// DependencyDecl — объявленная зависимость: уточнения цепочкой после Depend.
type DependencyDecl struct {
	k *Kit
	d *dependency
}

// Affects — что ломается, когда зависимость недоступна («выдача заказов», ≤ 100): critical
// говорит только «весь сервис down или нет», агенту нужно последствие.
func (d *DependencyDecl) Affects(what string) *DependencyDecl {
	d.k.checkText("dependency "+d.d.Id+" affects", what, maxTitleChars)
	d.k.mu.Lock()
	defer d.k.mu.Unlock()
	d.d.affects = strings.TrimSpace(what)
	return d
}

// Problem — результат проверки со своим статусом и сообщением: degraded или down — проблема;
// ok — зависимость в порядке, но с пометкой («вызовов не было, сеть до хоста есть»). Сообщение
// уходит в ручку состояния как есть — пишите его сами, без текста чужих ошибок (≤ 300).
type Problem struct {
	Status  string // ok | degraded | down
	Message string
}

func (p Problem) Error() string { return p.Status + ": " + p.Message }

// Metric объявляет свою метрику (если стандартных rps, ошибок и задержки недостаточно).
func (k *Kit) Metric(m Metric) {
	defer k.catch()
	switch {
	case !idRe.MatchString(m.Id) || strings.TrimSpace(m.PromQL) == "" || m.Title == "":
		fail("metric %q: Id (%s), Title and PromQL are required — not published", m.Id, idRe)
	case !slices.Contains(metricUnits, m.Unit):
		fail("metric %s: unit %q: expected count, ratio, seconds, bytes or rps — not published", m.Id, m.Unit)
	case !slices.Contains(directions, m.Direction):
		fail("metric %s: direction %q: expected higher_is_better or lower_is_better — not published", m.Id, m.Direction)
	}
	k.checkText("metric "+m.Id+" title", m.Title, maxTitleChars)
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.metrics) >= maxMetrics {
		fail("metric %s: more than %d metrics — not published", m.Id, maxMetrics)
	}
	k.metrics = append(k.metrics, m)
}

// ErrorPattern объявляет узнаваемую ошибку в логах: name — как её назовёт агент, pattern —
// регэксп RE2 (его исполняет pulse на строках логов сервиса).
func (k *Kit) ErrorPattern(name, pattern string) {
	defer k.catch()
	if name == "" || pattern == "" {
		fail("error pattern %q: name and pattern are required — not published", name)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		fail("error pattern %q: not an RE2 regexp: %s — not published", name, err)
	}
	k.checkText("error pattern name", name, maxTitleChars)
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
			problem, isProblem := errors.AsType[Problem](err)
			switch {
			case isProblem && (problem.Status == "ok" || problem.Status == "degraded" || problem.Status == "down"):
				res.status, res.message = problem.Status, cut(problem.Message, 300)
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

// describeRules — вид ошибки по её тексту, по порядку: сначала сеть и время, затем ответы
// системы (не найдено, отклонено, внутренняя ошибка). Коды — отдельным числом: «14040» не 404.
var describeRules = []struct {
	re   *regexp.Regexp
	text string
}{
	{regexp.MustCompile(`deadline|timeout|timed out`), "таймаут"},
	{regexp.MustCompile(`connection refused`), "в соединении отказано"},
	{regexp.MustCompile(`no such host|lookup .*: `), "хост не найден"},
	{regexp.MustCompile(`\b(401|403)\b|unauthori[sz]ed|unauthenticated|forbidden|permission denied|authentication`), "отказ в авторизации"},
	{regexp.MustCompile(`not configured`), "не настроена"},
	{regexp.MustCompile(`\b(502|503)\b|unavailable|connection reset|broken pipe|\beof\b|no route to host|network is unreachable`), "не отвечает"},
	{regexp.MustCompile(`\b404\b|not found|no rows|does not exist|не найден`), "не найдено"},
	{regexp.MustCompile(`\b(400|409|412|422|429)\b|invalid|bad request|rejected|validation|conflict|already|too many requests|precondition|некорректн|отклон`), "отклонено"},
	{regexp.MustCompile(`\b50[0-9]\b|internal`), "внутренняя ошибка"},
}

// Describe — короткое сообщение вместо текста ошибки: драйверы кладут в него строки
// подключения с паролями и данные запросов. Различает сеть (таймаут, отказ в соединении, хост
// не найден, не отвечает), авторизацию и ответы системы (не найдено, отклонено, внутренняя
// ошибка); вид не понятен — «ошибка». Годится для своих message и Error.Message.
func Describe(err error) string {
	if err == nil {
		return ""
	}
	return DescribeText(err.Error())
}

// DescribeText — то же для сохранённого текста ошибки (причина застревания в БД и т. п.);
// пустой текст — пусто.
func DescribeText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	for _, r := range describeRules {
		if r.re.MatchString(text) {
			return r.text
		}
	}
	return "ошибка"
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
