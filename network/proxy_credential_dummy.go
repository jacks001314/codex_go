package network

// Rust parity: codex-rs/network-proxy/src/credential_broker/configured.rs
// ConfiguredCredentialPattern (#44056). Rust uses rand_regex to generate dummy
// credentials that still satisfy the configured pattern; Go has no equivalent
// dependency, so this re-implements bounded pattern sampling over
// regexp/syntax for the same purpose: the brokered dummy must look like a valid
// credential to the client while the proxy substitutes the real value.

import (
	"fmt"
	"math/rand"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync"
	"time"
)

const (
	// maxCredentialPatternBytes mirrors Rust MAX_REGEX_BYTES.
	maxCredentialPatternBytes = 2048
	// maxCredentialDummyBytes mirrors Rust MAX_DUMMY_VALUE_BYTES.
	maxCredentialDummyBytes = 2048
	// maxCredentialDummyAttempts mirrors Rust MAX_DUMMY_ATTEMPTS.
	maxCredentialDummyAttempts = 64
)

var (
	credentialDummyRandMu sync.Mutex
	credentialDummyRand   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// credentialPatternMatches mirrors ConfiguredCredentialPattern::full_matcher:
// the value must match the pattern in full and the pattern must not accept an
// empty value.
func credentialPatternMatches(pattern string, value string) (bool, error) {
	matcher, err := compileCredentialPattern(pattern)
	if err != nil {
		return false, err
	}
	return matcher.MatchString(value), nil
}

func compileCredentialPattern(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > maxCredentialPatternBytes {
		return nil, fmt.Errorf("credential pattern exceeds %d bytes", maxCredentialPatternBytes)
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, fmt.Errorf("invalid credential pattern: %w", err)
	}
	anchored := fmt.Sprintf("^(?:%s)$", parsed.String())
	matcher, err := regexp.Compile(anchored)
	if err != nil {
		return nil, fmt.Errorf("invalid credential pattern: %w", err)
	}
	if matcher.MatchString("") {
		return nil, fmt.Errorf("credential pattern must not match an empty value")
	}
	return matcher, nil
}

// GenerateCredentialDummy returns a random value that matches pattern, differs
// from realValue, and is safe to embed in an environment value / header. It
// reports false when the pattern cannot produce a bounded candidate (matching
// Rust's compile-time "could not independently generate a matching dummy").
func GenerateCredentialDummy(pattern string, realValue string) (string, bool) {
	credentialDummyRandMu.Lock()
	rng := credentialDummyRand
	credentialDummyRandMu.Unlock()
	return generateCredentialDummyWithRand(pattern, realValue, rng)
}

func generateCredentialDummyWithRand(pattern string, realValue string, rng *rand.Rand) (string, bool) {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", false
	}
	matcher, err := compileCredentialPattern(pattern)
	if err != nil {
		return "", false
	}
	for attempt := 0; attempt < maxCredentialDummyAttempts; attempt++ {
		// Rust prefers an ASCII candidate before falling back to the full
		// generator, so try ASCII first for both attempts.
		asciiOnly := attempt < maxCredentialDummyAttempts/2
		budget := maxCredentialDummyBytes
		candidate, ok := generateCredentialMatch(parsed, rng, asciiOnly, &budget)
		if !ok || candidate == "" || candidate == realValue {
			continue
		}
		if len(candidate) > maxCredentialDummyBytes || strings.ContainsRune(candidate, '\x00') {
			continue
		}
		if matcher.MatchString(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func generateCredentialMatch(re *syntax.Regexp, rng *rand.Rand, asciiOnly bool, budget *int) (string, bool) {
	if re == nil || budget == nil || *budget <= 0 {
		return "", false
	}
	switch re.Op {
	case syntax.OpNoMatch:
		return "", false
	case syntax.OpEmptyMatch,
		syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return "", true
	case syntax.OpLiteral:
		literal := string(re.Rune)
		if len(literal) > *budget {
			return "", false
		}
		*budget -= len(literal)
		return literal, true
	case syntax.OpCharClass:
		character, ok := sampleCredentialClass(re.Rune, rng, asciiOnly)
		if !ok || len(string(character)) > *budget {
			return "", false
		}
		*budget -= len(string(character))
		return string(character), true
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		character := rune('a' + rng.Intn(26))
		if len(string(character)) > *budget {
			return "", false
		}
		*budget -= len(string(character))
		return string(character), true
	case syntax.OpCapture:
		return generateCredentialMatch(re.Sub[0], rng, asciiOnly, budget)
	case syntax.OpConcat:
		var builder strings.Builder
		for _, sub := range re.Sub {
			part, ok := generateCredentialMatch(sub, rng, asciiOnly, budget)
			if !ok {
				return "", false
			}
			builder.WriteString(part)
		}
		return builder.String(), true
	case syntax.OpAlternate:
		if len(re.Sub) == 0 {
			return "", false
		}
		order := rng.Perm(len(re.Sub))
		for _, index := range order {
			attemptBudget := *budget
			if part, ok := generateCredentialMatch(re.Sub[index], rng, asciiOnly, &attemptBudget); ok {
				*budget = attemptBudget
				return part, true
			}
		}
		return "", false
	case syntax.OpStar:
		return generateCredentialRepeat(re.Sub[0], 0, rng.Intn(4), rng, asciiOnly, budget)
	case syntax.OpPlus:
		return generateCredentialRepeat(re.Sub[0], 1, 1+rng.Intn(4), rng, asciiOnly, budget)
	case syntax.OpQuest:
		return generateCredentialRepeat(re.Sub[0], 0, rng.Intn(2), rng, asciiOnly, budget)
	case syntax.OpRepeat:
		minimum := re.Min
		maximum := minimum
		if re.Max < 0 {
			maximum = minimum + rng.Intn(4)
		} else if re.Max > minimum {
			maximum = minimum + rng.Intn(re.Max-minimum+1)
		}
		return generateCredentialRepeat(re.Sub[0], minimum, maximum, rng, asciiOnly, budget)
	default:
		return "", false
	}
}

func generateCredentialRepeat(re *syntax.Regexp, minimum int, maximum int, rng *rand.Rand, asciiOnly bool, budget *int) (string, bool) {
	if maximum < minimum {
		maximum = minimum
	}
	var builder strings.Builder
	for index := 0; index < maximum; index++ {
		part, ok := generateCredentialMatch(re, rng, asciiOnly, budget)
		if !ok {
			if index < minimum {
				return "", false
			}
			break
		}
		builder.WriteString(part)
	}
	return builder.String(), true
}

func sampleCredentialClass(ranges []rune, rng *rand.Rand, asciiOnly bool) (rune, bool) {
	if len(ranges) < 2 {
		return 0, false
	}
	type classRange struct {
		low  rune
		high rune
		size int
	}
	candidates := make([]classRange, 0, len(ranges)/2)
	total := 0
	for index := 0; index+1 < len(ranges); index += 2 {
		low, high := ranges[index], ranges[index+1]
		if asciiOnly && low > 0x7f {
			continue
		}
		if asciiOnly && high > 0x7e {
			high = 0x7e
		}
		if high < low {
			continue
		}
		size := int(high-low) + 1
		candidates = append(candidates, classRange{low: low, high: high, size: size})
		total += size
	}
	if total <= 0 {
		return 0, false
	}
	pick := rng.Intn(total)
	for _, candidate := range candidates {
		if pick < candidate.size {
			return candidate.low + rune(pick), true
		}
		pick -= candidate.size
	}
	return 0, false
}
