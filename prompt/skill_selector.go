package prompt

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	skillSelectorMaxQueryBytes    = 4 * 1024
	skillSelectorMaxQueryTerms    = 64
	skillSelectorMaxQueryGrams    = 512
	skillSelectorMaxDocumentBytes = 4 * 1024
	skillSelectorMaxDocumentTerms = 256
	skillSelectorMaxDocumentGrams = 512
	skillSelectorMaxCandidates    = 1000
	skillSelectorMaxResults       = 50
)

var skillSelectorStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "do": true, "for": true, "from": true, "how": true,
	"i": true, "in": true, "is": true, "it": true, "me": true, "my": true,
	"of": true, "on": true, "or": true, "please": true, "that": true, "the": true,
	"this": true, "to": true, "use": true, "we": true, "what": true, "when": true,
	"where": true, "which": true, "with": true, "you": true, "your": true,
}

type SkillSelectionDocument struct {
	ID               int
	Name             string
	ShortDescription string
	Description      string
}

type CheapSkillSelection struct {
	CandidateIDs          []int
	QueryTermCount        int
	QueryTruncated        bool
	CandidateSetTruncated bool
}

type scoredSkill struct {
	score float64
	id    int
	name  string
}

func SelectSkillsWeightedLexical(query string, documents []SkillSelectionDocument, limit int) CheapSkillSelection {
	query, bytesTruncated := boundedSkillSelectorString(query, skillSelectorMaxQueryBytes)
	phrase := skillSelectorNormalizePhrase(query)
	terms, termsTruncated := skillSelectorQueryTerms(phrase)
	result := CheapSkillSelection{QueryTermCount: len(terms), QueryTruncated: bytesTruncated || termsTruncated, CandidateSetTruncated: len(documents) > skillSelectorMaxCandidates}
	if len(terms) == 0 || limit <= 0 {
		return result
	}
	scored := make([]scoredSkill, 0)
	for _, document := range documents[:min(len(documents), skillSelectorMaxCandidates)] {
		score := skillSelectorWeightedScore(phrase, terms, document)
		if score > 0 {
			scored = append(scored, scoredSkill{score: float64(score), id: document.ID, name: document.Name})
		}
	}
	result.CandidateIDs = rankedSkillIDs(scored, limit)
	return result
}

func SelectSkillsMultiQueryLexical(query string, documents []SkillSelectionDocument, limit int) CheapSkillSelection {
	full := SelectSkillsWeightedLexical(query, documents, min(limit, skillSelectorMaxResults))
	views := skillSelectorQueryViews(query)
	if len(views) <= 1 || limit <= 0 {
		return full
	}
	type candidate struct{ id, bestRank, fullRank, viewCount, firstView int }
	candidates := map[int]candidate{}
	record := func(selection CheapSkillSelection, view int) {
		for index, id := range selection.CandidateIDs {
			rank := index + 1
			current, ok := candidates[id]
			if !ok {
				fullRank := int(^uint(0) >> 1)
				if view == 0 {
					fullRank = rank
				}
				candidates[id] = candidate{id, rank, fullRank, 1, view}
				continue
			}
			if rank < current.bestRank {
				current.bestRank = rank
			}
			current.viewCount++
			if view == 0 {
				current.fullRank = rank
			}
			candidates[id] = current
		}
	}
	record(full, 0)
	for index, view := range views[1:] {
		selection := SelectSkillsWeightedLexical(view, documents, min(limit, skillSelectorMaxResults))
		full.QueryTruncated = full.QueryTruncated || selection.QueryTruncated
		full.CandidateSetTruncated = full.CandidateSetTruncated || selection.CandidateSetTruncated
		record(selection, index+1)
	}
	ordered := make([]candidate, 0, len(candidates))
	for _, value := range candidates {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.bestRank != b.bestRank {
			return a.bestRank < b.bestRank
		}
		if a.fullRank != b.fullRank {
			return a.fullRank < b.fullRank
		}
		if a.viewCount != b.viewCount {
			return a.viewCount > b.viewCount
		}
		if a.firstView != b.firstView {
			return a.firstView < b.firstView
		}
		return a.id < b.id
	})
	count := min(limit, skillSelectorMaxResults, len(ordered))
	full.CandidateIDs = make([]int, count)
	for i := range full.CandidateIDs {
		full.CandidateIDs[i] = ordered[i].id
	}
	return full
}

func skillSelectorWeightedScore(queryPhrase string, queryTerms []string, document SkillSelectionDocument) int {
	name := skillSelectorNormalizeDocument(document.Name)
	short := skillSelectorNormalizeDocument(document.ShortDescription)
	description := skillSelectorNormalizeDocument(document.Description)
	nameTerms, shortTerms, descriptionTerms := skillSelectorTermSet(name), skillSelectorTermSet(short), skillSelectorTermSet(description)
	score, matched := 0, 0
	if name != "" && skillSelectorContainsPhrase(queryPhrase, name) {
		score += 256
	}
	for _, term := range queryTerms {
		found := false
		if name == term {
			score += 128
			found = true
		} else if nameTerms[term] {
			score += 64
			found = true
		} else if skillSelectorContainsRelated(nameTerms, term) {
			score += 24
			found = true
		}
		if shortTerms[term] {
			score += 16
			found = true
		} else if skillSelectorContainsRelated(shortTerms, term) {
			score += 6
			found = true
		}
		if descriptionTerms[term] {
			score += 4
			found = true
		} else if skillSelectorContainsRelated(descriptionTerms, term) {
			score++
			found = true
		}
		if found {
			matched++
		}
	}
	return score + matched*matched
}

func skillSelectorQueryViews(query string) []string {
	query, _ = boundedSkillSelectorString(query, skillSelectorMaxQueryBytes)
	full := strings.TrimSpace(query)
	if full == "" {
		return nil
	}
	views := []string{full}
	seen := map[string]bool{full: true}
	sentences := strings.FieldsFunc(full, func(r rune) bool { return r == '\n' || r == '\r' || r == '.' || r == '!' || r == '?' || r == ';' })
	for _, sentence := range sentences {
		for _, clause := range skillSelectorSplitConnectors(sentence) {
			clause = strings.TrimSpace(clause)
			if len([]rune(clause)) < 2 || seen[clause] {
				continue
			}
			seen[clause] = true
			views = append(views, clause)
			if len(views) == 8 {
				return views
			}
		}
	}
	return views
}

func skillSelectorSplitConnectors(value string) []string {
	lower := strings.ToLower(value)
	connectors := []string{" and then ", " and ", " then ", " also "}
	segments := []string{}
	start := 0
	for start < len(value) {
		position, length := -1, 0
		for _, connector := range connectors {
			if offset := strings.Index(lower[start:], connector); offset >= 0 && (position < 0 || start+offset < position) {
				position, length = start+offset, len(connector)
			}
		}
		if position < 0 {
			break
		}
		segments = append(segments, value[start:position])
		start = position + length
	}
	return append(segments, value[start:])
}

func skillSelectorNormalizeDocument(value string) string {
	value, _ = boundedSkillSelectorString(value, skillSelectorMaxDocumentBytes)
	return skillSelectorNormalizePhrase(value)
}
func skillSelectorNormalizePhrase(value string) string {
	return strings.Join(skillSelectorNormalizedTerms(value), " ")
}
func skillSelectorTermSet(value string) map[string]bool {
	out := map[string]bool{}
	terms := strings.Fields(value)
	for _, term := range terms[:min(len(terms), skillSelectorMaxDocumentTerms)] {
		out[term] = true
	}
	return out
}
func skillSelectorContainsPhrase(haystack, needle string) bool {
	return haystack == needle || strings.HasPrefix(haystack, needle+" ") || strings.HasSuffix(haystack, " "+needle) || strings.Contains(haystack, " "+needle+" ")
}
func skillSelectorContainsRelated(terms map[string]bool, query string) bool {
	if len([]rune(query)) < 4 {
		return false
	}
	for term := range terms {
		if len([]rune(term)) >= 4 && (strings.HasPrefix(term, query) || strings.HasPrefix(query, term)) {
			return true
		}
	}
	return false
}

func SelectSkillsCharacterNgram(query string, documents []SkillSelectionDocument, limit int) CheapSkillSelection {
	query, bytesTruncated := boundedSkillSelectorString(query, skillSelectorMaxQueryBytes)
	terms, termsTruncated := skillSelectorQueryTerms(query)
	grams, gramsTruncated := skillSelectorGrams(terms, skillSelectorMaxQueryGrams)
	result := CheapSkillSelection{QueryTermCount: len(terms), QueryTruncated: bytesTruncated || termsTruncated || gramsTruncated, CandidateSetTruncated: len(documents) > skillSelectorMaxCandidates}
	if len(grams) == 0 || limit <= 0 {
		return result
	}
	type prepared struct {
		id     int
		name   string
		fields [3]map[string]bool
	}
	preparedDocs := make([]prepared, 0, min(len(documents), skillSelectorMaxCandidates))
	frequencies := map[string]int{}
	for _, document := range documents[:min(len(documents), skillSelectorMaxCandidates)] {
		fields := [3]map[string]bool{skillSelectorDocumentGrams(document.Name), skillSelectorDocumentGrams(document.ShortDescription), skillSelectorDocumentGrams(document.Description)}
		seen := map[string]bool{}
		for _, field := range fields {
			for gram := range field {
				seen[gram] = true
			}
		}
		for gram := range seen {
			frequencies[gram]++
		}
		preparedDocs = append(preparedDocs, prepared{id: document.ID, name: document.Name, fields: fields})
	}
	minimumMatches := min(len(grams), 3)
	scored := make([]scoredSkill, 0)
	weights := [3]float64{8, 4, 1}
	for _, document := range preparedDocs {
		score, matched := 0.0, 0
		for _, gram := range grams {
			frequency := frequencies[gram]
			if frequency == 0 {
				continue
			}
			fieldWeight := 0.0
			for index, field := range document.fields {
				if field[gram] {
					fieldWeight += weights[index]
				}
			}
			if fieldWeight > 0 {
				score += math.Log(1+(float64(len(preparedDocs)-frequency)+0.5)/(float64(frequency)+0.5)) * fieldWeight
				matched++
			}
		}
		if matched >= minimumMatches {
			scored = append(scored, scoredSkill{score, document.id, document.name})
		}
	}
	result.CandidateIDs = rankedSkillIDs(scored, limit)
	return result
}

func SelectSkillsFieldedBM25(query string, documents []SkillSelectionDocument, limit int) CheapSkillSelection {
	query, bytesTruncated := boundedSkillSelectorString(query, skillSelectorMaxQueryBytes)
	terms, termsTruncated := skillSelectorQueryTerms(query)
	result := CheapSkillSelection{QueryTermCount: len(terms), QueryTruncated: bytesTruncated || termsTruncated, CandidateSetTruncated: len(documents) > skillSelectorMaxCandidates}
	if len(terms) == 0 || limit <= 0 {
		return result
	}
	type prepared struct {
		id     int
		name   string
		fields [3][]string
	}
	preparedDocs := make([]prepared, 0, min(len(documents), skillSelectorMaxCandidates))
	frequencies := map[string]int{}
	totals := [3]int{}
	for _, document := range documents[:min(len(documents), skillSelectorMaxCandidates)] {
		fields := [3][]string{skillSelectorDocumentTerms(document.Name), skillSelectorDocumentTerms(document.ShortDescription), skillSelectorDocumentTerms(document.Description)}
		seen := map[string]bool{}
		for index, field := range fields {
			totals[index] += len(field)
			for _, term := range field {
				seen[term] = true
			}
		}
		for term := range seen {
			frequencies[term]++
		}
		preparedDocs = append(preparedDocs, prepared{id: document.ID, name: document.Name, fields: fields})
	}
	averages := [3]float64{}
	for index := range averages {
		if len(preparedDocs) > 0 {
			averages[index] = float64(totals[index]) / float64(len(preparedDocs))
		}
	}
	weights := [3]float64{8, 4, 1}
	scored := make([]scoredSkill, 0)
	for _, document := range preparedDocs {
		score := 0.0
		for _, term := range terms {
			frequency := frequencies[term]
			if frequency == 0 {
				continue
			}
			weightedTF := 0.0
			for index, field := range document.fields {
				count := 0
				for _, value := range field {
					if value == term {
						count++
					}
				}
				if count == 0 {
					continue
				}
				ratio := 1.0
				if averages[index] != 0 {
					ratio = float64(len(field)) / averages[index]
				}
				weightedTF += weights[index] * float64(count) / (1 - 0.75 + 0.75*ratio)
			}
			if weightedTF > 0 {
				idf := math.Log(1 + (float64(len(preparedDocs)-frequency)+0.5)/(float64(frequency)+0.5))
				score += idf * weightedTF * 2.2 / (weightedTF + 1.2)
			}
		}
		if score > 0 {
			scored = append(scored, scoredSkill{score, document.id, document.name})
		}
	}
	result.CandidateIDs = rankedSkillIDs(scored, limit)
	return result
}

func rankedSkillIDs(scored []scoredSkill, limit int) []int {
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].name != scored[j].name {
			return scored[i].name < scored[j].name
		}
		return scored[i].id < scored[j].id
	})
	limit = min(limit, skillSelectorMaxResults, len(scored))
	ids := make([]int, limit)
	for i := range ids {
		ids[i] = scored[i].id
	}
	return ids
}

func skillSelectorQueryTerms(value string) ([]string, bool) {
	seen := map[string]bool{}
	terms := make([]string, 0)
	for _, term := range skillSelectorNormalizedTerms(value) {
		if len([]rune(term)) < 2 || skillSelectorStopWords[term] || seen[term] {
			continue
		}
		if len(terms) == skillSelectorMaxQueryTerms {
			return terms, true
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms, false
}

func skillSelectorDocumentTerms(value string) []string {
	value, _ = boundedSkillSelectorString(value, skillSelectorMaxDocumentBytes)
	terms := skillSelectorNormalizedTerms(value)
	return terms[:min(len(terms), skillSelectorMaxDocumentTerms)]
}

func skillSelectorDocumentGrams(value string) map[string]bool {
	grams, _ := skillSelectorGrams(skillSelectorDocumentTerms(value), skillSelectorMaxDocumentGrams)
	out := map[string]bool{}
	for _, gram := range grams {
		out[gram] = true
	}
	return out
}

func skillSelectorGrams(terms []string, limit int) ([]string, bool) {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, term := range terms {
		runes := []rune(term)
		minimum := 2
		if isASCIIString(term) && len(runes) > 2 {
			minimum = 3
		}
		for size := minimum; size <= min(5, len(runes)); size++ {
			for start := 0; start+size <= len(runes); start++ {
				gram := string(runes[start : start+size])
				if seen[gram] {
					continue
				}
				if len(out) == limit {
					return out, true
				}
				seen[gram] = true
				out = append(out, gram)
			}
		}
	}
	return out, false
}

func skillSelectorNormalizedTerms(value string) []string {
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(unicode.ToLower(r))
		} else {
			builder.WriteByte(' ')
		}
	}
	return strings.Fields(builder.String())
}

func boundedSkillSelectorString(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	end := maxBytes
	for end > 0 && !utf8Boundary(value, end) {
		end--
	}
	return value[:end], true
}

func utf8Boundary(value string, index int) bool {
	return index == 0 || index == len(value) || value[index]&0xc0 != 0x80
}
func isASCIIString(value string) bool {
	for _, r := range value {
		if r > 127 {
			return false
		}
	}
	return true
}
