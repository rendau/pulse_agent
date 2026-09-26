package chart

import chartModel "github.com/rendau/pulse_agent/internal/service/chart/model"

// Chart — графики к ответам: описание графика → PNG для Telegram. Про источник данных
// (pulse, модель) не знает.
type Chart interface {
	Render(spec *chartModel.Spec) ([]byte, error)
}
