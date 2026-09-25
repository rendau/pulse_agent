// Package evals — эталонные вопросы (cases.yaml) и эталонный прогон (baseline.json), вшитые в
// образ агента: прогон на стороне сервиса — /debug/eval, /v1/eval, команда /eval в Telegram.
// Правка вопросов или эталона — коммит; до деплоя новые вопросы гоняются с машины: make eval.
package evals

import _ "embed"

//go:embed cases.yaml
var Cases []byte

//go:embed baseline.json
var Baseline []byte
