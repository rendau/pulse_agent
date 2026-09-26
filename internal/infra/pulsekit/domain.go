package pulsekit

import (
	"fmt"
	"regexp"
	"time"
)

// лимиты раздела domain: он уходит модели при каждом get_service_info — коротко
const (
	maxResponsibilities = 5
	maxBoundaries       = 10
	maxEntities         = 10
	maxStatuses         = 20
	maxQuestions        = 10
	maxDomainChars      = 200
)

// Domain — бизнес-смысл сервиса для агента (стандарт, раздел domain): за что отвечает, чем не
// занимается, с какими объектами работает и какие вопросы к нему типичны. Заполняет агент,
// внедряющий стандарт, по коду; разработчик подтверждает.
type Domain struct {
	// Responsibilities — за что сервис отвечает (≤ 5 пунктов по 200 символов)
	Responsibilities []string
	// NotResponsible — чем не занимается и кто занимается: агент ищет причину там
	NotResponsible []Boundary
	// Entities — бизнес-объекты: формат номера, статусы, когда объект застрял
	Entities []Entity
	// Questions — типичные вопросы и куда за ответом
	Questions []Question
}

// Boundary — чем сервис не занимается; Service — имя сервиса в каталоге pulse, если известно.
type Boundary struct {
	What    string
	Service string
}

// Entity — бизнес-объект. IdPattern — формат номера (RE2 на всё значение, «[0-9]{7}»): по нему
// агент и pulse узнают объект в вопросе и в логах; IdExample должен под него подходить.
type Entity struct {
	Name        string
	IdPattern   string
	IdExample   string
	Description string
	Statuses    []EntityStatus
}

// EntityStatus — статус объекта: что значит и через сколько объект в нём считается застрявшим
// (0 — не застревает или не задано).
type EntityStatus struct {
	Name       string
	Meaning    string
	StuckAfter time.Duration
}

// Question — типичный вопрос к сервису и куда за ответом: Endpoint — id диагностической ручки,
// How — словами («логи сервиса по номеру доставки»).
type Question struct {
	Question string
	How      string
	Endpoint string
}

// validateDomain — формат номера и пример проверяются (не так — шаблон не публикуется); длинные
// тексты и лишние пункты — предупреждения (pulse обрежет).
func (k *Kit) validateDomain(d *Domain) *Domain {
	if d == nil {
		return nil
	}
	count := func(what string, n, max int) {
		if n > max {
			k.warn(fmt.Sprintf("domain %s: %d, the standard allows %d — pulse will drop the rest", what, n, max))
		}
	}
	count("responsibilities", len(d.Responsibilities), maxResponsibilities)
	count("not_responsible", len(d.NotResponsible), maxBoundaries)
	count("entities", len(d.Entities), maxEntities)
	count("questions", len(d.Questions), maxQuestions)
	for _, r := range d.Responsibilities {
		k.checkText("domain responsibility", r, maxDomainChars)
	}
	result := *d
	result.Entities = make([]Entity, 0, len(d.Entities))
	for _, e := range d.Entities {
		if e.Name == "" {
			k.problem("domain entity without Name — not published")
			continue
		}
		k.checkText("domain entity "+e.Name+" description", e.Description, maxDomainChars)
		count("entity "+e.Name+" statuses", len(e.Statuses), maxStatuses)
		if e.IdPattern != "" {
			re, err := regexp.Compile(`^(?:` + e.IdPattern + `)$`)
			switch {
			case err != nil:
				k.problem(fmt.Sprintf("domain entity %s: IdPattern is not an RE2 regexp: %s — pattern not published", e.Name, err))
				e.IdPattern = ""
			case e.IdExample != "" && !re.MatchString(e.IdExample):
				k.problem(fmt.Sprintf("domain entity %s: IdExample %q does not match IdPattern — pattern not published", e.Name, e.IdExample))
				e.IdPattern = ""
			}
		}
		result.Entities = append(result.Entities, e)
	}
	return &result
}

// domainProblems — вопросы со ссылкой на необъявленную ручку (ручки объявляются после New).
func (k *Kit) domainProblems() []string {
	if k.service.Domain == nil {
		return nil
	}
	var result []string
	for _, q := range k.service.Domain.Questions {
		if q.Endpoint == "" {
			continue
		}
		declared := false
		for _, e := range k.endpoints {
			declared = declared || e.Id == q.Endpoint
		}
		if !declared {
			result = append(result, fmt.Sprintf("domain question %q: endpoint %s is not declared — pulse will drop the link", q.Question, q.Endpoint))
		}
	}
	return result
}
