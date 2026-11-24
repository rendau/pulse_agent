package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
)

const (
	// securityOverlap — окно get_edge_traffic длиннее интервала опроса на столько: запросы на
	// стыке опросов и опоздавшие строки журнала не теряются
	securityOverlap = 5 * time.Minute
	// securityMaxWindow — дальше get_edge_traffic не смотрит
	securityMaxWindow = 24 * time.Hour
	// securityTop — чужих адресов в сводке: доля 4xx известна только для них
	securityTop = 50
	// securityPerPoll — сигналов атаки за опрос не больше (самые сильные): распределённая атака с
	// сотен адресов не заваливает ленту и лимит разборов
	securityPerPoll = 5
)

// уровни сигнала атаки: probe — только пути взломщиков (разведка); attack — перебор или
// чувствительные ручки. Ключ сигнала — адрес и уровень: разведка днём не глушит перебор вечером.
const (
	levelProbe  = "probe"
	levelAttack = "attack"
)

// strongSignatures — признаки взлома в путях, которых у обычных клиентов не бывает: обход
// каталогов, закодированные точки и слэши, адрес метаданных облака. Остальные (api_docs, admin,
// actuator, dotenv, git, wordpress) — фон сканеров интернета и любопытных пользователей.
var strongSignatures = []string{"path_traversal", "encoded_dot", "encoded_slash", signatureMetadata}

// signatureMetadata — обращение к адресу метаданных облака (169.254.169.254)
const signatureMetadata = "cloud_metadata"

// SecurityConfig — пороги сигнала атаки за окно опроса (интервал + securityOverlap), по чужим
// адресам (свои — own в ответе pulse — не проверяются).
type SecurityConfig struct {
	// Interval — опрос get_edge_traffic; 0 — не опрашивать
	Interval time.Duration
	// MaxRunsPerHour — разборов атак в час, отдельно от выкаток и алертов
	MaxRunsPerHour int
	// перебор: ответов 4xx одному адресу не меньше Brute4xxMin и их доля не меньше Brute4xxShare
	Brute4xxMin   int
	Brute4xxShare float64
	// StrongMin — запросов с явными признаками взлома (strongSignatures) не меньше
	StrongMin int
	// MetadataMin — обращений к адресу метаданных облака (169.254.169.254) не меньше: снаружи
	// туда не ходит никто, кроме взломщика
	MetadataMin int
	// ProbeMin — запросов с любыми признаками взлома (разведка: документация API, admin,
	// actuator, .env, .git) не меньше; 0 — не смотреть: у сканеров интернета это фон
	ProbeMin int
	// SensitiveMin — обращений к чувствительным ручкам с одного адреса не меньше
	SensitiveMin int
	// SensitiveRareMin и SensitiveRare — ручки, куда снаружи обычно не ходят (права, токены,
	// платежи, 1С): обращений не меньше SensitiveRareMin (меньше SensitiveMin)
	SensitiveRareMin int
	SensitiveRare    []string
}

// edgeRep — нужная наблюдателю часть ответа get_edge_traffic.
type edgeRep struct {
	Start      time.Time       `json:"start"`
	End        time.Time       `json:"end"`
	Window     string          `json:"window"`
	Clients    []edgeClient    `json:"clients"`
	Signatures []edgeSignature `json:"signatures"`
	Sensitive  []edgeSensitive `json:"sensitive"`
	Partial    bool            `json:"partial"`
}

type edgeCount struct {
	Value string  `json:"value"`
	Count float64 `json:"count"`
}

type edgeClient struct {
	Addr          string      `json:"addr"`
	Own           string      `json:"own"`
	Requests      float64     `json:"requests"`
	Share4xx      *float64    `json:"share_4xx"`
	DistinctPaths int         `json:"distinct_paths"`
	Hosts         []edgeCount `json:"hosts"`
	UserAgents    []edgeCount `json:"user_agents"`
	Countries     []edgeCount `json:"countries"`
	New           *bool       `json:"new"`
}

type edgeSignature struct {
	Addr       string      `json:"addr"`
	Own        string      `json:"own"`
	Hits       float64     `json:"hits"`
	Requests   float64     `json:"requests"`
	Signatures []edgeCount `json:"signatures"`
}

type edgeSensitive struct {
	Endpoint string `json:"endpoint"`
	Top      []struct {
		Addr string  `json:"addr"`
		Own  string  `json:"own"`
		Hits float64 `json:"hits"`
	} `json:"top"`
}

// suspect — что чужой адрес делал за окно (из сводки pulse).
type suspect struct {
	Addr       string         `json:"addr"`
	Window     string         `json:"window"`
	Requests   int            `json:"requests,omitempty"`
	Resp4xx    int            `json:"responses_4xx,omitempty"`
	Share4xx   float64        `json:"share_4xx,omitempty"`
	Paths      int            `json:"distinct_paths,omitempty"`
	Strong     int            `json:"strong_signature_hits,omitempty"`
	Metadata   int            `json:"cloud_metadata_hits,omitempty"`
	Probe      int            `json:"signature_hits,omitempty"`
	Signatures map[string]int `json:"signatures,omitempty"`
	Sensitive  map[string]int `json:"sensitive,omitempty"`
	Country    string         `json:"country,omitempty"`
	Hosts      []string       `json:"hosts,omitempty"`
	New        *bool          `json:"new,omitempty"`
	// UserAgents — НЕДОВЕРЕННЫЙ текст атакующего: только разбору как данные, не в заголовок
	UserAgents []string `json:"user_agents,omitempty"`

	Reasons []string `json:"reasons"`
	Level   string   `json:"level"`
	score   int
}

var (
	reCountry  = regexp.MustCompile(`^[A-Z]{2}$`)
	reWord     = regexp.MustCompile(`^[a-z_]{1,32}$`)
	reEndpoint = regexp.MustCompile(`^[A-Za-z0-9_/.-]{1,64}$`)
	reHost     = regexp.MustCompile(`^[a-z0-9.-]{1,100}$`)
)

// validAddr — адрес из сводки — IPv4/IPv6 или подсеть: в ключ, заголовок и вопрос идёт только он.
func validAddr(addr string) bool {
	if _, err := netip.ParseAddr(addr); err == nil {
		return true
	}
	_, err := netip.ParsePrefix(addr)
	return err == nil
}

// suspects — чужие адреса окна, перешедшие пороги, самые сильные первыми.
func suspects(rep *edgeRep, cfg SecurityConfig, window string) []*suspect {
	by := map[string]*suspect{}
	get := func(addr string) *suspect {
		if s, ok := by[addr]; ok {
			return s
		}
		s := &suspect{Addr: addr, Window: window, Signatures: map[string]int{}, Sensitive: map[string]int{}}
		by[addr] = s
		return s
	}

	for _, c := range rep.Clients {
		if c.Own != "" || !validAddr(c.Addr) {
			continue
		}
		s := get(c.Addr)
		s.Requests, s.Paths, s.New = int(c.Requests), c.DistinctPaths, c.New
		if c.Share4xx != nil {
			s.Share4xx = *c.Share4xx
			s.Resp4xx = int(c.Requests * *c.Share4xx)
		}
		s.Country, _ = lo.Find(lo.Map(c.Countries, func(v edgeCount, _ int) string { return v.Value }), reCountry.MatchString)
		s.Hosts = lo.Slice(lo.Filter(lo.Map(c.Hosts, func(v edgeCount, _ int) string { return v.Value }), func(h string, _ int) bool {
			return reHost.MatchString(h)
		}), 0, 5)
		s.UserAgents = lo.Slice(lo.Map(c.UserAgents, func(v edgeCount, _ int) string { return lo.Ellipsis(v.Value, 150) }), 0, 3)
	}
	for _, v := range rep.Signatures {
		if v.Own != "" || !validAddr(v.Addr) {
			continue
		}
		s := get(v.Addr)
		s.Requests = max(s.Requests, int(v.Requests))
		for _, sig := range v.Signatures {
			if !reWord.MatchString(sig.Value) {
				continue
			}
			s.Signatures[sig.Value] += int(sig.Count)
			if slices.Contains(strongSignatures, sig.Value) {
				s.Strong += int(sig.Count)
			}
			if sig.Value == signatureMetadata {
				s.Metadata += int(sig.Count)
			}
		}
		s.Probe = int(v.Hits)
	}
	for _, e := range rep.Sensitive {
		if !reEndpoint.MatchString(e.Endpoint) {
			continue
		}
		for _, t := range e.Top {
			if t.Own != "" || !validAddr(t.Addr) {
				continue
			}
			get(t.Addr).Sensitive[e.Endpoint] += int(t.Hits)
		}
	}

	out := make([]*suspect, 0)
	for _, s := range by {
		sensitive := lo.Sum(lo.Values(s.Sensitive))
		rare := lo.Sum(lo.FilterMapToSlice(s.Sensitive, func(e string, n int) (int, bool) {
			return n, lo.ContainsBy(cfg.SensitiveRare, func(r string) bool { return r != "" && strings.Contains(e, r) })
		}))
		if cfg.Brute4xxMin > 0 && s.Resp4xx >= cfg.Brute4xxMin && s.Share4xx >= cfg.Brute4xxShare {
			s.Reasons = append(s.Reasons, "bruteforce")
		}
		if cfg.SensitiveMin > 0 && sensitive >= cfg.SensitiveMin || cfg.SensitiveRareMin > 0 && rare >= cfg.SensitiveRareMin {
			s.Reasons = append(s.Reasons, "sensitive")
		}
		if cfg.StrongMin > 0 && s.Strong >= cfg.StrongMin || cfg.MetadataMin > 0 && s.Metadata >= cfg.MetadataMin {
			s.Reasons = append(s.Reasons, "exploit_paths")
		}
		if cfg.ProbeMin > 0 && s.Probe >= cfg.ProbeMin {
			s.Reasons = append(s.Reasons, "probing")
		}
		if len(s.Reasons) == 0 {
			continue
		}
		s.Level = levelProbe
		if lo.Contains(s.Reasons, "bruteforce") || lo.Contains(s.Reasons, "sensitive") {
			s.Level = levelAttack
		}
		s.score = s.Resp4xx + sensitive + 100*rare + 100*s.Strong + 1000*s.Metadata + 10*s.Probe
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Level == levelAttack) != (out[j].Level == levelAttack) {
			return out[i].Level == levelAttack
		}
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].Addr < out[j].Addr
	})
	return out
}

// summary — сводка адреса для заголовка и уведомления без разбора: только адрес, страна, числа и
// слова из словаря pulse (признаки, ручки) — текст атакующего (пути, User-Agent) сюда не идёт.
func (s *suspect) summary() string {
	who := s.Addr
	if s.Country != "" {
		who += " (" + s.Country + ")"
	}
	parts := make([]string, 0, 4)
	if s.Resp4xx > 0 && lo.Contains(s.Reasons, "bruteforce") {
		parts = append(parts, fmt.Sprintf("перебор — %d ответов 4xx (%.0f%% запросов)", s.Resp4xx, 100*s.Share4xx))
	}
	if len(s.Sensitive) > 0 {
		parts = append(parts, "ручки: "+countsText(s.Sensitive))
	}
	if len(s.Signatures) > 0 {
		parts = append(parts, "признаки взлома: "+countsText(s.Signatures))
	}
	text := fmt.Sprintf("атака с %s за %s: %s", who, s.Window, strings.Join(parts, "; "))
	if s.Requests > 0 {
		text += fmt.Sprintf("; всего запросов %d", s.Requests)
	}
	return text
}

// countsText — «/cb/sms/ ×7555, /account/ ×221»: больше — первыми, до 5.
func countsText(m map[string]int) string {
	keys := lo.Keys(m)
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return strings.Join(lo.Map(lo.Slice(keys, 0, 5), func(k string, _ int) string { return fmt.Sprintf("%s ×%d", k, m[k]) }), ", ")
}

// securityWindow — окно опроса: интервал с перекрытием, не больше суток.
func (s *Service) securityWindow() time.Duration {
	return min(s.cfg.Security.Interval+securityOverlap, securityMaxWindow)
}

// collectSecurity — сигналы атак из сводки внешних запросов (один вызов get_edge_traffic).
func (s *Service) collectSecurity(ctx context.Context) error {
	window := s.securityWindow()
	var rep edgeRep
	if err := s.call(ctx, "get_edge_traffic", map[string]any{"window": window.String(), "top": securityTop}, &rep); err != nil {
		return err
	}
	now := s.now()
	label := lo.CoalesceOrEmpty(rep.Window, window.String())
	found := suspects(&rep, s.cfg.Security, label)
	for _, v := range lo.Slice(found, 0, securityPerPoll) {
		details, _ := json.Marshal(v)
		signal := &notifyModel.Signal{
			Key: fmt.Sprintf("security:%s:%s", v.Addr, v.Level), Kind: notifyModel.KindSecurity, At: now, DueAt: now,
			Summary: v.summary(), Details: details,
		}
		added, err := s.notify.Observe(ctx, signal, s.cfg.AlertRepeat)
		if err != nil {
			return fmt.Errorf("notify.Observe: %w", err)
		}
		if added {
			slogSignal(signal)
		}
	}
	return nil
}

// dueSecurity — пора опрашивать журнал входящих запросов.
func (s *Service) dueSecurity(now time.Time) bool {
	return s.cfg.Security.Interval > 0 && now.Sub(s.securityPolled) >= s.cfg.Security.Interval
}

// securityRef — адрес атаки: ключ уведомления (приглушить можно этот адрес).
func securityRef(details map[string]any) string {
	return firstString(details, "addr")
}

// securityLevel — уровень сигнала атаки из деталей.
func securityLevel(signal *notifyModel.Signal) string {
	var details map[string]any
	_ = json.Unmarshal(signal.Details, &details)
	return firstString(details, "level")
}

// securityQuestion — вопрос разбора атаки: адрес, время и окно, сводка и детали сигнала.
func securityQuestion(signal *notifyModel.Signal) string {
	var details map[string]any
	_ = json.Unmarshal(signal.Details, &details)
	return fmt.Sprintf(securityQuestionText, securityRef(details), signal.At.Format(time.RFC3339),
		firstString(details, "window"), signal.Summary, string(signal.Details))
}
