package filesearch

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
)

const FindUpDefaultMaxConcurrentProbes = 8

type FindUpErrorPolicy string

const (
	FindUpPropagate FindUpErrorPolicy = "propagate"
	FindUpIgnore    FindUpErrorPolicy = "ignore"
)

type FindUpMetadataProbe func(path string) (os.FileInfo, error)

type FindUpOptions struct {
	Markers     []string
	ErrorPolicy FindUpErrorPolicy
	Probe       FindUpMetadataProbe
}

func FindUpNearestAncestorWithMarkers(start string, markers []string, policy FindUpErrorPolicy) (string, bool, error) {
	return FindUpNearestAncestor(start, &FindUpOptions{Markers: markers, ErrorPolicy: policy})
}

func FindUpNearestAncestor(start string, options *FindUpOptions) (string, bool, error) {
	if start == "" {
		return "", false, nil
	}
	if options == nil {
		options = &FindUpOptions{}
	}
	markers := normalizedFindUpMarkers(options.Markers)
	if len(markers) == 0 {
		return "", false, nil
	}
	probe := options.Probe
	if probe == nil {
		probe = os.Stat
	}
	policy := options.ErrorPolicy
	if policy == "" {
		policy = FindUpPropagate
	}
	for _, ancestor := range FindUpAncestors(start) {
		for _, marker := range markers {
			_, err := probe(filepath.Join(ancestor, marker))
			if err == nil {
				return ancestor, true, nil
			}
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if policy == FindUpIgnore {
				continue
			}
			return "", false, err
		}
	}
	return "", false, nil
}

func FindUpAncestors(start string) []string {
	cleaned := filepath.Clean(start)
	var out []string
	for cursor := cleaned; ; cursor = filepath.Dir(cursor) {
		out = append(out, cursor)
		parent := filepath.Dir(cursor)
		if parent == cursor {
			break
		}
	}
	return out
}

func normalizedFindUpMarkers(markers []string) []string {
	out := make([]string, 0, len(markers))
	seen := map[string]bool{}
	for _, marker := range markers {
		if marker == "" || seen[marker] {
			continue
		}
		out = append(out, marker)
		seen[marker] = true
	}
	sort.Strings(out)
	return out
}
