package service

import (
	"image/color"

	"gonum.org/v1/plot"
)

// темы графиков
const (
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// theme — цвета графика. Тему пользователя клиенты (Telegram и др.) не сообщают, поэтому она
// одна на сервис (CHART_THEME); тёмная по умолчанию.
type theme struct {
	background color.Color
	text       color.Color
	axis       color.Color
	grid       color.Color
	palette    []color.Color
}

func rgb(v uint32) color.Color {
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

var themes = map[string]theme{
	// фон — как у тёмной темы Telegram, цвета рядов — посветлее, чтобы читались на тёмном
	ThemeDark: {
		background: rgb(0x1e2227),
		text:       rgb(0xe3e6ea),
		axis:       rgb(0x8a9199),
		grid:       rgb(0x343a41),
		palette: []color.Color{
			rgb(0x6aa9ff), rgb(0xffa94d), rgb(0xff6b6b), rgb(0x69db7c),
			rgb(0x63e6be), rgb(0xffd43b), rgb(0xda77f2), rgb(0xd8a47f),
		},
	},
	// tableau10
	ThemeLight: {
		background: color.White,
		text:       color.Black,
		axis:       color.Black,
		grid:       color.Gray{Y: 0xe0},
		palette: []color.Color{
			rgb(0x4e79a7), rgb(0xf28e2b), rgb(0xe15759), rgb(0x59a14f),
			rgb(0x76b7b2), rgb(0xedc948), rgb(0xb07aa1), rgb(0x9c755f),
		},
	},
}

func (t theme) color(i int) color.Color {
	return t.palette[i%len(t.palette)]
}

// apply раскрашивает подписи, оси и фон графика.
func (t theme) apply(p *plot.Plot) {
	p.BackgroundColor = t.background
	p.Title.TextStyle.Color = t.text
	p.Legend.TextStyle.Color = t.text
	for _, axis := range []*plot.Axis{&p.X, &p.Y} {
		axis.Color = t.axis
		axis.Tick.Color = t.axis
		axis.Tick.Label.Color = t.text
		axis.Label.TextStyle.Color = t.text
	}
}
