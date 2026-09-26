package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/markdown_render/math/render.rs. A bounded
// Unicode layout for the deliberately small TeX math subset the TUI renders:
// accents apply only to single graphemes so their scope survives terminal
// rendering, unsupported Unicode scripts use explicit grouping, and structured
// layouts stay bounded.

const (
	mathMaxRows    = 16
	mathMaxColumns = 256
	mathMaxDepth   = 32
)

type mathLayout struct {
	rows     []string
	baseline int
}

func mathText(text string) mathLayout {
	return mathLayout{rows: []string{text}}
}

func (l mathLayout) width() int {
	width := 0
	for _, row := range l.rows {
		if rowWidth := codextui.DisplayWidth(row); rowWidth > width {
			width = rowWidth
		}
	}
	return width
}

// join concatenates two layouts on their shared baseline. Neighboring fraction
// bars are separated so their numerators cannot become one number.
func (l mathLayout) join(right mathLayout) (mathLayout, bool) {
	baseline := l.baseline
	if right.baseline > baseline {
		baseline = right.baseline
	}
	height := baseline + len(l.rows) - l.baseline
	if rightHeight := baseline + len(right.rows) - right.baseline; rightHeight > height {
		height = rightHeight
	}
	width := l.width()
	if len(l.rows) > 1 && len(right.rows) > 1 {
		width++
	}
	if height > mathMaxRows || width+right.width() > mathMaxColumns {
		return mathLayout{}, false
	}
	rows := make([]string, height)
	for index := range rows {
		var builder strings.Builder
		if leftIndex := index - (baseline - l.baseline); leftIndex >= 0 && leftIndex < len(l.rows) {
			builder.WriteString(l.rows[leftIndex])
		}
		builder.WriteString(strings.Repeat(" ", width-codextui.DisplayWidth(builder.String())))
		if rightIndex := index - (baseline - right.baseline); rightIndex >= 0 && rightIndex < len(right.rows) {
			builder.WriteString(right.rows[rightIndex])
		}
		rows[index] = builder.String()
	}
	return mathLayout{rows: rows, baseline: baseline}, true
}

func (l mathLayout) single() (string, bool) {
	if len(l.rows) == 1 {
		return l.rows[0], true
	}
	return "", false
}

// mathRender mirrors Rust's `render`: parse the trimmed source and join the rows,
// rejecting input that is empty or produces only blank rows.
func mathRender(source string, display bool) (string, bool) {
	parser := &mathParser{
		remaining:        strings.TrimSpace(source),
		display:          display,
		stackAnnotations: display,
	}
	result, ok := parser.sequence(mathSeqInput)
	if !ok {
		return "", false
	}
	blank := true
	for _, row := range result.rows {
		if strings.TrimSpace(row) != "" {
			blank = false
			break
		}
	}
	if blank {
		return "", false
	}
	return strings.Join(result.rows, "\n"), true
}

type mathSeqEnd int

const (
	mathSeqInput mathSeqEnd = iota
	mathSeqGroup
	mathSeqCell
)

type mathParser struct {
	remaining        string
	depth            int
	display          bool
	stackAnnotations bool
}

func (p *mathParser) take() (rune, bool) {
	if p.remaining == "" {
		return 0, false
	}
	ch, size := utf8.DecodeRuneInString(p.remaining)
	p.remaining = p.remaining[size:]
	return ch, true
}

func (p *mathParser) sequence(end mathSeqEnd) (mathLayout, bool) {
	if p.depth >= mathMaxDepth {
		return mathLayout{}, false
	}
	p.depth++
	result := mathText("")
	scripts := 0
	hasBase := false
	for p.remaining != "" {
		ch, _ := utf8.DecodeRuneInString(p.remaining)
		remaining := strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
		if end == mathSeqCell &&
			(strings.HasPrefix(remaining, "&") || strings.HasPrefix(remaining, "}") ||
				strings.HasPrefix(remaining, `\\`) || strings.HasPrefix(remaining, `\end{`)) {
			p.remaining = remaining
			p.depth--
			return result, true
		}
		if ch == '}' {
			if end != mathSeqGroup {
				return mathLayout{}, false
			}
			p.take()
			p.depth--
			return result, true
		}
		if ch == '^' || ch == '_' {
			script := 2
			if ch == '^' {
				script = 1
			}
			if !hasBase || scripts&script != 0 {
				return mathLayout{}, false
			}
			scripts |= script
		} else if !unicode.IsSpace(ch) {
			scripts = 0
		}
		atom, ok := p.atom()
		if !ok {
			return mathLayout{}, false
		}
		if !unicode.IsSpace(ch) && ch != '^' && ch != '_' {
			// Flattening a compound base would change the scope of a following script.
			text, single := atom.single()
			hasBase = single && uniseg.GraphemeClusterCount(text) == 1
		}
		joined, ok := result.join(atom)
		if !ok {
			return mathLayout{}, false
		}
		result = joined
	}
	p.depth--
	if end != mathSeqInput {
		return mathLayout{}, false
	}
	return result, true
}

func (p *mathParser) argument() (mathLayout, bool) {
	p.remaining = strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
	if strings.HasPrefix(p.remaining, "^") || strings.HasPrefix(p.remaining, "_") {
		return mathLayout{}, false
	}
	return p.atom()
}

func (p *mathParser) atom() (mathLayout, bool) {
	if p.depth >= mathMaxDepth {
		return mathLayout{}, false
	}
	p.depth++
	result, ok := p.atomInner()
	p.depth--
	return result, ok
}

func (p *mathParser) atomInner() (mathLayout, bool) {
	ch, ok := p.take()
	if !ok {
		return mathLayout{}, false
	}
	switch {
	case ch == '{':
		return p.sequence(mathSeqGroup)
	case ch == '\\':
		return p.command()
	case ch == '^' || ch == '_':
		arg, ok := p.compactArgument()
		if !ok {
			return mathLayout{}, false
		}
		plain := "0123456789+-=()abcdefghijklmnoprstuvwxyz"
		alphabet := "⁰¹²³⁴⁵⁶⁷⁸⁹⁺⁻⁼⁽⁾ᵃᵇᶜᵈᵉᶠᵍʰⁱʲᵏˡᵐⁿᵒᵖʳˢᵗᵘᵛʷˣʸᶻ"
		if ch == '_' {
			plain = "0123456789+-=()aehijklmnoprstuvx"
			alphabet = "₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎ₐₑₕᵢⱼₖₗₘₙₒₚᵣₛₜᵤᵥₓ"
		}
		text, single := arg.single()
		if !single {
			return mathLayout{}, false
		}
		mapped, ok := mapScriptText(text, plain, alphabet)
		if !ok {
			mapped = string(ch) + "{" + text + "}"
		}
		return mathText(mapped), true
	case ch == '}' || ch == '$' || ch == '%' || ch == '#' || ch == '&' || ch == '`':
		return mathLayout{}, false
	case unicode.IsSpace(ch):
		p.remaining = strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
		return mathText(" "), true
	case unicode.IsControl(ch):
		return mathLayout{}, false
	default:
		return mathText(string(ch)), true
	}
}

func (p *mathParser) command() (mathLayout, bool) {
	length := 0
	for length < len(p.remaining) && isASCIIAlphabetic(p.remaining[length]) {
		length++
	}
	if length == 0 {
		ch, ok := p.take()
		if !ok {
			return mathLayout{}, false
		}
		switch ch {
		case ',', ';', ':', ' ':
			return mathText(" "), true
		case '!':
			return mathText(""), true
		case '{':
			return mathText("{"), true
		case '}':
			return mathText("}"), true
		case '|':
			return mathText("‖"), true
		default:
			return mathLayout{}, false
		}
	}
	name := p.remaining[:length]
	p.remaining = p.remaining[length:]
	switch {
	case (name == "sum" || name == "bigwedge") && p.stackAnnotations:
		symbol := "∑"
		if name == "bigwedge" {
			symbol = "⋀"
		}
		return p.displayOperator(symbol)
	case name == "begin" || name == "boxed" || name == "underset" || name == "overset" ||
		name == "substack" || name == "mathcal":
		return p.structuredCommand(name)
	case name == "frac" || name == "dfrac" || name == "tfrac":
		numerator, ok := p.compactArgument()
		if !ok {
			return mathLayout{}, false
		}
		denominator, ok := p.compactArgument()
		if !ok {
			return mathLayout{}, false
		}
		numeratorText, ok := numerator.single()
		if !ok {
			return mathLayout{}, false
		}
		denominatorText, ok := denominator.single()
		if !ok {
			return mathLayout{}, false
		}
		if !p.display {
			return mathText("((" + numeratorText + ")/(" + denominatorText + "))"), true
		}
		width := codextui.DisplayWidth(numeratorText)
		if w := codextui.DisplayWidth(denominatorText); w > width {
			width = w
		}
		if width < 1 {
			width = 1
		}
		if width > mathMaxColumns {
			return mathLayout{}, false
		}
		return mathLayout{
			rows: []string{
				strings.Repeat(" ", (width-codextui.DisplayWidth(numeratorText))/2) + numeratorText,
				strings.Repeat("─", width),
				strings.Repeat(" ", (width-codextui.DisplayWidth(denominatorText))/2) + denominatorText,
			},
			baseline: 1,
		}, true
	case name == "sqrt":
		if strings.HasPrefix(strings.TrimLeftFunc(p.remaining, unicode.IsSpace), "[") {
			return mathLayout{}, false
		}
		radicand, ok := p.compactArgument()
		if !ok {
			return mathLayout{}, false
		}
		text, single := radicand.single()
		if !single {
			return mathLayout{}, false
		}
		return mathText("√(" + text + ")"), true
	case name == "mathbb":
		arg, ok := p.argument()
		if !ok {
			return mathLayout{}, false
		}
		text, single := arg.single()
		if !single {
			return mathLayout{}, false
		}
		switch text {
		case "R":
			return mathText("ℝ"), true
		case "C":
			return mathText("ℂ"), true
		case "N":
			return mathText("ℕ"), true
		case "Z":
			return mathText("ℤ"), true
		case "Q":
			return mathText("ℚ"), true
		case "P":
			return mathText("ℙ"), true
		default:
			return mathLayout{}, false
		}
	case name == "hat" || name == "widehat" || name == "bar" || name == "tilde" ||
		name == "vec" || name == "dot" || name == "ddot":
		arg, ok := p.argument()
		if !ok {
			return mathLayout{}, false
		}
		text, single := arg.single()
		if !single || uniseg.GraphemeClusterCount(text) != 1 ||
			strings.TrimSpace(text) == "" || codextui.DisplayWidth(text) == 0 {
			return mathLayout{}, false
		}
		accent := "\u0302"
		switch name {
		case "bar":
			accent = "\u0304"
		case "tilde":
			accent = "\u0303"
		case "vec":
			accent = "\u20d7"
		case "dot":
			accent = "\u0307"
		case "ddot":
			accent = "\u0308"
		}
		return mathText(text + accent), true
	case name == "mathrm" || name == "mathbf" || name == "mathit":
		return p.argument()
	case name == "text" || name == "operatorname":
		remaining := strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
		if !strings.HasPrefix(remaining, "{") {
			return mathLayout{}, false
		}
		remaining = remaining[1:]
		end := strings.IndexByte(remaining, '}')
		if end < 0 {
			return mathLayout{}, false
		}
		text := remaining[:end]
		if strings.ContainsAny(text, `{\$%#&`) || containsControl(text) {
			return mathLayout{}, false
		}
		p.remaining = remaining[end+1:]
		return mathText(text), true
	case name == "left" || name == "right" || name == "bigl" || name == "bigr":
		p.remaining = strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
		ch, ok := p.take()
		if !ok {
			return mathLayout{}, false
		}
		switch ch {
		case '.':
			return mathText(""), true
		case '(', ')', '[', ']', '|':
			return mathText(string(ch)), true
		case '<':
			return mathText("⟨"), true
		case '>':
			return mathText("⟩"), true
		case '\\':
			length := 0
			for length < len(p.remaining) && isASCIIAlphabetic(p.remaining[length]) {
				length++
			}
			if length == 0 {
				length = 1
			}
			if length > len(p.remaining) {
				return mathLayout{}, false
			}
			name := p.remaining[:length]
			p.remaining = p.remaining[length:]
			delimiter, ok := mathDelimiter(name)
			if !ok {
				return mathLayout{}, false
			}
			return mathText(delimiter), true
		default:
			return mathLayout{}, false
		}
	case name == "quad" || name == "qquad":
		return mathText(" "), true
	case name == "sin" || name == "cos" || name == "tan" || name == "log" || name == "ln" ||
		name == "exp" || name == "lim" || name == "max" || name == "min":
		return mathText(name), true
	default:
		text, ok := mathSymbol(name)
		if !ok {
			return mathLayout{}, false
		}
		return mathText(text), true
	}
}

func mapScriptText(text string, plain string, alphabet string) (string, bool) {
	plainRunes := []rune(plain)
	alphabetRunes := []rune(alphabet)
	var builder strings.Builder
	for _, value := range text {
		index := indexRune(plainRunes, value)
		if index < 0 || index >= len(alphabetRunes) {
			return "", false
		}
		builder.WriteRune(alphabetRunes[index])
	}
	return builder.String(), true
}

func indexRune(values []rune, target rune) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func isASCIIAlphabetic(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func containsControl(text string) bool {
	for _, character := range text {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

// mathSymbol mirrors Rust's `symbol`, falling back to the delimiter table.
func mathSymbol(name string) (string, bool) {
	symbols := map[string]string{
		"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ",
		"epsilon": "ϵ", "varepsilon": "ε", "zeta": "ζ", "eta": "η",
		"theta": "θ", "vartheta": "ϑ", "iota": "ι", "kappa": "κ",
		"varkappa": "ϰ", "lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ",
		"pi": "π", "varpi": "ϖ", "rho": "ρ", "varrho": "ϱ", "sigma": "σ",
		"varsigma": "ς", "tau": "τ", "upsilon": "υ", "phi": "ϕ", "varphi": "φ",
		"chi": "χ", "psi": "ψ", "omega": "ω",
		"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ",
		"Pi": "Π", "Sigma": "Σ", "Upsilon": "Υ", "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
		"sum": "∑", "prod": "∏", "coprod": "∐", "int": "∫", "iint": "∬",
		"iiint": "∭", "oint": "∮", "infty": "∞", "partial": "∂", "nabla": "∇",
		"hbar": "ℏ", "ell": "ℓ", "Re": "ℜ", "Im": "ℑ", "aleph": "ℵ",
		"imath": "ı", "jmath": "ȷ", "prime": "′", "angle": "∠",
		"dagger": "†", "ddagger": "‡", "pm": "±", "mp": "∓", "times": "×",
		"cdot": "·", "div": "÷", "circ": "∘", "bullet": "∙", "oplus": "⊕",
		"otimes": "⊗", "odot": "⊙",
		"le": "≤", "leq": "≤", "ge": "≥", "geq": "≥", "ne": "≠", "neq": "≠",
		"approx": "≈", "propto": "∝", "sim": "∼", "simeq": "≃", "cong": "≅",
		"lesssim": "≲", "gtrsim": "≳", "ll": "≪", "gg": "≫", "perp": "⊥",
		"parallel": "∥", "equiv": "≡", "in": "∈", "notin": "∉", "ni": "∋",
		"subset": "⊂", "subseteq": "⊆", "supset": "⊃", "supseteq": "⊇",
		"cup": "∪", "cap": "∩", "bigcup": "⋃", "bigcap": "⋂", "bigwedge": "⋀",
		"setminus": "∖", "emptyset": "∅", "varnothing": "∅",
		"land": "∧", "wedge": "∧", "lor": "∨", "vee": "∨", "neg": "¬", "lnot": "¬",
		"top": "⊤", "bot": "⊥", "forall": "∀", "exists": "∃", "nexists": "∄",
		"to": "→", "rightarrow": "→", "leftarrow": "←", "leftrightarrow": "↔",
		"mapsto": "↦", "uparrow": "↑", "downarrow": "↓", "updownarrow": "↕",
		"Leftarrow": "⇐", "impliedby": "⇐", "Rightarrow": "⇒", "implies": "⇒",
		"Leftrightarrow": "⇔", "iff": "⇔",
		"ldots": "…", "dots": "…", "cdots": "⋯", "vdots": "⋮", "ddots": "⋱",
	}
	if symbol, ok := symbols[name]; ok {
		return symbol, true
	}
	return mathDelimiter(name)
}

// mathDelimiter mirrors Rust's `delimiter`.
func mathDelimiter(name string) (string, bool) {
	delimiters := map[string]string{
		"langle": "⟨", "rangle": "⟩",
		"lbrace": "{", "{": "{", "rbrace": "}", "}": "}",
		"lbrack": "[", "rbrack": "]",
		"vert": "|", "lvert": "|", "rvert": "|",
		"Vert": "‖", "lVert": "‖", "rVert": "‖", "|": "‖",
		"lfloor": "⌊", "rfloor": "⌋", "lceil": "⌈", "rceil": "⌉",
	}
	delimiter, ok := delimiters[name]
	return delimiter, ok
}
