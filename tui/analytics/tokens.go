package analytics

import (
	"errors"
	"math"
	"sort"
	"time"
)

// tokenHistory builds the daily text token history for one grouping (Rust
// analytics/tokens.rs history, #45741).
func tokenHistory(data analyticsData, grouping AccountAnalyticsGrouping, start, end time.Time) (AccountAnalyticsHistory, error) {
	return tokenFilteredHistory(data, grouping, start, end, nil)
}

// tokenFilteredHistory additionally keeps only one model's groups. Token counts
// are independent of credit and allowance accounting: components are summed per
// day, a reported model total wins when components are missing, and anything
// unparseable or fractional is an error.
func tokenFilteredHistory(data analyticsData, grouping AccountAnalyticsGrouping, start, end time.Time, modelFilter *string) (AccountAnalyticsHistory, error) {
	days := map[time.Time]map[string]float64{}
	for _, record := range data.Data {
		date, err := time.Parse("2006-01-02", record.Date)
		if err != nil {
			return AccountAnalyticsHistory{}, errors.New("Invalid token report date.")
		}
		if date.Before(start) || date.After(end) {
			continue
		}
		var groups []tokenCounts
		switch {
		case record.Groups != nil:
			for _, group := range record.Groups {
				key := "Unknown"
				if group.IsOther {
					key = "Other"
				} else if model, ok := group.Dimensions["model"]; ok {
					key = model
				}
				groups = append(groups, tokenCounts{
					key: key,
					components: [3]*float64{
						group.UncachedTextInputTokens,
						group.CachedTextInputTokens,
						group.TextOutputTokens,
					},
				})
			}
		case record.Models != nil:
			for _, model := range record.Models {
				groups = append(groups, tokenCounts{
					key: model.Model,
					components: [3]*float64{
						model.UncachedTextInputTokens,
						model.CachedTextInputTokens,
						model.TextOutputTokens,
					},
					total: model.TextTotalTokens,
				})
			}
		default:
			return AccountAnalyticsHistory{}, errors.New("Token breakdown is not available in this report.")
		}
		values := days[date]
		if values == nil {
			values = map[string]float64{}
			days[date] = values
		}
		for _, group := range groups {
			if modelFilter != nil && *modelFilter != group.key {
				continue
			}
			missingComponents := group.components[0] == nil && group.components[1] == nil && group.components[2] == nil
			if grouping == AccountAnalyticsGroupingModel && group.total != nil && (*group.total != 0 || missingComponents) {
				if !validTokenCount(*group.total) {
					return AccountAnalyticsHistory{}, errors.New("Invalid token count.")
				}
				values[group.key] += *group.total
				continue
			}
			if missingComponents {
				return AccountAnalyticsHistory{}, errors.New("Token counts were not reported.")
			}
			for index, label := range tokenComponentLabels {
				count := 0.0
				if group.components[index] != nil {
					count = *group.components[index]
				}
				if !validTokenCount(count) {
					return AccountAnalyticsHistory{}, errors.New("Invalid token count.")
				}
				key := label
				if grouping == AccountAnalyticsGroupingModel {
					key = group.key
				}
				values[key] += count
			}
		}
	}
	return AccountAnalyticsHistory{
		Unit:      AccountAnalyticsUnitTokens,
		UpdatedAt: analyticsFreshness(data.DataFreshnessTS),
		Data:      tokenHistoryDays(days),
	}, nil
}

type tokenCounts struct {
	key        string
	components [3]*float64
	total      *float64
}

// tokenComponentLabels is Rust's ["Uncached input", "Cached input", "Output"].
var tokenComponentLabels = [3]string{"Uncached input", "Cached input", "Output"}

func validTokenCount(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value == math.Trunc(value)
}

func tokenHistoryDays(days map[time.Time]map[string]float64) []AccountAnalyticsDay {
	dates := make([]time.Time, 0, len(days))
	for date := range days {
		dates = append(dates, date)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	history := make([]AccountAnalyticsDay, 0, len(dates))
	for _, date := range dates {
		values := days[date]
		keys := sortedFloatKeys(values)
		total := 0.0
		for _, key := range keys {
			total += values[key]
		}
		day := AccountAnalyticsDay{Date: date, Total: total}
		for _, key := range keys {
			day.Values = append(day.Values, AccountAnalyticsValue{Key: key, Label: key, Value: values[key]})
		}
		history = append(history, day)
	}
	return history
}
