// Package welcome renders the deterministic Codex/OpenAI welcome blossom.
//
// Rust parity: codex-rs/tui/src/empty_state_animation/{geometry,lighting,
// renderer,paths}.rs. The renderer projects two signed-distance fields onto a
// Braille cell grid and applies fixed studio lighting.
package welcome

import (
	"math"
	"strconv"
	"strings"
	"sync"
)

const (
	MaxColumns = 60
	MaxRows    = 24
	grid       = 160
	extent     = 1.08
	step       = extent * 2 / (grid - 1)
)

var dotBits = [8]uint8{1, 8, 2, 16, 4, 32, 64, 128}

const (
	halfDepth  = 0.1642880353482906
	wobble     = 0.11242630996974184
	grainPhase = 3.8990964158506114
)

// Cell is one terminal cell of the Braille projection.
type Cell struct {
	Dots uint8
	R    uint8
	G    uint8
	B    uint8
}

// Lighting is the fixed studio light rig adapted to terminal colors.
type Lighting struct {
	Background [3]float64
	Key        [3]float64
	Shadow     [3]float64
	Fill       [3]float64
	Rim        [3]float64
	Highlight  [3]float64
}

// DarkLighting matches Rust's dark-terminal rig.
func DarkLighting() Lighting {
	return Lighting{
		Background: [3]float64{16, 16, 16},
		Key:        [3]float64{210, 221, 235},
		Shadow:     [3]float64{52, 71, 105},
		Fill:       [3]float64{76, 118, 159},
		Rim:        [3]float64{91, 196, 216},
		Highlight:  [3]float64{255, 244, 218},
	}
}

// TerminalLighting mirrors Lighting::terminal for a known fg/bg pair.
func TerminalLighting(fg [3]uint8, bg [3]uint8) Lighting {
	background := [3]float64{float64(bg[0]), float64(bg[1]), float64(bg[2])}
	luma := 0.2126*float64(bg[0]) + 0.7152*float64(bg[1]) + 0.0722*float64(bg[2])
	if luma >= 128 {
		key := [3]float64{float64(fg[0]), float64(fg[1]), float64(fg[2])}
		shadow := [3]float64{
			background[0]*0.65 + key[0]*0.35,
			background[1]*0.65 + key[1]*0.35,
			background[2]*0.65 + key[2]*0.35,
		}
		return Lighting{Background: background, Key: key, Shadow: shadow, Fill: key, Rim: key, Highlight: key}
	}
	dark := DarkLighting()
	dark.Background = background
	return dark
}

// Shade applies the Rust studio lighting model.
func (l Lighting) Shade(normal [3]float64, depth float64, grain float64) [3]uint8 {
	nx, ny, nz := normal[0], normal[1], normal[2]
	diffuse := clamp(nx*-0.410+ny*-0.564+nz*0.718+grain, 0, 1)
	bounce := clamp(nx*0.55+ny*0.2-nz*0.35, 0, 1) * (1 - diffuse) * 0.38
	edge := math.Pow(1-math.Abs(clamp(nz, -1, 1)), 2.4) *
		clamp(nx*0.85-ny*0.38, 0, 1) * 0.82
	gloss := math.Pow(clamp(nx*-0.220+ny*-0.302+nz*0.928, 0, 1), 18) * 0.96
	base := (1 - bounce) * (1 - edge) * (1 - gloss)
	gain := clamp(0.90+depth*0.18, 0.68, 1)
	var rgb [3]uint8
	for i := 0; i < 3; i++ {
		value := l.Shadow[i]*(1-diffuse)*base +
			l.Key[i]*diffuse*base +
			l.Fill[i]*bounce*(1-edge)*(1-gloss) +
			l.Rim[i]*edge*(1-gloss) +
			l.Highlight[i]*gloss
		rgb[i] = uint8(math.Round(l.Background[i] + (value-l.Background[i])*gain))
	}
	return rgb
}

// Renderer caches the signed-distance fields and the last projected frame.
type Renderer struct {
	once sync.Once

	shape  []float64
	depth  []float32
	colors []uint32
	cells  []Cell

	cacheKey   string
	cacheFrame []Cell
}

// NewRenderer builds an empty renderer; field generation is lazy.
func NewRenderer() *Renderer {
	return &Renderer{}
}

// Frame returns columns*rows cells for phase in [0,1). The result is reused by
// the caller until the next Frame call.
func (r *Renderer) Frame(columns, rows int, phase float64, light Lighting) []Cell {
	if columns <= 0 || rows <= 0 {
		return nil
	}
	columns = min(columns, MaxColumns)
	rows = min(rows, MaxRows)
	phase = math.Mod(phase, 1)
	if phase < 0 {
		phase++
	}
	key := strconv.Itoa(columns) + "x" + strconv.Itoa(rows) + ":" + strconv.FormatFloat(math.Round(phase*120)/120, 'f', 5, 64)
	if key == r.cacheKey {
		return r.cacheFrame
	}
	r.once.Do(r.init)

	second := 0.0
	if phase >= 0.5 {
		second = 1
	}
	spin := smooth((phase*2 - second) / 0.82)
	morph := smooth((spin - 2.0/3.0) * 3)
	rotation := (second + spin) * 2 * math.Pi
	blend := morph
	if second == 1 {
		blend = 1 - morph
	}
	sx, cx := math.Sincos(math.Sin(rotation) * wobble)
	sy, cy := math.Sincos(rotation)
	sz, cz := math.Sincos(math.Sin(phase*2*math.Pi) * 0.045)
	bevel := 0.075 + blend*0.012
	dotColumns := columns * 2
	dotRows := rows * 4
	dotWidth := 800 / float64(dotColumns)
	dotHeight := 550 / float64(dotRows)
	for i := range r.depth {
		r.depth[i] = float32(math.Inf(-1))
	}
	for i := range r.shape {
		r.shape[i] = fields[0][i]*(1-blend) + fields[1][i]*blend
	}
	project := func(point [3]float64, normal [3]float64) {
		x, y, z := point[0], point[1], point[2]
		nx, ny, nz := normal[0], normal[1], normal[2]
		u := x*2.2 + y*0.7
		v := y*2.8 - x*0.4
		z += math.Sin(u)*0.075 + math.Sin(v)*0.04
		nx -= nz * (math.Cos(u)*0.165 - math.Cos(v)*0.016)
		ny -= nz * (math.Cos(u)*0.0525 + math.Cos(v)*0.112)
		length := math.Sqrt(nx*nx + ny*ny + nz*nz)
		if length == 0 {
			length = 1
		}
		nx /= length
		ny /= length
		nz /= length
		x1 := x*cy + z*sy
		z1 := -x*sy + z*cy
		y2 := y*cx - z1*sx
		z2 := y*sx + z1*cx
		x3 := x1*cz - y2*sz
		y3 := x1*sz + y2*cz
		perspective := 4.4 / (4.4 - z2)
		column := int(math.Floor((400 + x3*206*perspective) / dotWidth))
		row := int(math.Floor((275 + y3*206*perspective) / dotHeight))
		if column < 0 || column >= dotColumns || row < 0 || row >= dotRows {
			return
		}
		index := row*dotColumns + column
		if z2 <= float64(r.depth[index]) {
			return
		}
		nx1 := nx*cy + nz*sy
		nz1 := -nx*sy + nz*cy
		ny2 := ny*cx - nz1*sx
		nz2 := ny*sx + nz1*cx
		r.depth[index] = float32(z2)
		shaded := light.Shade([3]float64{nx1*cz - ny2*sz, nx1*sz + ny2*cz, nz2}, z2,
			math.Sin(x*23+y*19+grainPhase)*0.01)
		r.colors[index] = uint32(shaded[0])<<16 | uint32(shaded[1])<<8 | uint32(shaded[2])
	}
	for row := 1; row < grid-1; row++ {
		for column := 1; column < grid-1; column++ {
			i := row*grid + column
			d := r.shape[i]
			if d < -step {
				continue
			}
			x := float64(column)*step - extent
			y := float64(row)*step - extent
			dx := r.shape[i+1] - r.shape[i-1]
			dy := r.shape[i+grid] - r.shape[i-grid]
			length := math.Sqrt(dx*dx + dy*dy)
			if length == 0 {
				length = 1
			}
			gx, gy := dx/length, dy/length
			if d > 0 {
				edge := clamp(1-d/bevel, 0, 1)
				nz := math.Sqrt(1 - edge*edge)
				z := halfDepth - bevel + bevel*nz
				project([3]float64{x, y, z}, [3]float64{-gx * edge, -gy * edge, nz})
				project([3]float64{x, y, -z}, [3]float64{-gx * edge, -gy * edge, -nz})
			}
			if math.Abs(d) < step*0.8 {
				sideDepth := halfDepth - bevel
				layers := int(math.Ceil(sideDepth * 2 / step))
				for layer := 0; layer <= layers; layer++ {
					project(
						[3]float64{x - gx*d, y - gy*d, -sideDepth + float64(layer)/float64(layers)*sideDepth*2},
						[3]float64{-gx, -gy, 0},
					)
				}
			}
		}
	}
	for i := range r.cells {
		r.cells[i] = Cell{}
	}
	frame := make([]Cell, columns*rows)
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			var dots uint8
			var count, rsum, gsum, bsum uint32
			for point, bit := range dotBits {
				index := (row*4+point/2)*dotColumns + column*2 + point%2
				if !math.IsInf(float64(r.depth[index]), -1) {
					dots |= bit
					color := r.colors[index]
					rsum += (color >> 16) & 0xff
					gsum += (color >> 8) & 0xff
					bsum += color & 0xff
					count++
				}
			}
			cell := Cell{Dots: dots}
			if count > 0 {
				cell.R = uint8((rsum + count/2) / count)
				cell.G = uint8((gsum + count/2) / count)
				cell.B = uint8((bsum + count/2) / count)
			}
			frame[row*columns+column] = cell
		}
	}
	r.cacheKey = key
	r.cacheFrame = frame
	return frame
}

func (r *Renderer) init() {
	r.shape = make([]float64, grid*grid)
	samples := MaxColumns * MaxRows * 8
	r.depth = make([]float32, samples)
	r.colors = make([]uint32, samples)
	r.cells = make([]Cell, samples/8)
	fieldsOnce.Do(func() {
		fields[0] = field(codexPath, codexBounds)
		fields[1] = field(openAIPath, openAIBounds)
	})
}

var fields [2][]float64
var fieldsOnce sync.Once

func smooth(value float64) float64 {
	t := clamp(value, 0, 1)
	return t * t * t * (t*(t*6-15) + 10)
}

func clamp(value, low, high float64) float64 {
	return math.Max(low, math.Min(high, value))
}

type svgPath struct {
	remaining string
}

func (p *svgPath) number() float64 {
	p.remaining = strings.TrimLeft(p.remaining, " \t\r\n,")
	end := 1
	for end < len(p.remaining) && (p.remaining[end] == '.' || p.remaining[end] >= '0' && p.remaining[end] <= '9') {
		end++
	}
	value, _ := strconv.ParseFloat(p.remaining[:end], 64)
	p.remaining = p.remaining[end:]
	return value
}

func (p *svgPath) point() [2]float64 {
	return [2]float64{p.number(), p.number()}
}

func field(path string, bounds [4]float64) []float64 {
	parser := svgPath{remaining: path}
	var segments [][2][2]float64
	var point, start [2]float64
	for strings.TrimSpace(parser.remaining) != "" {
		command := parser.remaining[0]
		parser.remaining = parser.remaining[1:]
		var next [2]float64
		switch command {
		case 'M':
			point = parser.point()
			start = point
			continue
		case 'L':
			next = parser.point()
		case 'H':
			next = [2]float64{parser.number(), point[1]}
		case 'V':
			next = [2]float64{point[0], parser.number()}
		case 'Z':
			next = start
		case 'C':
			origin := point
			a := parser.point()
			b := parser.point()
			end := parser.point()
			for stepIndex := 1; stepIndex <= 12; stepIndex++ {
				t := float64(stepIndex) / 12
				u := 1 - t
				var sample [2]float64
				for axis := 0; axis < 2; axis++ {
					sample[axis] = u*u*u*origin[axis] +
						3*u*u*t*a[axis] +
						3*u*t*t*b[axis] +
						t*t*t*end[axis]
				}
				segments = append(segments, [2][2]float64{point, sample})
				point = sample
			}
			continue
		default:
			continue
		}
		segments = append(segments, [2][2]float64{point, next})
		point = next
	}
	scale := math.Max(bounds[2]-bounds[0], bounds[3]-bounds[1]) / 2
	for i := range segments {
		for axis := 0; axis < 2; axis++ {
			center := (bounds[axis] + bounds[axis+2]) / 2
			segments[i][0][axis] = (segments[i][0][axis] - center) / scale
			segments[i][1][axis] = (segments[i][1][axis] - center) / scale
		}
	}
	inside := make([]bool, grid*grid)
	for row := 0; row < grid; row++ {
		y := float64(row)*step - extent
		type crossing struct {
			x       float64
			winding int
		}
		crossings := make([]crossing, 0, len(segments))
		for _, segment := range segments {
			a, b := segment[0], segment[1]
			if a[1] <= y && b[1] > y || b[1] <= y && a[1] > y {
				winding := -1
				if b[1] > a[1] {
					winding = 1
				}
				crossings = append(crossings, crossing{
					x:       a[0] + (y-a[1])/(b[1]-a[1])*(b[0]-a[0]),
					winding: winding,
				})
			}
		}
		for i := 1; i < len(crossings); i++ {
			for j := i; j > 0 && crossings[j].x < crossings[j-1].x; j-- {
				crossings[j], crossings[j-1] = crossings[j-1], crossings[j]
			}
		}
		cursor, winding := 0, 0
		for column := 0; column < grid; column++ {
			x := float64(column)*step - extent
			for cursor < len(crossings) && crossings[cursor].x <= x {
				winding += crossings[cursor].winding
				cursor++
			}
			inside[row*grid+column] = winding != 0
		}
	}
	distance := make([]float64, grid*grid)
	for i := range distance {
		distance[i] = grid
	}
	for row := 1; row < grid-1; row++ {
		for column := 1; column < grid-1; column++ {
			i := row*grid + column
			if inside[i] != inside[i-1] || inside[i] != inside[i+1] ||
				inside[i] != inside[i-grid] || inside[i] != inside[i+grid] {
				distance[i] = 0.5
			}
		}
	}
	for pass := 0; pass < 2; pass++ {
		for row := 1; row < grid-1; row++ {
			for column := 1; column < grid-1; column++ {
				i := row*grid + column
				var neighbors [4]int
				if pass == 1 {
					i = (grid-1-row)*grid + grid - 1 - column
					neighbors = [4]int{i + 1, i + grid, i + grid - 1, i + grid + 1}
				} else {
					neighbors = [4]int{i - 1, i - grid, i - grid - 1, i - grid + 1}
				}
				value := distance[i]
				for n, neighbor := range neighbors {
					weight := 1.0
					if n >= 2 {
						weight = math.Sqrt2
					}
					value = math.Min(value, distance[neighbor]+weight)
				}
				distance[i] = value
			}
		}
	}
	out := make([]float64, len(distance))
	for i, value := range distance {
		sign := -1.0
		if inside[i] {
			sign = 1
		}
		out[i] = value * step * sign
	}
	return out
}

const codexPath = "M16.585 10C16.585 6.3632 13.6368 3.41504 10 3.41504C6.3632 3.41504 3.41504 6.3632 3.41504 10C3.41504 13.6368 6.3632 16.585 10 16.585C13.6368 16.585 16.585 13.6368 16.585 10ZM13.333 11.418L13.4678 11.4316C13.7705 11.4938 13.9979 11.7619 13.998 12.083C13.998 12.4042 13.7706 12.6722 13.4678 12.7344L13.333 12.748H10.833C10.4659 12.7479 10.168 12.4502 10.168 12.083C10.1681 11.716 10.466 11.4181 10.833 11.418H13.333ZM7.65332 12.4258C7.46427 12.7404 7.05603 12.8422 6.74121 12.6533C6.42642 12.4644 6.32391 12.0561 6.5127 11.7412L7.65332 12.4258ZM8.90332 9.6582C9.02956 9.86872 9.02956 10.1313 8.90332 10.3418L7.65332 12.4258L7.08301 12.083L6.5127 11.7412L7.55664 10L6.5127 8.25879L7.08301 7.91699L7.65332 7.57422L8.90332 9.6582ZM6.74121 7.34668C7.05603 7.15779 7.46427 7.25955 7.65332 7.57422L6.5127 8.25879C6.32391 7.94392 6.42642 7.53563 6.74121 7.34668ZM17.915 10C17.915 14.3713 14.3713 17.915 10 17.915C5.62867 17.915 2.08496 14.3713 2.08496 10C2.08496 5.62867 5.62867 2.08496 10 2.08496C14.3713 2.08496 17.915 5.62867 17.915 10Z"

var codexBounds = [4]float64{2.08496, 2.08496, 17.915, 17.915}

const openAIPath = "M11.6475 18.3409C11.0975 18.3409 10.575 18.2364 10.08 18.0274C9.58502 17.8184 9.14502 17.5269 8.76002 17.1529C8.34202 17.2959 7.90751 17.3674 7.45651 17.3674C6.71951 17.3674 6.03751 17.1859 5.41051 16.8229C4.78351 16.4599 4.27751 15.9649 3.89251 15.3379C3.51851 14.7109 3.33151 14.0124 3.33151 13.2424C3.33151 12.9234 3.37551 12.5769 3.46351 12.2029C3.02351 11.7959 2.68251 11.3284 2.44051 10.8004C2.19851 10.2614 2.07751 9.70044 2.07751 9.11744C2.07751 8.52344 2.20401 7.95144 2.45701 7.40144C2.71001 6.85144 3.06201 6.37844 3.51301 5.98244C3.97501 5.57544 4.50851 5.29494 5.11351 5.14094C5.23451 4.51394 5.48751 3.95294 5.87251 3.45794C6.26851 2.95194 6.75252 2.55594 7.32452 2.26994C7.89652 1.98394 8.50702 1.84094 9.15602 1.84094C9.70602 1.84094 10.2285 1.94544 10.7235 2.15444C11.2185 2.36344 11.6585 2.65494 12.0435 3.02894C12.4615 2.88594 12.896 2.81444 13.347 2.81444C14.084 2.81444 14.766 2.99594 15.393 3.35894C16.02 3.72194 16.5205 4.21694 16.8945 4.84394C17.2795 5.47094 17.472 6.16944 17.472 6.93944C17.472 7.25844 17.428 7.60494 17.34 7.97894C17.78 8.38594 18.121 8.85894 18.363 9.39794C18.605 9.92594 18.726 10.4814 18.726 11.0644C18.726 11.6584 18.5995 12.2304 18.3465 12.7804C18.0935 13.3304 17.736 13.8089 17.274 14.2159C16.823 14.6119 16.295 14.8869 15.69 15.0409C15.569 15.6679 15.3105 16.2289 14.9145 16.7239C14.5295 17.2299 14.051 17.6259 13.479 17.9119C12.907 18.1979 12.2965 18.3409 11.6475 18.3409ZM7.57201 16.2784C8.12201 16.2784 8.60051 16.1629 9.00751 15.9319L12.1095 14.1499C12.2195 14.0729 12.2745 13.9684 12.2745 13.8364V12.4174L8.28152 14.7109C8.03952 14.8539 7.79751 14.8539 7.55552 14.7109L4.43701 12.9124C4.43701 12.9454 4.43151 12.9839 4.42051 13.0279C4.42051 13.0719 4.42051 13.1379 4.42051 13.2259C4.42051 13.7869 4.55252 14.3039 4.81651 14.7769C5.09152 15.2389 5.47101 15.6019 5.95501 15.8659C6.43901 16.1409 6.97801 16.2784 7.57201 16.2784ZM7.73701 13.5889C7.80301 13.6219 7.86351 13.6384 7.91851 13.6384C7.97351 13.6384 8.02852 13.6219 8.08352 13.5889L9.32101 12.8794L5.34451 10.5694C5.10251 10.4264 4.98151 10.2119 4.98151 9.92594V6.34544C4.43151 6.58744 3.99151 6.96144 3.66151 7.46744C3.33151 7.96244 3.16651 8.51244 3.16651 9.11744C3.16651 9.65644 3.30401 10.1734 3.57901 10.6684C3.85401 11.1634 4.21151 11.5374 4.65151 11.7904L7.73701 13.5889ZM11.6475 17.2519C12.2305 17.2519 12.7585 17.1199 13.2315 16.8559C13.7045 16.5919 14.0785 16.2289 14.3535 15.7669C14.6285 15.3049 14.766 14.7879 14.766 14.2159V10.6519C14.766 10.5199 14.711 10.4209 14.601 10.3549L13.347 9.62894V14.2324C13.347 14.5184 13.226 14.7329 12.984 14.8759L9.86551 16.6744C10.4045 17.0594 10.9985 17.2519 11.6475 17.2519ZM12.2745 11.2129V8.96894L10.41 7.91294L8.52902 8.96894V11.2129L10.41 12.2689L12.2745 11.2129ZM7.45651 5.94944C7.45651 5.66344 7.57752 5.44894 7.81952 5.30594L10.938 3.50744C10.399 3.12244 9.80502 2.92994 9.15602 2.92994C8.57302 2.92994 8.04501 3.06194 7.57201 3.32594C7.09902 3.58994 6.72502 3.95294 6.45002 4.41494C6.18602 4.87694 6.05401 5.39394 6.05401 5.96594V9.51344C6.05401 9.64544 6.10901 9.74994 6.21902 9.82694L7.45651 10.5529V5.94944ZM15.8385 13.8364C16.3885 13.5944 16.823 13.2204 17.142 12.7144C17.472 12.2084 17.637 11.6584 17.637 11.0644C17.637 10.5254 17.4995 10.0084 17.2245 9.51344C16.9495 9.01844 16.592 8.64444 16.152 8.39144L13.0665 6.60944C13.0005 6.56544 12.94 6.54894 12.885 6.55994C12.83 6.55994 12.775 6.57644 12.72 6.60944L11.4825 7.30244L15.4755 9.62894C15.5965 9.69494 15.6845 9.78294 15.7395 9.89294C15.8055 9.99194 15.8385 10.1129 15.8385 10.2559V13.8364ZM12.522 5.45444C12.764 5.30044 13.006 5.30044 13.248 5.45444L16.383 7.28594C16.383 7.20894 16.383 7.10994 16.383 6.98894C16.383 6.46094 16.251 5.96044 15.987 5.48744C15.734 5.00344 15.3655 4.61844 14.8815 4.33244C14.4085 4.04644 13.8585 3.90344 13.2315 3.90344C12.6815 3.90344 12.203 4.01894 11.796 4.24994L8.69402 6.03194C8.58402 6.10894 8.52902 6.21344 8.52902 6.34544V7.76444L12.522 5.45444Z"

var openAIBounds = [4]float64{2.07751, 1.84094, 18.726, 18.3409}
