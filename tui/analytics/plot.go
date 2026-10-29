package analytics

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// AnalyticsBand is one signed band of a stacked chart. Amounts are nonnegative
// inside a band; the negative band grows down from the baseline so refunds stay
// visible instead of being folded into the positive stack
// (Rust analytics/plot/painting.rs Band, #45763).
type AnalyticsBand int

const (
	// AnalyticsBandPositive stacks usage above the baseline.
	AnalyticsBandPositive AnalyticsBand = iota
	// AnalyticsBandNegative stacks refunds below the baseline.
	AnalyticsBandNegative
)

// Amount is the nonnegative magnitude this band plots for a signed value.
func (b AnalyticsBand) Amount(value float64) float64 {
	amount := value
	if b == AnalyticsBandNegative {
		amount = -value
	}
	return math.Max(amount, 0)
}

func (b AnalyticsBand) rowY(baseline, row int) int {
	if b == AnalyticsBandNegative {
		return baseline + row + 1
	}
	return baseline - row - 1
}

// partialGlyph reverses the complementary lower block for a top-anchored
// partial refund cell.
func (b AnalyticsBand) partialGlyph(level int) rune {
	glyphs := []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	index := level
	if b == AnalyticsBandNegative && level < 8 {
		index = 8 - level
	}
	if index < 0 {
		index = 0
	}
	if index >= len(glyphs) {
		index = len(glyphs) - 1
	}
	return glyphs[index]
}

// AnalyticsSeriesColorName names the terminal palette color used by one of the
// four stacked series (Rust analytics/styles.rs series_colors).
func AnalyticsSeriesColorName(series int) string {
	switch series {
	case 0:
		return "cyan"
	case 1:
		return "magenta"
	case 2:
		return "green"
	default:
		return "default"
	}
}

// AnalyticsCell is one rendered character with the style facts the view needs to
// paint it. Series is -1 when the cell is not part of a bar. The selected-value
// callout sets Bold; views render it in white on dark backgrounds and in the
// terminal foreground on light backgrounds, matching Rust's styles.rs.
type AnalyticsCell struct {
	Rune      rune
	Series    int
	Dim       bool
	Accent    bool
	Bold      bool
	Reversed  bool
	Underline bool
}

func analyticsSpace() AnalyticsCell {
	return AnalyticsCell{Rune: ' ', Series: -1}
}

type analyticsGrid struct {
	width int
	cells [][]AnalyticsCell
}

func newAnalyticsGrid(width, height int) *analyticsGrid {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	rows := make([][]AnalyticsCell, height)
	for index := range rows {
		row := make([]AnalyticsCell, width)
		for position := range row {
			row[position] = analyticsSpace()
		}
		rows[index] = row
	}
	return &analyticsGrid{width: width, cells: rows}
}

func (g *analyticsGrid) at(x, y int) AnalyticsCell {
	if g == nil || y < 0 || y >= len(g.cells) || x < 0 || x >= g.width {
		return analyticsSpace()
	}
	return g.cells[y][x]
}

func (g *analyticsGrid) set(x, y int, cell AnalyticsCell) {
	if g == nil || y < 0 || y >= len(g.cells) || x < 0 || x >= g.width {
		return
	}
	g.cells[y][x] = cell
}

// writeText paints label starting at x without exceeding limit display columns,
// leaving cells outside the grid untouched (Rust Buffer::set_line).
func (g *analyticsGrid) writeText(x, y int, label string, limit int, cell AnalyticsCell) {
	if limit < 0 {
		return
	}
	used := 0
	for _, char := range label {
		charWidth := runewidth.RuneWidth(char)
		if used+charWidth > limit {
			return
		}
		style := cell
		style.Rune = char
		g.set(x+used, y, style)
		used += charWidth
	}
}

// AnalyticsPlotDay is one calendar slot. A missing day has a nil Day but still
// occupies its date so gaps render differently from zero usage.
type AnalyticsPlotDay struct {
	Date time.Time
	Day  *AccountAnalyticsDay
}

// AnalyticsChart is rendered stacked-bar geometry, top row first.
type AnalyticsChart struct {
	Lines [][]AnalyticsCell
	Bands int
}

// AnalyticsChartText renders the chart without color or modifiers, trimming
// trailing spaces from each row.
func AnalyticsChartText(chart AnalyticsChart) string {
	lines := make([]string, 0, len(chart.Lines))
	for _, row := range chart.Lines {
		var builder strings.Builder
		for _, cell := range row {
			builder.WriteRune(cell.Rune)
		}
		lines = append(lines, strings.TrimRight(builder.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// AnalyticsPeak is the chart's scale shared with the selected-day summary: the
// larger of the summed absolute parts and the authoritative daily total
// (Rust analytics/plot.rs peak).
func AnalyticsPeak(days []AnalyticsPlotDay, categories []AccountAnalyticsValue) float64 {
	peak := 0.0
	for _, entry := range days {
		if entry.Day == nil {
			continue
		}
		parts := AnalyticsParts(entry.Day.Values, categories)
		sum := 0.0
		for _, value := range parts {
			sum += math.Abs(value)
		}
		sum = math.Max(sum, math.Abs(entry.Day.Total))
		peak = math.Max(peak, sum)
	}
	return peak
}

// AnalyticsAxisMax rounds a peak up to two readable intervals; counts and
// tokens never use fractional ticks (Rust analytics/plot.rs axis_max).
func AnalyticsAxisMax(peak float64, unit AccountAnalyticsUnit) float64 {
	integral := unit == AccountAnalyticsUnitCount || unit == AccountAnalyticsUnitTokens
	if peak == 0 {
		if integral {
			return 2
		}
		return 1
	}
	step := peak / 2
	if integral {
		step = math.Max(step, 1)
	}
	magnitude := math.Pow(10, math.Floor(math.Log10(step)))
	if magnitude == 0 || math.IsInf(magnitude, 0) || math.IsNaN(magnitude) {
		return peak
	}
	normalized := step / magnitude
	multiple := 10.0
	for _, candidate := range []float64{1, 1.5, 2, 2.5, 3, 4, 5, 7.5, 10} {
		if candidate < normalized {
			continue
		}
		if integral && math.Trunc(candidate*magnitude) != candidate*magnitude {
			continue
		}
		multiple = candidate
		break
	}
	ceiling := multiple * magnitude * 2
	if !math.IsInf(ceiling, 0) && !math.IsNaN(ceiling) && ceiling >= peak {
		return ceiling
	}
	return peak
}

// analyticsPlotLayout is the calendar viewport shared by bars, axes, and
// annotations (Rust analytics/plot/layout.rs Layout).
type analyticsPlotLayout struct {
	width     int
	height    int
	axisWidth int
	plotWidth int
	count     int
	start     int
	baseline  int
	markerRow int
}

func newAnalyticsPlotLayout(dayCount, cursor, width, height, labelWidth, bands int) analyticsPlotLayout {
	axisWidth := 0
	if width >= 30 && labelWidth <= width/3 {
		axisWidth = labelWidth
	}
	plotWidth := width - axisWidth
	count := dayCount
	if plotWidth < count {
		count = plotWidth
	}
	if count < 1 {
		count = 1
	}
	start := cursor - (count - 1)
	if start < 0 {
		start = 0
	}
	if furthest := dayCount - count; start > furthest {
		start = furthest
	}
	if start < 0 {
		start = 0
	}
	baseline := height + 1
	extra := 1
	if bands == 2 {
		extra = height + 2
	}
	return analyticsPlotLayout{
		width:     width,
		height:    height,
		axisWidth: axisWidth,
		plotWidth: plotWidth,
		count:     count,
		start:     start,
		baseline:  baseline,
		markerRow: baseline + extra,
	}
}

func (l analyticsPlotLayout) center(index int) int {
	return l.axisWidth + (index*l.plotWidth/l.count+(index+1)*l.plotWidth/l.count-1)/2
}

// AnalyticsPlot renders daily stacked bars for a calendar window. The cursor
// must index days; out-of-range cursors are clamped so a resized view cannot
// panic (Rust analytics/plot.rs chart, #45763).
func AnalyticsPlot(
	days []AnalyticsPlotDay,
	categories []AccountAnalyticsValue,
	cursor int,
	width int,
	height int,
	unit AccountAnalyticsUnit,
) AnalyticsChart {
	if len(days) == 0 || width <= 0 {
		return AnalyticsChart{}
	}
	if height < 0 {
		height = 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(days) {
		cursor = len(days) - 1
	}
	values := make([][4]float64, len(days))
	negative := false
	for index, entry := range days {
		if entry.Day == nil {
			continue
		}
		values[index] = AnalyticsParts(entry.Day.Values, categories)
		for _, value := range values[index] {
			if value < 0 {
				negative = true
			}
		}
	}
	bands := []AnalyticsBand{AnalyticsBandPositive}
	if negative {
		bands = []AnalyticsBand{AnalyticsBandPositive, AnalyticsBandNegative}
	}
	peak := AnalyticsAxisMax(AnalyticsPeak(days, categories), unit)
	if peak <= 0 {
		peak = 1
	}
	labelWidth := analyticsMaxInt(
		runewidth.StringWidth(analyticsTickLabel(peak, unit)),
		runewidth.StringWidth(analyticsTickLabel(peak/2, unit)),
	) + len(bands) - 1 + 2
	layout := newAnalyticsPlotLayout(len(days), cursor, width, height, labelWidth, len(bands))
	renderer := analyticsPlotRenderer{
		days:   days,
		values: values,
		cursor: cursor,
		peak:   peak,
		unit:   unit,
		bands:  bands,
		layout: layout,
	}
	grid := newAnalyticsGrid(width, layout.markerRow+2)
	renderer.paintBars(grid)
	renderer.annotateMissing(grid)
	renderer.paintAxis(grid)
	renderer.annotateSelection(grid)
	renderer.annotateCalendar(grid)
	lines := make([][]AnalyticsCell, 0, len(grid.cells))
	for _, row := range grid.cells {
		line := make([]AnalyticsCell, len(row))
		copy(line, row)
		lines = append(lines, line)
	}
	return AnalyticsChart{Lines: lines, Bands: len(bands)}
}

type analyticsPlotRenderer struct {
	days   []AnalyticsPlotDay
	values [][4]float64
	cursor int
	peak   float64
	unit   AccountAnalyticsUnit
	bands  []AnalyticsBand
	layout analyticsPlotLayout
}

// analyticsAmount formats one plotted amount: counts and tokens round to whole
// numbers, credits keep their two-decimal form (Rust analytics/plot.rs amount).
func analyticsPlotAmount(value float64, unit AccountAnalyticsUnit) string {
	switch unit {
	case AccountAnalyticsUnitCount, AccountAnalyticsUnitTokens:
		// Rust #45769 compacts large chart values to one decimal.
		return analyticsCompactAmount(math.Round(value))
	case AccountAnalyticsUnitCredits:
		return analyticsCreditAmount(value)
	default:
		return analyticsAmount(value)
	}
}

// analyticsTickLabel formats an axis tick with the shared compact form for
// every unit (Rust analytics/plot.rs tick, #45769).
func analyticsTickLabel(value float64, unit AccountAnalyticsUnit) string {
	return analyticsCompactAmount(value)
}

func analyticsMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func (r *analyticsPlotRenderer) paintBars(grid *analyticsGrid) {
	layout := r.layout
	for index := 0; index < layout.count; index++ {
		dayIndex := layout.start + index
		left := layout.axisWidth + index*layout.plotWidth/layout.count
		step := (index+1)*layout.plotWidth/layout.count - index*layout.plotWidth/layout.count
		barWidth := step * 2 / 3
		if barWidth < 1 {
			barWidth = 1
		}
		if barWidth > 12 {
			barWidth = 12
		}
		x := left + (step-barWidth)/2
		parts := r.values[dayIndex]
		for _, band := range r.bands {
			plotted := 0.0
			for _, value := range parts {
				plotted += band.Amount(value)
			}
			scaled := int(plotted * float64(layout.height*8) / r.peak)
			if scaled < 1 && plotted > 0 {
				scaled = 1
			}
			for row := 0; row < layout.height; row++ {
				level := scaled - row*8
				if level < 0 {
					level = 0
				}
				if level > 8 {
					level = 8
				}
				if level == 0 {
					continue
				}
				reverse := band == AnalyticsBandNegative && level < 8
				glyph := band.partialGlyph(level)
				bottom := float64(row*8) * r.peak / float64(layout.height*8)
				top := math.Min(float64(row*8+level)*r.peak/float64(layout.height*8), plotted)
				sample := bottom + (top-bottom)/2
				cumulative := 0.0
				series := 3
				for position, value := range parts {
					cumulative += band.Amount(value)
					if sample < cumulative {
						series = position
						break
					}
				}
				y := band.rowY(layout.baseline, row)
				for column := x; column < x+barWidth; column++ {
					grid.set(column, y, AnalyticsCell{
						Rune:     glyph,
						Series:   series,
						Reversed: reverse,
					})
				}
			}
		}
	}
}

func (r *analyticsPlotRenderer) paintAxis(grid *analyticsGrid) {
	layout := r.layout
	for x := layout.axisWidth; x < layout.width; x++ {
		grid.set(x, layout.baseline, AnalyticsCell{Rune: '─', Series: -1, Dim: true})
	}
	if layout.axisWidth == 0 {
		return
	}
	type tick struct {
		y     int
		value float64
	}
	ticks := []tick{{y: 1, value: r.peak}, {y: layout.baseline, value: 0}}
	if layout.height >= 4 {
		ticks = append(ticks, tick{y: 1 + layout.height/2, value: r.peak / 2})
	}
	if len(r.bands) == 2 {
		ticks = append(ticks, tick{y: layout.baseline + layout.height, value: -r.peak})
	}
	for _, entry := range ticks {
		label := analyticsTickLabel(entry.value, r.unit)
		x := layout.axisWidth - (len(label) + 1)
		if x < 0 {
			x = 0
		}
		grid.writeText(x, entry.y, label, layout.axisWidth, AnalyticsCell{Dim: true})
	}
}

// annotateMissing marks days without a report with an em dash and days that
// reported zero usage with a zero. The selected slot gets its own callout below
// (Rust analytics/plot/annotations.rs annotate_missing).
func (r *analyticsPlotRenderer) annotateMissing(grid *analyticsGrid) {
	layout := r.layout
	for index := 0; index < layout.count; index++ {
		dayIndex := layout.start + index
		if dayIndex == r.cursor {
			continue
		}
		var marker rune
		switch entry := r.days[dayIndex]; {
		case entry.Day == nil:
			marker = '—'
		case entry.Day.Total == 0 && analyticsAllZero(r.values[dayIndex]):
			marker = '0'
		}
		if marker == 0 {
			continue
		}
		grid.set(layout.center(index), layout.height, AnalyticsCell{Rune: marker, Series: -1, Dim: true})
	}
}

func analyticsAllZero(parts [4]float64) bool {
	for _, value := range parts {
		if value != 0 {
			return false
		}
	}
	return true
}

// annotateSelection paints the authoritative daily total beside the cursor and,
// for short ranges, beside every non-empty bar. Labels that cannot fit are
// omitted instead of truncated, and a guide connects the callout to the bar
// (Rust analytics/plot/annotations.rs annotate_selection).
func (r *analyticsPlotRenderer) annotateSelection(grid *analyticsGrid) {
	layout := r.layout
	for _, dayIndex := range r.selectionLabels() {
		total := 0.0
		label := "—"
		if entry := r.days[dayIndex]; entry.Day != nil {
			total = entry.Day.Total
			label = analyticsPlotAmount(total, r.unit)
		}
		if runewidth.StringWidth(label) > layout.plotWidth {
			continue
		}
		x := layout.center(dayIndex - layout.start)
		labelWidth := runewidth.StringWidth(label)
		labelX := x - labelWidth/2
		if labelX > layout.width-labelWidth {
			labelX = layout.width - labelWidth
		}
		if labelX < layout.axisWidth {
			labelX = layout.axisWidth
		}
		occupiedStart := labelX - 1
		if occupiedStart < layout.axisWidth {
			occupiedStart = layout.axisWidth
		}
		occupiedEnd := labelX + labelWidth + 1
		if occupiedEnd > layout.width {
			occupiedEnd = layout.width
		}
		below := total < 0
		band := AnalyticsBandPositive
		if below {
			band = AnalyticsBandNegative
		}
		extent := 0.0
		for _, value := range r.values[dayIndex] {
			extent += band.Amount(value)
		}
		rows := layout.height
		if scaled := int(math.Ceil(extent * float64(layout.height) / r.peak)); scaled < rows {
			rows = scaled
		}
		if rows < 0 {
			rows = 0
		}
		boundary := layout.baseline - rows
		labelY := boundary - 1
		if below {
			boundary = layout.baseline + rows
			labelY = boundary + 1
		}
		if labelY < 0 {
			labelY = 0
		}
		// Keep the callout close without painting over a neighboring bar.
		for analyticsRowOccupied(grid, occupiedStart, occupiedEnd, labelY) {
			if below && labelY+1 < layout.markerRow {
				labelY++
			} else if !below && labelY > 0 {
				labelY--
			} else {
				break
			}
		}
		if analyticsRowOccupied(grid, occupiedStart, occupiedEnd, labelY) {
			continue
		}
		style := AnalyticsCell{Rune: ' ', Series: -1, Dim: true}
		if dayIndex == r.cursor {
			style = AnalyticsCell{Rune: ' ', Series: -1, Bold: true}
		}
		guideStart := labelY + 1
		guideEnd := boundary
		if below {
			guideStart = boundary + 1
			guideEnd = labelY
		}
		for y := guideStart; y < guideEnd; y++ {
			if grid.at(x, y).Rune == ' ' {
				grid.set(x, y, AnalyticsCell{Rune: '┊', Series: -1, Dim: style.Dim, Bold: style.Bold})
			}
		}
		grid.writeText(labelX, labelY, label, labelWidth, style)
	}
	x := layout.center(r.cursor - layout.start)
	grid.set(x, layout.markerRow, AnalyticsCell{Rune: '▲', Series: -1, Accent: true})
}

func analyticsRowOccupied(grid *analyticsGrid, start, end, y int) bool {
	for column := start; column < end; column++ {
		if grid.at(column, y).Rune != ' ' {
			return true
		}
	}
	return false
}

// selectionLabels gives the cursor first choice of space, then adds labels for
// the other visible bars when the range is short enough to read them all
// (Rust analytics/plot/annotations.rs selection order).
func (r *analyticsPlotRenderer) selectionLabels() []int {
	labels := []int{r.cursor}
	short := len(r.days) <= 7
	for index := 0; index < r.layout.count; index++ {
		dayIndex := r.layout.start + index
		if dayIndex == r.cursor || !short {
			continue
		}
		entry := r.days[dayIndex]
		if entry.Day == nil {
			continue
		}
		if entry.Day.Total != 0 || !analyticsAllZero(r.values[dayIndex]) {
			labels = append(labels, dayIndex)
		}
	}
	return labels
}

// annotateCalendar paints stable month/day ticks under the bars. Endpoint and
// weekly ticks get priority over the current selection, and a label is replaced
// by the day-only form or dropped rather than truncated
// (Rust analytics/plot/annotations.rs annotate_calendar).
func (r *analyticsPlotRenderer) annotateCalendar(grid *analyticsGrid) {
	layout := r.layout
	occupied := make([]bool, layout.width)
	order := make([]int, 0, layout.count)
	order = append(order, 0)
	for index := layout.count - 1; index >= 1; index-- {
		order = append(order, index)
	}
	for _, index := range order {
		dayIndex := layout.start + index
		if len(r.days) > 7 && dayIndex%7 != 0 && index+1 != layout.count {
			continue
		}
		date := r.days[dayIndex].Date
		label := date.Format("Jan 2")
		if len(r.days) <= 7 && layout.plotWidth/layout.count < 7 {
			label = strconv.Itoa(date.Day())
		}
		if runewidth.StringWidth(label) > layout.plotWidth {
			label = strconv.Itoa(date.Day())
		}
		if runewidth.StringWidth(label) > layout.plotWidth {
			continue
		}
		offset := layout.center(index) - len(label)/2
		if furthest := layout.width - len(label); offset > furthest {
			offset = furthest
		}
		if offset < layout.axisWidth {
			offset = layout.axisWidth
		}
		start := offset - 1
		if start < 0 {
			start = 0
		}
		end := offset + len(label) + 1
		if end > layout.width {
			end = layout.width
		}
		used := false
		for column := start; column < end; column++ {
			if occupied[column] {
				used = true
				break
			}
		}
		if used {
			continue
		}
		for column := offset; column < offset+len(label) && column < layout.width; column++ {
			occupied[column] = true
		}
		style := AnalyticsCell{Rune: ' ', Series: -1, Dim: true}
		if dayIndex == r.cursor {
			style = AnalyticsCell{Rune: ' ', Series: -1, Accent: true, Underline: true}
		}
		grid.writeText(offset, layout.markerRow+1, label, layout.width-offset, style)
	}
}
