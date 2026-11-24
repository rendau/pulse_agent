package dto

// FindingReq — внешняя находка об атаке (POST /v1/findings).
type FindingReq struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Text     string `json:"text"`
	Source   string `json:"source"`
}

// FindingRep — id уведомления (0 — не создано) и был ли это повтор об уже известном адресе.
type FindingRep struct {
	Id           int64 `json:"id"`
	Deduplicated bool  `json:"deduplicated"`
}
