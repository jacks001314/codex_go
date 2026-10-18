package chatgptapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// AnalyticsReportKind identifies one account analytics report.
type AnalyticsReportKind string

const (
	AnalyticsReportUsage             AnalyticsReportKind = "usage"
	AnalyticsReportEnterpriseTokens  AnalyticsReportKind = "enterprise_tokens"
	AnalyticsReportCredits           AnalyticsReportKind = "credits"
	AnalyticsReportWorkspaceCredits  AnalyticsReportKind = "workspace_credits"
	AnalyticsReportEnterpriseCredits AnalyticsReportKind = "enterprise_credits"
	AnalyticsReportMessages          AnalyticsReportKind = "messages"
	AnalyticsReportPlugins           AnalyticsReportKind = "plugins"
	AnalyticsReportSkills            AnalyticsReportKind = "skills"
)

// AnalyticsReport selects one analytics endpoint. Breakdown applies to
// EnterpriseCredits, and Limit applies to Plugins and Skills.
type AnalyticsReport struct {
	Kind      AnalyticsReportKind
	Breakdown string
	Limit     uint8
}

// AnalyticsResponse contains the typed report selected by AnalyticsReport.
// Exactly one field other than Kind is non-nil on success.
type AnalyticsResponse struct {
	Kind              AnalyticsReportKind
	Usage             *DailyProductSurfaceUsageResponse
	Credits           *CreditUsageEventsResponse
	EnterpriseCredits *CurrentUserCreditUsageResponse
	Messages          *DailyWorkspaceUsageCountResponse
	Plugins           *PluginUsageMetricsResponse
	Skills            *DailySkillUsageMetricsResponse
}

// ActiveUsersSummary summarizes active users across clients and models.
type ActiveUsersSummary struct {
	TotalUsers int64                    `json:"total_users"`
	Clients    []ClientActiveUsersCount `json:"clients"`
	Models     []ModelActivitySummary   `json:"models,omitempty"`
}

// ClientActiveUsersCount counts active users for one client.
type ClientActiveUsersCount struct {
	ClientID string `json:"client_id"`
	Users    int64  `json:"users"`
}

// ClientWorkspaceUsageCount reports workspace usage attributed to one client.
type ClientWorkspaceUsageCount struct {
	ClientID                string   `json:"client_id"`
	Users                   int64    `json:"users"`
	Threads                 int64    `json:"threads"`
	Turns                   int64    `json:"turns"`
	Credits                 float64  `json:"credits"`
	CostUSD                 *string  `json:"cost_usd,omitempty"`
	OnDemandCredits         *float64 `json:"on_demand_credits,omitempty"`
	UncachedTextInputTokens *int64   `json:"uncached_text_input_tokens,omitempty"`
	CachedTextInputTokens   *int64   `json:"cached_text_input_tokens,omitempty"`
	TextOutputTokens        *int64   `json:"text_output_tokens,omitempty"`
	TextTotalTokens         *int64   `json:"text_total_tokens,omitempty"`
}

// ConsumptionUsageGroup reports usage dimensions for one grouped row.
type ConsumptionUsageGroup struct {
	Dimensions              map[string]string `json:"dimensions"`
	IsOther                 *bool             `json:"is_other,omitempty"`
	Credits                 float64           `json:"credits"`
	OnDemandCredits         *float64          `json:"on_demand_credits,omitempty"`
	UncachedTextInputTokens *int64            `json:"uncached_text_input_tokens,omitempty"`
	CachedTextInputTokens   *int64            `json:"cached_text_input_tokens,omitempty"`
	TextOutputTokens        *int64            `json:"text_output_tokens,omitempty"`
	TextTotalTokens         *int64            `json:"text_total_tokens,omitempty"`
	TotalTokens             *int64            `json:"total_tokens,omitempty"`
}

// CreditUsageEventBySurface is one signed credit usage event.
type CreditUsageEventBySurface struct {
	Date           string  `json:"date"`
	ProductSurface string  `json:"product_surface"`
	CreditAmount   float64 `json:"credit_amount"`
	UsageID        *string `json:"usage_id,omitempty"`
}

// CreditUsageEventsResponse contains signed credit usage events.
type CreditUsageEventsResponse struct {
	Data []CreditUsageEventBySurface `json:"data"`
}

// CurrentUserCreditUsageDatum is one dated enterprise credit breakdown row.
type CurrentUserCreditUsageDatum struct {
	Date   string             `json:"date"`
	Values map[string]float64 `json:"values"`
}

// CurrentUserCreditUsageResponse is a dated enterprise credit report.
type CurrentUserCreditUsageResponse struct {
	Breakdown       string                         `json:"breakdown"`
	Data            []CurrentUserCreditUsageDatum  `json:"data"`
	Series          []CurrentUserCreditUsageSeries `json:"series"`
	Unit            *string                        `json:"unit,omitempty"`
	DataFreshnessTS *string                        `json:"data_freshness_ts,omitempty"`
}

// CurrentUserCreditUsageSeries totals one enterprise credit series.
type CurrentUserCreditUsageSeries struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Total float64 `json:"total"`
}

// DailyProductSurfaceUsage is one daily usage row.
type DailyProductSurfaceUsage struct {
	Attribution               []PersonalUsageAttribution        `json:"attribution,omitempty"`
	Date                      string                            `json:"date"`
	ProductSurfaceUsageValues map[string]float64                `json:"product_surface_usage_values"`
	PremiumUsageValues        *ProductSurfacePremiumUsageValues `json:"premium_usage_values,omitempty"`
	Models                    []ModelUsage                      `json:"models,omitempty"`
	Groups                    []ConsumptionUsageGroup           `json:"groups,omitempty"`
}

// DailyProductSurfaceUsageResponse contains daily product surface usage rows.
type DailyProductSurfaceUsageResponse struct {
	Data            []DailyProductSurfaceUsage `json:"data"`
	Units           *string                    `json:"units,omitempty"`
	DataFreshnessTS *string                    `json:"data_freshness_ts,omitempty"`
	GroupBy         *string                    `json:"group_by,omitempty"`
	BreakdownBy     []string                   `json:"breakdown_by,omitempty"`
}

// DailySkillUsageMetricsResponse contains daily skill usage metrics.
type DailySkillUsageMetricsResponse struct {
	Data            []DailySkillUsageOverview `json:"data"`
	DataFreshnessTS *string                   `json:"data_freshness_ts,omitempty"`
	GroupBy         *string                   `json:"group_by,omitempty"`
}

// DailySkillUsageOverview groups skill usage for one day.
type DailySkillUsageOverview struct {
	Date                string               `json:"date"`
	SkillUsageOverviews []SkillUsageOverview `json:"skill_usage_overviews"`
}

// DailyWorkspaceUsageCount is one daily workspace usage row.
type DailyWorkspaceUsageCount struct {
	Date    string                      `json:"date"`
	Totals  WorkspaceUsageCount         `json:"totals"`
	Clients []ClientWorkspaceUsageCount `json:"clients"`
	Models  []ModelUsage                `json:"models,omitempty"`
	Groups  []WorkspaceUsageGroup       `json:"groups,omitempty"`
}

// DailyWorkspaceUsageCountResponse contains daily workspace usage rows.
type DailyWorkspaceUsageCountResponse struct {
	Data               []DailyWorkspaceUsageCount `json:"data"`
	BalanceUnit        *string                    `json:"balance_unit,omitempty"`
	ActiveUsersSummary *ActiveUsersSummary        `json:"active_users_summary,omitempty"`
	GroupBy            *string                    `json:"group_by,omitempty"`
	BreakdownBy        []string                   `json:"breakdown_by,omitempty"`
}

// ModelActivitySummary counts activity for one model.
type ModelActivitySummary struct {
	Model   string `json:"model"`
	Users   int64  `json:"users"`
	Threads int64  `json:"threads"`
	Turns   int64  `json:"turns"`
}

// ModelUsage reports usage attributed to one model.
type ModelUsage struct {
	Model                   string   `json:"model"`
	Speed                   *string  `json:"speed,omitempty"`
	Credits                 float64  `json:"credits"`
	CostUSD                 *string  `json:"cost_usd,omitempty"`
	Users                   *int64   `json:"users,omitempty"`
	Threads                 *int64   `json:"threads,omitempty"`
	Turns                   *int64   `json:"turns,omitempty"`
	OnDemandCredits         *float64 `json:"on_demand_credits,omitempty"`
	UncachedTextInputTokens *int64   `json:"uncached_text_input_tokens,omitempty"`
	CachedTextInputTokens   *int64   `json:"cached_text_input_tokens,omitempty"`
	TextOutputTokens        *int64   `json:"text_output_tokens,omitempty"`
	TextTotalTokens         *int64   `json:"text_total_tokens,omitempty"`
	TotalTokens             *int64   `json:"total_tokens,omitempty"`
}

// PersonalUsageAttribution preserves the backend attribution dimensions.
type PersonalUsageAttribution struct {
	ThreadSource string  `json:"thread_source"`
	TurnTrigger  string  `json:"turn_trigger"`
	Model        string  `json:"model"`
	Surface      string  `json:"surface"`
	Value        float64 `json:"value"`
}

// PluginUsageBucket groups plugin usage for one day.
type PluginUsageBucket struct {
	Date                 string                `json:"date"`
	PluginUsageOverviews []PluginUsageOverview `json:"plugin_usage_overviews"`
}

// PluginUsageMetricsResponse contains daily plugin usage metrics.
type PluginUsageMetricsResponse struct {
	Data            []PluginUsageBucket `json:"data"`
	DataFreshnessTS *string             `json:"data_freshness_ts,omitempty"`
	GroupBy         *string             `json:"group_by,omitempty"`
}

// PluginUsageOverview counts invocations for one plugin.
type PluginUsageOverview struct {
	PluginID         *string `json:"plugin_id,omitempty"`
	PluginName       string  `json:"plugin_name"`
	DisplayName      string  `json:"display_name"`
	Marketplace      *string `json:"marketplace,omitempty"`
	InvocationCounts int64   `json:"invocation_counts"`
}

// ProductSurfacePremiumUsageValues groups premium usage by surface.
type ProductSurfacePremiumUsageValues struct {
	TotalUsageCredits                map[string]float64 `json:"total_usage_credits"`
	CreditUsageCredits               map[string]float64 `json:"credit_usage_credits"`
	UncachedTextInputTokensBySurface map[string]int64   `json:"uncached_text_input_tokens_by_surface"`
	CachedTextInputTokensBySurface   map[string]int64   `json:"cached_text_input_tokens_by_surface"`
	TextOutputTokensBySurface        map[string]int64   `json:"text_output_tokens_by_surface"`
	TextTotalTokensBySurface         map[string]int64   `json:"text_total_tokens_by_surface"`
	TotalTokensBySurface             map[string]int64   `json:"total_tokens_by_surface,omitempty"`
}

// SkillUsageOverview counts invocations for one skill.
type SkillUsageOverview struct {
	SkillName        string   `json:"skill_name"`
	DisplayName      string   `json:"display_name"`
	SkillIDs         []string `json:"skill_ids"`
	InvocationCounts int64    `json:"invocation_counts"`
}

// WorkspaceUsageCount totals workspace usage.
type WorkspaceUsageCount struct {
	Users                   int64    `json:"users"`
	Threads                 int64    `json:"threads"`
	Turns                   int64    `json:"turns"`
	Credits                 float64  `json:"credits"`
	CostUSD                 *string  `json:"cost_usd,omitempty"`
	OnDemandCredits         *float64 `json:"on_demand_credits,omitempty"`
	UncachedTextInputTokens *int64   `json:"uncached_text_input_tokens,omitempty"`
	CachedTextInputTokens   *int64   `json:"cached_text_input_tokens,omitempty"`
	TextOutputTokens        *int64   `json:"text_output_tokens,omitempty"`
	TextTotalTokens         *int64   `json:"text_total_tokens,omitempty"`
}

// WorkspaceUsageGroup reports workspace usage for one grouped row.
type WorkspaceUsageGroup struct {
	Dimensions map[string]string `json:"dimensions"`
	IsOther    *bool             `json:"is_other,omitempty"`
	Users      int64             `json:"users"`
	Turns      int64             `json:"turns"`
	CostUSD    *string           `json:"cost_usd,omitempty"`
}

var errInvalidAnalyticsResponse = errors.New("Invalid analytics response.")

// GetAccountAnalytics fetches one report for an inclusive UTC date range.
// Response bodies are not included in validation errors because they can
// contain billing details.
func (c *CloudClient) GetAccountAnalytics(ctx context.Context, report AnalyticsReport, startDate string, endDate string) (*AnalyticsResponse, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	route, query, err := analyticsRequest(report, startDate, endDate)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, c.apiPath(route...), query, nil, &raw); err != nil {
		var decodeErr *cloudResponseDecodeError
		if errors.As(err, &decodeErr) {
			return nil, errInvalidAnalyticsResponse
		}
		return nil, err
	}
	if err := validateAnalyticsResponse(report.Kind, raw); err != nil {
		return nil, errInvalidAnalyticsResponse
	}
	return decodeAnalyticsResponse(report.Kind, raw)
}

func analyticsRequest(report AnalyticsReport, startDate string, endDate string) ([]string, url.Values, error) {
	var route []string
	query := url.Values{}
	switch report.Kind {
	case AnalyticsReportUsage:
		route = []string{"usage", "daily-token-usage-breakdown"}
	case AnalyticsReportEnterpriseTokens:
		route = []string{"usage", "daily-workspace-user-token-usage-breakdown"}
	case AnalyticsReportCredits:
		route = []string{"usage", "credit-usage-events"}
	case AnalyticsReportWorkspaceCredits:
		route = []string{"usage", "daily-workspace-user-token-usage-breakdown"}
	case AnalyticsReportEnterpriseCredits:
		route = []string{"usage", "daily-workspace-user-credit-usage"}
	case AnalyticsReportMessages:
		route = []string{"analytics", "daily-workspace-usage-counts"}
	case AnalyticsReportPlugins:
		route = []string{"analytics", "daily-plugin-usage-metrics"}
	case AnalyticsReportSkills:
		route = []string{"analytics", "daily-skill-usage-metrics"}
	default:
		return nil, nil, fmt.Errorf("unsupported analytics report %q", report.Kind)
	}
	if report.Kind != AnalyticsReportCredits {
		query.Set("start_date", startDate)
		query.Set("end_date", endDate)
		if report.Kind == AnalyticsReportEnterpriseCredits {
			query.Set("breakdown", report.Breakdown)
		} else {
			query.Set("group_by", "day")
		}
	}
	switch report.Kind {
	case AnalyticsReportMessages, AnalyticsReportPlugins, AnalyticsReportSkills:
		query.Set("workspace_user", "true")
	}
	switch report.Kind {
	case AnalyticsReportEnterpriseTokens:
		query.Set("breakdown_by", "model")
		query.Add("modes", "codex")
		query.Add("modes", "work")
	case AnalyticsReportPlugins:
		query.Set("top_plugin_limit", strconv.Itoa(int(report.Limit)))
	case AnalyticsReportSkills:
		query.Set("top_skill_limit", strconv.Itoa(int(report.Limit)))
	}
	return route, query, nil
}

func decodeAnalyticsResponse(kind AnalyticsReportKind, raw json.RawMessage) (*AnalyticsResponse, error) {
	response := &AnalyticsResponse{Kind: kind}
	var err error
	switch kind {
	case AnalyticsReportUsage, AnalyticsReportEnterpriseTokens, AnalyticsReportWorkspaceCredits:
		response.Usage = &DailyProductSurfaceUsageResponse{}
		err = json.Unmarshal(raw, response.Usage)
	case AnalyticsReportCredits:
		response.Credits = &CreditUsageEventsResponse{}
		err = json.Unmarshal(raw, response.Credits)
	case AnalyticsReportEnterpriseCredits:
		response.EnterpriseCredits = &CurrentUserCreditUsageResponse{}
		err = json.Unmarshal(raw, response.EnterpriseCredits)
	case AnalyticsReportMessages:
		response.Messages = &DailyWorkspaceUsageCountResponse{}
		err = json.Unmarshal(raw, response.Messages)
	case AnalyticsReportPlugins:
		response.Plugins = &PluginUsageMetricsResponse{}
		err = json.Unmarshal(raw, response.Plugins)
	case AnalyticsReportSkills:
		response.Skills = &DailySkillUsageMetricsResponse{}
		err = json.Unmarshal(raw, response.Skills)
	default:
		return nil, fmt.Errorf("unsupported analytics report %q", kind)
	}
	if err != nil {
		return nil, errInvalidAnalyticsResponse
	}
	return response, nil
}

func validateAnalyticsResponse(kind AnalyticsReportKind, raw json.RawMessage) error {
	top, err := analyticsObject(raw)
	if err != nil {
		return err
	}
	data, err := analyticsArrayField(top, "data")
	if err != nil {
		return err
	}
	switch kind {
	case AnalyticsReportUsage, AnalyticsReportEnterpriseTokens, AnalyticsReportWorkspaceCredits:
		for _, item := range data {
			obj, err := analyticsObject(item)
			if err != nil {
				return err
			}
			if err := requireAnalyticsFields(obj, "date", "product_surface_usage_values"); err != nil {
				return err
			}
			if err := validateOptionalAnalyticsArray(obj, "attribution", []string{"thread_source", "turn_trigger", "model", "surface", "value"}); err != nil {
				return err
			}
			if err := validateOptionalAnalyticsArray(obj, "models", []string{"model", "credits"}); err != nil {
				return err
			}
			if err := validateOptionalAnalyticsArray(obj, "groups", []string{"dimensions", "credits"}); err != nil {
				return err
			}
		}
	case AnalyticsReportCredits:
		for _, item := range data {
			obj, err := analyticsObject(item)
			if err != nil {
				return err
			}
			if err := requireAnalyticsFields(obj, "date", "product_surface", "credit_amount"); err != nil {
				return err
			}
		}
	case AnalyticsReportEnterpriseCredits:
		if err := requireAnalyticsFields(top, "breakdown", "series"); err != nil {
			return err
		}
		for _, item := range data {
			obj, err := analyticsObject(item)
			if err != nil {
				return err
			}
			if err := requireAnalyticsFields(obj, "date", "values"); err != nil {
				return err
			}
		}
		series, err := analyticsArrayField(top, "series")
		if err != nil {
			return err
		}
		for _, item := range series {
			obj, err := analyticsObject(item)
			if err != nil {
				return err
			}
			if err := requireAnalyticsFields(obj, "key", "label", "total"); err != nil {
				return err
			}
		}
	case AnalyticsReportMessages:
		for _, item := range data {
			obj, err := analyticsObject(item)
			if err != nil {
				return err
			}
			if err := requireAnalyticsFields(obj, "date", "totals", "clients"); err != nil {
				return err
			}
			totals, err := analyticsObjectField(obj, "totals")
			if err != nil {
				return err
			}
			if err := requireAnalyticsFields(totals, "users", "threads", "turns", "credits"); err != nil {
				return err
			}
			if err := validateOptionalAnalyticsArray(obj, "models", []string{"model", "credits"}); err != nil {
				return err
			}
			if err := validateOptionalAnalyticsArray(obj, "groups", []string{"dimensions", "users", "turns"}); err != nil {
				return err
			}
			clients, err := analyticsArrayField(obj, "clients")
			if err != nil {
				return err
			}
			for _, client := range clients {
				clientObj, err := analyticsObject(client)
				if err != nil {
					return err
				}
				if err := requireAnalyticsFields(clientObj, "client_id", "users", "threads", "turns", "credits"); err != nil {
					return err
				}
			}
		}
	case AnalyticsReportPlugins:
		for _, item := range data {
			obj, err := analyticsObject(item)
			if err != nil {
				return err
			}
			if err := requireAnalyticsFields(obj, "date", "plugin_usage_overviews"); err != nil {
				return err
			}
			if err := validateAnalyticsArrayField(obj, "plugin_usage_overviews", []string{"plugin_name", "display_name", "invocation_counts"}); err != nil {
				return err
			}
		}
	case AnalyticsReportSkills:
		for _, item := range data {
			obj, err := analyticsObject(item)
			if err != nil {
				return err
			}
			if err := requireAnalyticsFields(obj, "date", "skill_usage_overviews"); err != nil {
				return err
			}
			if err := validateAnalyticsArrayField(obj, "skill_usage_overviews", []string{"skill_name", "display_name", "skill_ids", "invocation_counts"}); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported analytics report %q", kind)
	}
	return nil
}

func validateOptionalAnalyticsArray(obj map[string]json.RawMessage, key string, fields []string) error {
	value, ok := obj[key]
	if !ok || isJSONNull(value) {
		return nil
	}
	items, err := analyticsArray(value)
	if err != nil {
		return err
	}
	for _, item := range items {
		itemObj, err := analyticsObject(item)
		if err != nil {
			return err
		}
		if err := requireAnalyticsFields(itemObj, fields...); err != nil {
			return err
		}
	}
	return nil
}

func validateAnalyticsArrayField(obj map[string]json.RawMessage, key string, fields []string) error {
	items, err := analyticsArrayField(obj, key)
	if err != nil {
		return err
	}
	for _, item := range items {
		itemObj, err := analyticsObject(item)
		if err != nil {
			return err
		}
		if err := requireAnalyticsFields(itemObj, fields...); err != nil {
			return err
		}
	}
	return nil
}

func analyticsObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, errInvalidAnalyticsResponse
	}
	return obj, nil
}

func analyticsObjectField(obj map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	value, ok := obj[key]
	if !ok || isJSONNull(value) {
		return nil, errInvalidAnalyticsResponse
	}
	return analyticsObject(value)
}

func analyticsArray(raw json.RawMessage) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, errInvalidAnalyticsResponse
	}
	return items, nil
}

func analyticsArrayField(obj map[string]json.RawMessage, key string) ([]json.RawMessage, error) {
	value, ok := obj[key]
	if !ok || isJSONNull(value) {
		return nil, errInvalidAnalyticsResponse
	}
	return analyticsArray(value)
}

func requireAnalyticsFields(obj map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		value, ok := obj[field]
		if !ok || isJSONNull(value) {
			return errInvalidAnalyticsResponse
		}
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
