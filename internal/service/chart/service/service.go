// Package service — графики на gonum/plot: линии для рядов во времени, горизонтальные
// столбцы для сравнения по категориям. PNG рисуется в памяти, шрифты (Liberation, с
// кириллицей) вшиты в библиотеку — от окружения ничего не нужно.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	// база часовых поясов вшита в бинарник: время на оси — по Алматы, не по TZ контейнера
	_ "time/tzdata"

	"github.com/samber/lo"
	"github.com/samber/lo/mutable"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/text"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"

	chartModel "github.com/rendau/pulse_agent/internal/service/chart/model"
)

const (
	// размер картинки: 1152×648 — чётко на телефоне и без лишнего веса
	width  = 8 * vg.Inch
	height = 4.5 * vg.Inch
	dpi    = 144
	margin = 12 * vg.Length(1)

	// maxSeries — больше линий на одном графике не различить; остальные отбрасываются
	// (сначала — с наименьшими значениями), в заголовке — сколько показано
	maxSeries = 8
	// maxBars — столбцов на графике
	maxBars = 25
	// markersUpTo — точки линии с маркерами, пока их мало (одиночная точка без маркера не видна)
	markersUpTo = 40
)

// Config — пояс времени на оси и тема (ThemeDark по умолчанию, ThemeLight).
type Config struct {
	Location *time.Location
	Theme    string
}

type Service struct {
	loc   *time.Location
	theme theme
}

func New(cfg Config) *Service {
	loc := cfg.Location
	if loc == nil {
		loc, _ = time.LoadLocation("Asia/Almaty")
	}
	t, ok := themes[cfg.Theme]
	if !ok {
		t = themes[ThemeDark]
	}
	return &Service{loc: loc, theme: t}
}

// ErrInvalidSpec — описание графика нарисовать нельзя (нет точек, неизвестный тип);
// текст ошибки — для модели, чтобы она поправила вызов.
var ErrInvalidSpec = errors.New("invalid chart")

func (s *Service) Render(spec *chartModel.Spec) ([]byte, error) {
	series := lo.Filter(spec.Series, func(r chartModel.Series, _ int) bool { return len(r.Points) > 0 })
	if len(series) == 0 {
		return nil, fmt.Errorf("%w: no points", ErrInvalidSpec)
	}

	title := strings.TrimSpace(spec.Title)
	if len(series) > maxSeries {
		sort.SliceStable(series, func(i, j int) bool { return peak(series[i]) > peak(series[j]) })
		title = strings.TrimSpace(fmt.Sprintf("%s (показаны %d рядов из %d)", title, maxSeries, len(series)))
		series = series[:maxSeries]
	}

	factor, unit := scale(spec.Unit, lo.Max(lo.Map(series, func(r chartModel.Series, _ int) float64 { return peak(r) })))

	p := plot.New()
	p.Title.Text = title
	p.Title.Padding = vg.Points(8)
	p.Legend.Top, p.Legend.Left = true, true
	// без засечек: на телефоне читается лучше
	styleText(&p.Title.TextStyle, 15)
	styleText(&p.Legend.TextStyle, 11)
	for _, axis := range []*plot.Axis{&p.X, &p.Y} {
		styleText(&axis.Tick.Label, 11)
		styleText(&axis.Label.TextStyle, 12)
	}
	s.theme.apply(p)

	h := height
	var err error
	switch spec.Type {
	case chartModel.TypeLine, "":
		err = s.line(p, series, factor, unit)
	case chartModel.TypeBar:
		h, err = s.bars(p, series, factor, unit)
	default:
		err = fmt.Errorf("%w: type %q; expected line or bar", ErrInvalidSpec, spec.Type)
	}
	if err != nil {
		return nil, err
	}

	canvas := vgimg.NewWith(vgimg.UseWH(width, h), vgimg.UseDPI(dpi), vgimg.UseBackgroundColor(s.theme.background))
	// поля по краям: иначе длинные подписи оси и легенда упираются в границу картинки
	p.Draw(draw.Crop(draw.New(canvas), margin, -margin, margin/2, -margin/2))

	var buf bytes.Buffer
	if _, err = (vgimg.PngCanvas{Canvas: canvas}).WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// line — ряды во времени: ось X — время по Алматы, формат подписей — по длине периода.
func (s *Service) line(p *plot.Plot, series []chartModel.Series, factor float64, unit string) error {
	var from, to time.Time
	for _, r := range series {
		for _, pt := range r.Points {
			if pt.Time.IsZero() {
				return fmt.Errorf("%w: line point of %q has no time", ErrInvalidSpec, r.Name)
			}
			if from.IsZero() || pt.Time.Before(from) {
				from = pt.Time
			}
			if pt.Time.After(to) {
				to = pt.Time
			}
		}
	}

	p.Add(s.gridLines(true, true))
	p.Y.Label.Text = unit
	p.X.Tick.Marker = timeTicker{loc: s.loc, format: timeFormat(to.Sub(from))}

	for i, r := range series {
		points := byTime(r.Points)
		xys := make(plotter.XYs, len(points))
		for j, pt := range points {
			xys[j] = plotter.XY{X: float64(pt.Time.UnixNano()) / 1e9, Y: pt.Value * factor}
		}

		style := s.theme.color(i)
		if len(xys) <= markersUpTo {
			l, sc, err := plotter.NewLinePoints(xys)
			if err != nil {
				return fmt.Errorf("line %q: %w", r.Name, err)
			}
			l.Color, l.Width = style, vg.Points(2)
			sc.Color, sc.Radius = style, vg.Points(2.5)
			p.Add(l, sc)
			addLegend(p, r.Name, l, len(series))
			continue
		}

		l, err := plotter.NewLine(xys)
		if err != nil {
			return fmt.Errorf("line %q: %w", r.Name, err)
		}
		l.Color, l.Width = style, vg.Points(2)
		p.Add(l)
		addLegend(p, r.Name, l, len(series))
	}

	fitValues(&p.Y, len(series) > 1)
	return nil
}

// bars — горизонтальные столбцы: длинные имена сервисов читаются на оси Y. Несколько рядов —
// группы столбцов по категории (первый ряд — верхний). Категории — в порядке первого
// появления, сверху вниз; у столбцов — значения. Высота картинки — по числу категорий:
// два столбца не растягиваются на весь экран.
func (s *Service) bars(p *plot.Plot, series []chartModel.Series, factor float64, unit string) (vg.Length, error) {
	labels := lo.Uniq(lo.FlatMap(series, func(r chartModel.Series, _ int) []string {
		return lo.Map(r.Points, func(pt chartModel.Point, _ int) string { return pt.Label })
	}))
	if lo.Contains(labels, "") {
		return 0, fmt.Errorf("%w: bar point has no label", ErrInvalidSpec)
	}
	if len(labels) > maxBars {
		labels = labels[:maxBars]
		p.Title.Text = strings.TrimSpace(fmt.Sprintf("%s (первые %d)", p.Title.Text, maxBars))
	}

	// gonum рисует категорию 0 внизу — переворачиваем, чтобы первая была сверху
	index := make(map[string]int, len(labels))
	for i, label := range labels {
		index[label] = len(labels) - 1 - i
	}

	p.Add(s.gridLines(true, false))
	p.X.Label.Text = unit
	if len(series) > 1 {
		p.Legend.Left = false
	}

	rowHeight := barHeight * vg.Length(1+0.6*float64(len(series)-1))
	h := min(max(vg.Length(len(labels))*rowHeight*1.6+vg.Inch*1.3, 3*vg.Inch), 10*vg.Inch)
	barWidth := rowHeight / vg.Length(len(series))
	withValues := len(labels)*len(series) <= 40

	for i, r := range series {
		values := make(plotter.Values, len(labels))
		for _, pt := range r.Points {
			if j, ok := index[pt.Label]; ok {
				values[j] = pt.Value * factor
			}
		}

		b, err := plotter.NewBarChart(values, barWidth)
		if err != nil {
			return 0, fmt.Errorf("bars %q: %w", r.Name, err)
		}
		b.Horizontal = true
		b.Color = s.theme.color(i)
		b.LineStyle.Width = 0
		b.Offset = rowHeight/2 - barWidth/2 - barWidth*vg.Length(i)
		p.Add(b)
		addLegend(p, r.Name, b, len(series))

		if withValues {
			xys := make(plotter.XYs, len(values))
			texts := make([]string, len(values))
			for j, v := range values {
				xys[j], texts[j] = plotter.XY{X: v, Y: float64(j)}, formatValue(v)
			}
			l, err := plotter.NewLabels(plotter.XYLabels{XYs: xys, Labels: texts})
			if err != nil {
				return 0, fmt.Errorf("bar labels %q: %w", r.Name, err)
			}
			for k := range l.TextStyle {
				styleText(&l.TextStyle[k], 10)
				l.TextStyle[k].Color = s.theme.text
				l.TextStyle[k].YAlign = -0.5
			}
			l.Offset = vg.Point{X: vg.Points(4), Y: b.Offset}
			p.Add(l)
		}
	}

	reversed := append([]string{}, labels...)
	mutable.Reverse(reversed)
	p.NominalY(reversed...)

	// запас справа под подписи значений и легенду
	if p.X.Min > 0 {
		p.X.Min = 0
	}
	p.X.Max += (p.X.Max - p.X.Min) * lo.Ternary(len(series) > 1, 0.35, 0.12)
	return h, nil
}

// barHeight — толщина одиночного столбца.
const barHeight = 18 * vg.Length(1)

// formatValue — подпись значения у столбца: без лишних знаков после запятой.
func formatValue(v float64) string {
	switch a := math.Abs(v); {
	case a >= 100 || a == math.Trunc(a):
		return strconv.FormatFloat(math.Round(v), 'f', -1, 64)
	case a >= 10:
		return strconv.FormatFloat(v, 'f', 1, 64)
	default:
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
}

// styleText — шрифт без засечек нужного размера.
func styleText(style *text.Style, size float64) {
	style.Font.Variant = "Sans"
	style.Font.Size = vg.Points(size)
}

// gridLines — сетка: vertical — линии по оси X, horizontal — по оси Y.
func (s *Service) gridLines(vertical, horizontal bool) *plotter.Grid {
	grid := plotter.NewGrid()
	grid.Vertical.Color = lo.Ternary[color.Color](vertical, s.theme.grid, color.Transparent)
	grid.Horizontal.Color = lo.Ternary[color.Color](horizontal, s.theme.grid, color.Transparent)
	return grid
}

// addLegend — легенда нужна, только когда рядов больше одного.
func addLegend(p *plot.Plot, name string, thumb plot.Thumbnailer, series int) {
	if series > 1 && name != "" {
		p.Legend.Add(name, thumb)
	}
}

// fitValues — ось значений линий. Ряды, которые опускаются ниже половины максимума (ошибки,
// трафик), — от нуля: иначе картинка преувеличит разницу. Ряды далеко от нуля (память
// 520–650 МБ) — по данным с полями: от нуля линия прижата к верху, а три четверти пусты.
// Сверху — запас под легенду.
func fitValues(axis *plot.Axis, legend bool) {
	low, high := axis.Min, axis.Max
	switch {
	case low <= 0 || low < high/2:
		axis.Min = math.Min(low, 0)
	default:
		pad := (high - low) * 0.15
		if pad == 0 {
			pad = high * 0.1
		}
		axis.Min, axis.Max = low-pad, high+pad
	}
	if legend {
		axis.Max += (axis.Max - axis.Min) * 0.25
	}
}

// byTime — точки ряда по времени.
func byTime(points []chartModel.Point) []chartModel.Point {
	sorted := append([]chartModel.Point{}, points...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })
	return sorted
}

func peak(r chartModel.Series) float64 {
	return lo.Max(lo.Map(r.Points, func(pt chartModel.Point, _ int) float64 { return math.Abs(pt.Value) }))
}

// tickSteps — шаги подписей оси времени: круглые, чтобы метки вставали на 10:00, 10:15…
var tickSteps = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 2 * 24 * time.Hour, 7 * 24 * time.Hour,
}

// maxTimeTicks — подписей на оси времени не больше этого.
const maxTimeTicks = 8

// timeTicker — метки оси времени в поясе ответов на круглых шагах от полуночи.
type timeTicker struct {
	loc    *time.Location
	format string
}

func (t timeTicker) Ticks(minX, maxX float64) []plot.Tick {
	from := time.Unix(0, int64(minX*1e9)).In(t.loc)
	to := time.Unix(0, int64(maxX*1e9)).In(t.loc)

	step := tickSteps[len(tickSteps)-1]
	for _, s := range tickSteps {
		if to.Sub(from)/s < maxTimeTicks {
			step = s
			break
		}
	}

	midnight := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, t.loc)
	first := midnight.Add((from.Sub(midnight) + step - 1) / step * step)

	ticks := make([]plot.Tick, 0, maxTimeTicks)
	for at := first; !at.After(to); at = at.Add(step) {
		ticks = append(ticks, plot.Tick{Value: float64(at.UnixNano()) / 1e9, Label: at.Format(t.format)})
	}
	return ticks
}

// timeFormat — подписи оси времени: часы, если период в пределах полутора суток, иначе дата.
func timeFormat(span time.Duration) string {
	switch {
	case span <= 36*time.Hour:
		return "15:04"
	case span <= 10*24*time.Hour:
		return "02.01 15:04"
	default:
		return "02.01"
	}
}
