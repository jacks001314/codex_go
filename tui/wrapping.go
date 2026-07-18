package tui

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Rust parity: codex-rs/tui/src/wrapping.rs.

type WrapOptions struct {
	Width            int
	InitialIndent    string
	SubsequentIndent string
	BreakWords       bool
	PreserveURLs     bool
}

func TextContainsURLLike(text string) bool {
	for _, token := range strings.Fields(text) {
		if IsURLLikeToken(token) {
			return true
		}
	}
	return false
}

func TextHasMixedURLAndNonURLTokens(text string) bool {
	hasURL := false
	hasNonURL := false
	for _, raw := range strings.Fields(text) {
		if IsURLLikeToken(raw) {
			hasURL = true
		} else if isSubstantiveNonURLToken(raw) {
			hasNonURL = true
		}
		if hasURL && hasNonURL {
			return true
		}
	}
	return false
}

func IsURLLikeToken(raw string) bool {
	token := trimURLToken(raw)
	if token == "" {
		return false
	}
	return isAbsoluteURLLike(token) || isBareURLLike(token)
}

func AdaptiveWrapLine(text string, options WrapOptions) []string {
	if options.Width <= 0 {
		return []string{""}
	}
	if TextContainsURLLike(text) {
		options.PreserveURLs = true
	}
	return WrapLine(text, options)
}

func WrapLine(text string, options WrapOptions) []string {
	width := options.Width
	if width <= 0 {
		return []string{""}
	}
	tokens := strings.Fields(text)
	if len(tokens) == 0 {
		return []string{options.InitialIndent + strings.TrimRight(text, " \t")}
	}
	lines := []string{}
	current := options.InitialIndent
	currentLimit := width
	for _, token := range tokens {
		pieces := []string{token}
		breakWidth := tokenBreakWidth(current, options)
		if shouldBreakToken(token, breakWidth, options) {
			pieces = splitTokenByWidth(token, breakWidth)
		}
		for _, piece := range pieces {
			separator := ""
			if strings.TrimSpace(current) != "" && strings.TrimSpace(current) != strings.TrimSpace(options.InitialIndent) && strings.TrimSpace(current) != strings.TrimSpace(options.SubsequentIndent) {
				separator = " "
			}
			candidate := current + separator + piece
			if separator == "" && (current == options.InitialIndent || current == options.SubsequentIndent) {
				current = candidate
				continue
			}
			if lenColumns(candidate) <= currentLimit || strings.TrimSpace(current) == "" || current == options.InitialIndent || current == options.SubsequentIndent {
				if lenColumns(candidate) <= currentLimit || lenColumns(current) == 0 {
					current = candidate
					continue
				}
			}
			lines = append(lines, strings.TrimRight(current, " \t"))
			current = options.SubsequentIndent + piece
			currentLimit = width
		}
	}
	lines = append(lines, strings.TrimRight(current, " \t"))
	return lines
}

func WrapLines(lines []string, options WrapOptions) []string {
	out := []string{}
	for i, line := range lines {
		lineOptions := options
		if i > 0 {
			lineOptions.InitialIndent = options.SubsequentIndent
		}
		out = append(out, AdaptiveWrapLine(line, lineOptions)...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func tokenBreakWidth(current string, options WrapOptions) int {
	limit := options.Width - lenColumns(options.SubsequentIndent)
	if current == options.InitialIndent {
		limit = options.Width - lenColumns(options.InitialIndent)
	}
	return maxInt(1, limit)
}

func shouldBreakToken(token string, limit int, options WrapOptions) bool {
	if !options.BreakWords {
		return false
	}
	if options.PreserveURLs && IsURLLikeToken(token) {
		return false
	}
	return limit > 0 && lenColumns(token) > limit
}

func splitTokenByWidth(token string, width int) []string {
	if width <= 0 {
		return []string{token}
	}
	var out []string
	var current strings.Builder
	used := 0
	for _, r := range token {
		rw := runewidth.RuneWidth(r)
		if used > 0 && used+rw > width {
			out = append(out, current.String())
			current.Reset()
			used = 0
		}
		current.WriteRune(r)
		used += rw
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

func trimURLToken(token string) string {
	return strings.Trim(token, "()[]{}<>,.;:!'\"")
}

func isAbsoluteURLLike(token string) bool {
	if !strings.Contains(token, "://") {
		return false
	}
	parsed, err := url.Parse(token)
	if err == nil && parsed.Scheme != "" {
		if parsed.Host != "" {
			return true
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "ftp", "ftps", "ws", "wss":
			return false
		default:
			return parsed.Opaque != "" || parsed.Path != ""
		}
	}
	scheme, rest, ok := strings.Cut(token, "://")
	return ok && validScheme(scheme) && rest != ""
}

func isBareURLLike(token string) bool {
	hostPort, hasTrailer := splitHostPortAndTrailer(token)
	if hostPort == "" {
		return false
	}
	if !hasTrailer && !strings.HasPrefix(strings.ToLower(hostPort), "www.") {
		return false
	}
	host, port := splitHostAndPort(hostPort)
	if host == "" || (port != "" && !validPort(port)) {
		return false
	}
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil || isDomainName(host)
}

func splitHostPortAndTrailer(token string) (string, bool) {
	index := strings.IndexAny(token, "/?#")
	if index < 0 {
		return token, false
	}
	return token[:index], true
}

func splitHostAndPort(hostPort string) (string, string) {
	host, port, err := net.SplitHostPort(hostPort)
	if err == nil {
		return strings.Trim(host, "[]"), port
	}
	host, port, ok := strings.Cut(hostPort, ":")
	if ok && host != "" && port != "" && allDigits(port) {
		return host, port
	}
	return hostPort, ""
}

func validScheme(scheme string) bool {
	if scheme == "" {
		return false
	}
	for i, r := range scheme {
		if i == 0 && !isASCIIAlpha(r) {
			return false
		}
		if !isASCIIAlphaNum(r) && r != '+' && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

func validPort(port string) bool {
	if port == "" || len(port) > 5 || !allDigits(port) {
		return false
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 0 && value <= 65535
}

func isDomainName(host string) bool {
	labels := strings.Split(strings.ToLower(host), ".")
	if len(labels) < 2 || !isTLD(labels[len(labels)-1]) {
		return false
	}
	for _, label := range labels[:len(labels)-1] {
		if !isDomainLabel(label) {
			return false
		}
	}
	return true
}

func isTLD(label string) bool {
	if len(label) < 2 || len(label) > 63 {
		return false
	}
	for _, r := range label {
		if !isASCIIAlpha(r) {
			return false
		}
	}
	return true
}

func isDomainLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if !isASCIIAlphaNum(r) && r != '-' {
			return false
		}
	}
	return true
}

func isSubstantiveNonURLToken(raw string) bool {
	token := trimURLToken(raw)
	if token == "" || isDecorativeMarker(raw, token) {
		return false
	}
	for _, r := range token {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

func isDecorativeMarker(raw string, token string) bool {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "-", "*", "+", ">", "|":
		return true
	}
	if strings.HasSuffix(raw, ".") || strings.HasSuffix(raw, ")") {
		return allDigits(token)
	}
	return false
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isASCIIAlphaNum(r rune) bool {
	return isASCIIAlpha(r) || (r >= '0' && r <= '9')
}

func lenColumns(text string) int {
	return runewidth.StringWidth(text)
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
