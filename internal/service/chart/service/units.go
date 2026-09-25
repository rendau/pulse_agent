package service

import "strings"

// scale — во что перевести значения ряда, чтобы их было удобно читать: байты — в МБ/ГБ
// (десятичные, как их считает модель в тексте ответа: график и текст не расходятся),
// доли — в проценты, секунды меньше секунды — в мс, трафик меньше запроса в секунду —
// в запросы в минуту. max — наибольшее значение по модулю.
func scale(unit string, max float64) (factor float64, label string) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "bytes", "byte", "b":
		factor, label = 1, "байт"
		for _, u := range []string{"КБ", "МБ", "ГБ", "ТБ"} {
			if max/factor < 1000 {
				break
			}
			factor, label = factor*1000, u
		}
		return 1 / factor, label
	case "ratio":
		return 100, "%"
	case "percent", "%":
		return 1, "%"
	case "seconds", "second", "s":
		if max < 1 {
			return 1000, "мс"
		}
		return 1, "с"
	case "cores", "core":
		if max < 0.1 {
			return 100, "% ядра"
		}
		return 1, "ядра"
	case "rps":
		if max < 1 {
			return 60, "запросов в минуту"
		}
		return 1, "запросов в секунду"
	case "count", "":
		return 1, ""
	default:
		return 1, unit
	}
}
