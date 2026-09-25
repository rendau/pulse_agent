package pulsekit

// ManifestRep — ответ /.well-known/pulse (docs/service-manifest.md, «Манифест»).
type ManifestRep struct {
	PulseManifest int             `json:"pulse_manifest"`
	Service       serviceRep      `json:"service"`
	Build         buildRep        `json:"build"`
	Dependencies  []dependencyRep `json:"dependencies,omitempty"`
	Endpoints     []endpointRep   `json:"endpoints,omitempty"`
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

type dependencyRep struct {
	Id       string `json:"id"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Critical bool   `json:"critical"`
}

type endpointRep struct {
	Id          string              `json:"id"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Path        string              `json:"path"`
	Params      map[string]paramRep `json:"params,omitempty"`
	TimeoutMs   int64               `json:"timeout_ms,omitempty"`
	Response    *schema             `json:"response"`
	RowsPath    string              `json:"rows_path,omitempty"`
}

type paramRep struct {
	Type        string   `json:"type"`
	Pattern     string   `json:"pattern,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Default     string   `json:"default,omitempty"`
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

	for _, d := range k.deps {
		rep.Dependencies = append(rep.Dependencies, dependencyRep{Id: d.Id, Kind: d.Kind, Target: d.Target, Critical: d.Critical})
	}
	for _, e := range k.endpoints {
		params := make(map[string]paramRep, len(e.Params))
		for name, p := range e.Params {
			params[name] = paramRep{Type: cmpOr(p.Type, "string"), Pattern: p.Pattern, Enum: p.Enum, Min: p.Min, Max: p.Max,
				Default: p.Default, Required: p.Required, Description: p.Description, Personal: p.Personal}
		}
		rep.Endpoints = append(rep.Endpoints, endpointRep{Id: e.Id, Title: e.Title, Description: e.Description, Path: e.Path,
			Params: params, TimeoutMs: e.Timeout.Milliseconds(), Response: e.response, RowsPath: e.RowsPath})
	}
	return rep
}
