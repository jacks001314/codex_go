package analytics

import (
	"math"
	"sort"
)

// AnalyticsCategories aggregates a history's daily values per key, ordered by
// descending absolute total and then by key (Rust analytics/render.rs
// categories, #45762).
func AnalyticsCategories(history *AccountAnalyticsHistory) []AccountAnalyticsValue {
	if history == nil {
		return nil
	}
	totals := map[string]AccountAnalyticsValue{}
	order := []string{}
	for _, day := range history.Data {
		for _, value := range day.Values {
			total, seen := totals[value.Key]
			if !seen {
				total = AccountAnalyticsValue{Key: value.Key, Label: value.Label}
				order = append(order, value.Key)
			}
			total.Value += value.Value
			totals[value.Key] = total
		}
	}
	values := make([]AccountAnalyticsValue, 0, len(order))
	for _, key := range order {
		values = append(values, totals[key])
	}
	sort.SliceStable(values, func(i, j int) bool {
		left := math.Abs(values[i].Value)
		right := math.Abs(values[j].Value)
		if left != right {
			return left > right
		}
		return values[i].Key < values[j].Key
	})
	return values
}
