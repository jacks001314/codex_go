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

// AnalyticsParts partitions one day's values into the three plotted categories
// plus an Other remainder. The remainder is not renormalized: the last slot
// collects every value whose key is not one of the first three categories
// (Rust analytics/render.rs parts, #45763).
func AnalyticsParts(values []AccountAnalyticsValue, categories []AccountAnalyticsValue) [4]float64 {
	var parts [4]float64
	for _, value := range values {
		index := 3
		for position := 0; position < len(categories) && position < 3; position++ {
			if categories[position].Key == value.Key {
				index = position
				break
			}
		}
		parts[index] += value.Value
	}
	return parts
}
