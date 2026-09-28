package pulsekit

// ManifestRep — ответ /.well-known/pulse (docs/service-manifest.md, «Манифест»).
type ManifestRep struct {
	PulseManifest int             `json:"pulse_manifest"`
	Service       serviceRep      `json:"service"`
	Build         buildRep        `json:"build"`
	Runbooks      []runbookRep    `json:"runbooks,omitempty"`
	Dependencies  []dependencyRep `json:"dependencies,omitempty"`
	Metrics       []metricRep     `json:"metrics,omitempty"`
	Logs          *logsRep        `json:"logs,omitempty"`
	Domain        *domainRep      `json:"domain,omitempty"`
	Endpoints     []endpointRep   `json:"endpoints,omitempty"`
}

type domainRep struct {
	Responsibilities []string      `json:"responsibilities,omitempty"`
	NotResponsible   []boundaryRep `json:"not_responsible,omitempty"`
	Entities         []entityRep   `json:"entities,omitempty"`
	Questions        []questionRep `json:"questions,omitempty"`
}

type boundaryRep struct {
	What    string `json:"what"`
	Service string `json:"service,omitempty"`
}

type entityRep struct {
	Name        string            `json:"name"`
	IdPattern   string            `json:"id_pattern,omitempty"`
	IdExample   string            `json:"id_example,omitempty"`
	Description string            `json:"description,omitempty"`
	Statuses    []entityStatusRep `json:"statuses,omitempty"`
}

type entityStatusRep struct {
	Name       string `json:"name"`
	Meaning    string `json:"meaning,omitempty"`
	StuckAfter string `json:"stuck_after,omitempty"`
}

type questionRep struct {
	Question string `json:"question"`
	How      string `json:"how,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

func encodeDomain(d *Domain) *domainRep {
	if d == nil {
		return nil
	}
	rep := &domainRep{Responsibilities: d.Responsibilities}
	for _, b := range d.NotResponsible {
		rep.NotResponsible = append(rep.NotResponsible, boundaryRep(b))
	}
	for _, e := range d.Entities {
		entity := entityRep{Name: e.Name, IdPattern: e.IdPattern, IdExample: e.IdExample, Description: e.Description}
		for _, s := range e.Statuses {
			status := entityStatusRep{Name: s.Name, Meaning: s.Meaning}
			if s.StuckAfter > 0 {
				status.StuckAfter = s.StuckAfter.String()
			}
			entity.Statuses = append(entity.Statuses, status)
		}
		rep.Entities = append(rep.Entities, entity)
	}
	for _, q := range d.Questions {
		rep.Questions = append(rep.Questions, questionRep(q))
	}
	return rep
}

type serviceRep struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases,omitempty"`
	Owner       struct {
		Team     string   `json:"team"`
		Contacts []string `json:"contacts,omitempty"`
	} `json:"owner"`
	Criticality string `json:"criticality"`
	RepoUrl     string `json:"repo_url,omitempty"`
	DocsUrl     string `json:"docs_url,omitempty"`
}

type buildRep struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	BuiltAt string `json:"built_at,omitempty"`
}

type runbookRep struct {
	Title string `json:"title"`
	Url   string `json:"url"`
}

type dependencyRep struct {
	Id       string `json:"id"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Critical bool   `json:"critical"`
	Affects  string `json:"affects,omitempty"`
}

type metricRep struct {
	Id        string `json:"id"`
	Title     string `json:"title"`
	PromQL    string `json:"promql"`
	Unit      string `json:"unit,omitempty"`
	Direction string `json:"direction,omitempty"`
}

type logsRep struct {
	ErrorPatterns []errorPatternRep `json:"error_patterns,omitempty"`
}

type errorPatternRep struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// audienceHuman — ответ ручки только для человека, не для ИИ.
const audienceHuman = "human"

type endpointRep struct {
	Id          string              `json:"id"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Path        string              `json:"path"`
	Audience    string              `json:"audience,omitempty"`
	Params      map[string]paramRep `json:"params,omitempty"`
	TimeoutMs   int64               `json:"timeout_ms,omitempty"`
	Response    *schema             `json:"response,omitempty"`
	RowsPath    string              `json:"rows_path,omitempty"`
	MaxRows     int                 `json:"max_rows,omitempty"`
}

type paramRep struct {
	Type        string   `json:"type"`
	Pattern     string   `json:"pattern,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Default     any      `json:"default,omitempty"` // в типе параметра: 20, а не "20"
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description,omitempty"`
	Personal    string   `json:"x-personal,omitempty"`
}

// Manifest — манифест сервиса.
func (k *Kit) Manifest() ManifestRep {
	k.mu.RLock()
	defer k.mu.RUnlock()

	rep := ManifestRep{
		PulseManifest: ManifestVersion,
		Build:         buildRep{Version: k.build.Version, Commit: k.build.Commit, BuiltAt: k.build.BuiltAt},
	}
	s := k.service
	rep.Service = serviceRep{Name: s.Name, Title: s.Title, Description: s.Description, Aliases: s.Aliases,
		Criticality: s.Criticality, RepoUrl: s.RepoUrl, DocsUrl: s.DocsUrl}
	rep.Service.Owner.Team, rep.Service.Owner.Contacts = s.OwnerTeam, s.OwnerContacts
	for _, r := range s.Runbooks {
		rep.Runbooks = append(rep.Runbooks, runbookRep(r))
	}
	rep.Domain = encodeDomain(s.Domain)

	for _, d := range k.deps {
		rep.Dependencies = append(rep.Dependencies, dependencyRep{Id: d.Id, Kind: d.Kind, Target: d.Target, Critical: d.Critical, Affects: d.affects})
	}
	for _, m := range k.metrics {
		rep.Metrics = append(rep.Metrics, metricRep(m))
	}
	if len(k.errorPatterns) > 0 {
		rep.Logs = &logsRep{}
		for _, p := range k.errorPatterns {
			rep.Logs.ErrorPatterns = append(rep.Logs.ErrorPatterns, errorPatternRep(p))
		}
	}
	for _, e := range k.endpoints {
		params := make(map[string]paramRep, len(e.Params))
		for name, p := range e.Params {
			def, _ := typedDefault(p) // проверено при регистрации
			params[name] = paramRep{Type: cmpOr(p.Type, "string"), Pattern: p.Pattern, Enum: p.Enum, Min: p.Min, Max: p.Max,
				Default: def, Required: p.Required, Description: p.Description, Personal: p.Personal}
		}
		audience := ""
		if e.Human {
			audience = audienceHuman
		}
		rep.Endpoints = append(rep.Endpoints, endpointRep{Id: e.Id, Title: e.Title, Description: e.Description, Path: e.Path,
			Audience: audience, Params: params, TimeoutMs: e.Timeout.Milliseconds(), Response: e.response, RowsPath: e.RowsPath, MaxRows: e.MaxRows})
	}
	return rep
}
