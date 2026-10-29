package analytics

import "codex_go/chatgptapi"

// analyticsData is the endpoint-neutral projection the normalizer reads. It is
// never used to decode HTTP responses: optional fields select the inputs used by
// each report, and missing backend values stay missing (Rust report_data.rs).
type analyticsData struct {
	Data            []analyticsRecord
	Units           *string
	DataFreshnessTS *string
	// Series is nil when the report has no series metadata.
	Series []analyticsSeries
}

type analyticsSeries struct {
	Key   string
	Label string
}

type analyticsRecord struct {
	Date                      string
	Values                    map[string]float64
	ProductSurfaceUsageValues map[string]float64
	PremiumUsageValues        *analyticsPremiumUsage
	Attribution               []analyticsUsageAttribution
	CreditAmount              *float64
	ProductSurface            *string
	Totals                    *analyticsCount
	Models                    []analyticsModel
	Groups                    []analyticsTokenGroup
	Clients                   []analyticsClient
	PluginUsageOverviews      []analyticsTool
	SkillUsageOverviews       []analyticsTool
}

type analyticsPremiumUsage struct {
	CreditUsageCredits map[string]float64
}

type analyticsUsageAttribution struct {
	ThreadSource *string
	TurnTrigger  *string
	Model        *string
	Surface      *string
	Value        float64
}

type analyticsCount struct {
	Turns float64
}

type analyticsModel struct {
	Model                   string
	Credits                 *float64
	UncachedTextInputTokens *float64
	CachedTextInputTokens   *float64
	TextOutputTokens        *float64
	TextTotalTokens         *float64
	Turns                   *float64
	OnDemandCredits         *float64
	Speed                   *string
}

type analyticsTokenGroup struct {
	Dimensions              map[string]string
	IsOther                 bool
	UncachedTextInputTokens *float64
	CachedTextInputTokens   *float64
	TextOutputTokens        *float64
}

type analyticsClient struct {
	ClientID string
	Turns    float64
}

type analyticsTool struct {
	DisplayName      string
	InvocationCounts float64
}

// projectAnalyticsResponse mirrors the From<AnalyticsResponse> projection. The
// backend reports are typed, so each arm maps only the fields the normalizer
// reads for that report kind.
func projectAnalyticsResponse(response chatgptapi.AnalyticsResponse) analyticsData {
	switch response.Kind {
	case chatgptapi.AnalyticsReportUsage:
		if response.Usage == nil {
			return analyticsData{}
		}
		source := response.Usage
		data := analyticsData{Units: source.Units, DataFreshnessTS: source.DataFreshnessTS}
		for _, record := range source.Data {
			projected := analyticsRecord{
				Date:                      record.Date,
				ProductSurfaceUsageValues: nonNilFloatMap(record.ProductSurfaceUsageValues),
			}
			if record.PremiumUsageValues != nil {
				projected.PremiumUsageValues = &analyticsPremiumUsage{
					CreditUsageCredits: nonNilFloatMap(record.PremiumUsageValues.CreditUsageCredits),
				}
			}
			if record.Attribution != nil {
				projected.Attribution = make([]analyticsUsageAttribution, 0, len(record.Attribution))
				for _, entry := range record.Attribution {
					projected.Attribution = append(projected.Attribution, analyticsUsageAttribution{
						ThreadSource: stringPointer(entry.ThreadSource),
						TurnTrigger:  stringPointer(entry.TurnTrigger),
						Model:        stringPointer(entry.Model),
						Surface:      stringPointer(entry.Surface),
						Value:        entry.Value,
					})
				}
			}
			projected.Models = projectModels(record.Models)
			if record.Groups != nil {
				projected.Groups = make([]analyticsTokenGroup, 0, len(record.Groups))
				for _, group := range record.Groups {
					projected.Groups = append(projected.Groups, analyticsTokenGroup{
						Dimensions:              group.Dimensions,
						IsOther:                 group.IsOther != nil && *group.IsOther,
						UncachedTextInputTokens: floatPointerFromInt64Pointer(group.UncachedTextInputTokens),
						CachedTextInputTokens:   floatPointerFromInt64Pointer(group.CachedTextInputTokens),
						TextOutputTokens:        floatPointerFromInt64Pointer(group.TextOutputTokens),
					})
				}
			}
			data.Data = append(data.Data, projected)
		}
		return data
	case chatgptapi.AnalyticsReportCredits:
		if response.Credits == nil {
			return analyticsData{}
		}
		data := analyticsData{}
		for _, record := range response.Credits.Data {
			data.Data = append(data.Data, analyticsRecord{
				Date:           record.Date,
				CreditAmount:   floatPointer(record.CreditAmount),
				ProductSurface: stringPointer(record.ProductSurface),
			})
		}
		return data
	case chatgptapi.AnalyticsReportEnterpriseCredits:
		if response.EnterpriseCredits == nil {
			return analyticsData{}
		}
		source := response.EnterpriseCredits
		data := analyticsData{
			DataFreshnessTS: source.DataFreshnessTS,
			Series:          make([]analyticsSeries, 0, len(source.Series)),
		}
		for _, series := range source.Series {
			data.Series = append(data.Series, analyticsSeries{Key: series.Key, Label: series.Label})
		}
		for _, record := range source.Data {
			data.Data = append(data.Data, analyticsRecord{
				Date:   record.Date,
				Values: nonNilFloatMap(record.Values),
			})
		}
		return data
	case chatgptapi.AnalyticsReportMessages:
		if response.Messages == nil {
			return analyticsData{}
		}
		data := analyticsData{}
		for _, record := range response.Messages.Data {
			projected := analyticsRecord{
				Date:    record.Date,
				Totals:  &analyticsCount{Turns: float64(record.Totals.Turns)},
				Clients: make([]analyticsClient, 0, len(record.Clients)),
			}
			for _, client := range record.Clients {
				projected.Clients = append(projected.Clients, analyticsClient{
					ClientID: client.ClientID,
					Turns:    float64(client.Turns),
				})
			}
			projected.Models = projectModels(record.Models)
			data.Data = append(data.Data, projected)
		}
		return data
	case chatgptapi.AnalyticsReportPlugins:
		if response.Plugins == nil {
			return analyticsData{}
		}
		source := response.Plugins
		data := analyticsData{DataFreshnessTS: source.DataFreshnessTS}
		for _, record := range source.Data {
			overviews := make([]analyticsTool, 0, len(record.PluginUsageOverviews))
			for _, overview := range record.PluginUsageOverviews {
				overviews = append(overviews, analyticsTool{
					DisplayName:      overview.DisplayName,
					InvocationCounts: float64(overview.InvocationCounts),
				})
			}
			data.Data = append(data.Data, analyticsRecord{Date: record.Date, PluginUsageOverviews: overviews})
		}
		return data
	case chatgptapi.AnalyticsReportSkills:
		if response.Skills == nil {
			return analyticsData{}
		}
		source := response.Skills
		data := analyticsData{DataFreshnessTS: source.DataFreshnessTS}
		for _, record := range source.Data {
			overviews := make([]analyticsTool, 0, len(record.SkillUsageOverviews))
			for _, overview := range record.SkillUsageOverviews {
				overviews = append(overviews, analyticsTool{
					DisplayName:      overview.DisplayName,
					InvocationCounts: float64(overview.InvocationCounts),
				})
			}
			data.Data = append(data.Data, analyticsRecord{Date: record.Date, SkillUsageOverviews: overviews})
		}
		return data
	default:
		return analyticsData{}
	}
}

// projectModels mirrors From<backend::ModelUsage> for AnalyticsModel. A nil
// slice stays nil so the normalizer can tell "not reported" from "reported
// empty", matching Rust's Option<Vec<_>>.
func projectModels(models []chatgptapi.ModelUsage) []analyticsModel {
	if models == nil {
		return nil
	}
	projected := make([]analyticsModel, 0, len(models))
	for _, model := range models {
		converted := analyticsModel{
			Model:                   model.Model,
			Credits:                 floatPointer(model.Credits),
			UncachedTextInputTokens: floatPointerFromInt64Pointer(model.UncachedTextInputTokens),
			CachedTextInputTokens:   floatPointerFromInt64Pointer(model.CachedTextInputTokens),
			TextOutputTokens:        floatPointerFromInt64Pointer(model.TextOutputTokens),
			TextTotalTokens:         floatPointerFromInt64Pointer(model.TextTotalTokens),
			Turns:                   floatPointerFromInt64Pointer(model.Turns),
			OnDemandCredits:         model.OnDemandCredits,
			Speed:                   model.Speed,
		}
		projected = append(projected, converted)
	}
	return projected
}

func nonNilFloatMap(values map[string]float64) map[string]float64 {
	if values == nil {
		return map[string]float64{}
	}
	return values
}

func stringPointer(value string) *string {
	return &value
}

func floatPointer(value float64) *float64 {
	return &value
}

func floatPointerFromInt64Pointer(value *int64) *float64 {
	if value == nil {
		return nil
	}
	converted := float64(*value)
	return &converted
}
