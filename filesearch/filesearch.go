package filesearch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type MatchType string

const (
	MatchFile      MatchType = "file"
	MatchDirectory MatchType = "directory"
)

type FileMatch struct {
	Score     int       `json:"score"`
	Path      string    `json:"path"`
	MatchType MatchType `json:"match_type"`
	Root      string    `json:"root"`
	FileName  string    `json:"file_name,omitempty"`
	Indices   []int     `json:"indices"`
}

func (m *FileMatch) FullPath() string {
	if m == nil {
		return ""
	}
	return filepath.Join(m.Root, m.Path)
}

func (m *FileMatch) MarshalJSON() ([]byte, error) {
	fileName := m.FileName
	if fileName == "" {
		fileName = FileNameFromPath(m.Path)
	}
	var indices []int
	if m.Indices != nil {
		indices = append([]int(nil), m.Indices...)
	}
	return json.Marshal(struct {
		Root      string    `json:"root"`
		Path      string    `json:"path"`
		MatchType MatchType `json:"match_type"`
		FileName  string    `json:"file_name"`
		Score     int       `json:"score"`
		Indices   []int     `json:"indices"`
	}{
		Root:      m.Root,
		Path:      m.Path,
		MatchType: m.MatchType,
		FileName:  fileName,
		Score:     m.Score,
		Indices:   indices,
	})
}

type Results struct {
	Matches         []FileMatch `json:"matches"`
	TotalMatchCount int         `json:"total_match_count"`
}

type Options struct {
	Limit          int
	Exclude        []string
	ComputeIndices bool
	IncludeHidden  bool
}

func DefaultOptions() Options {
	return Options{Limit: 20}
}

func Run(ctx context.Context, pattern string, roots []string, options Options) (*Results, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("search pattern is required")
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("at least one search directory is required")
	}
	if options.Limit <= 0 {
		options.Limit = 20
	}
	matches := []FileMatch{}
	total := 0
	for _, root := range roots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(absRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			name := entry.Name()
			if !options.IncludeHidden && strings.HasPrefix(name, ".") && path != absRoot {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			relative, err := filepath.Rel(absRoot, path)
			if err != nil || relative == "." {
				return nil
			}
			relative = filepath.ToSlash(relative)
			if excluded(relative, options.Exclude) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			score, indices, ok := Score(pattern, relative)
			if !ok {
				return nil
			}
			total++
			matchType := MatchFile
			if entry.IsDir() {
				matchType = MatchDirectory
			}
			if options.ComputeIndices {
				matches = append(matches, FileMatch{Score: score, Path: relative, MatchType: matchType, Root: absRoot, Indices: indices})
				return nil
			}
			matches = append(matches, FileMatch{Score: score, Path: relative, MatchType: matchType, Root: absRoot})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(matches, func(i int, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Path < matches[j].Path
	})
	if len(matches) > options.Limit {
		matches = matches[:options.Limit]
	}
	return &Results{Matches: matches, TotalMatchCount: total}, nil
}

func Score(pattern string, candidate string) (int, []int, bool) {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	candidateLower := strings.ToLower(candidate)
	if pattern == "" {
		return 0, nil, false
	}
	indices := make([]int, 0, len(pattern))
	searchFrom := 0
	score := 0
	lastIndex := -1
	for _, ch := range pattern {
		found := strings.IndexRune(candidateLower[searchFrom:], ch)
		if found < 0 {
			return 0, nil, false
		}
		index := searchFrom + found
		indices = append(indices, index)
		if index == 0 || candidateLower[index-1] == '/' || candidateLower[index-1] == '-' || candidateLower[index-1] == '_' || candidateLower[index-1] == '.' {
			score += 15
		} else {
			score += 5
		}
		if lastIndex >= 0 && index == lastIndex+1 {
			score += 8
		}
		lastIndex = index
		searchFrom = index + 1
	}
	score -= len(candidateLower) / 8
	return score, indices, true
}

func FileNameFromPath(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) {
		return path
	}
	return name
}

func excluded(relative string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(filepath.ToSlash(pattern))
		if pattern == "" {
			continue
		}
		if ok, _ := filepath.Match(pattern, relative); ok {
			return true
		}
		if strings.HasSuffix(pattern, "/**") && strings.HasPrefix(relative, strings.TrimSuffix(pattern, "/**")+"/") {
			return true
		}
		if strings.Contains(pattern, "/") {
			continue
		}
		if ok, _ := filepath.Match(pattern, filepath.Base(relative)); ok {
			return true
		}
	}
	return false
}
