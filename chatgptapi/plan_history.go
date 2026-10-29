package chatgptapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

// PlanLimitDimension identifies one supported breakdown dimension. Server
// dimensions that this client does not know are reported as
// PlanLimitDimensionUnknown instead of being dropped (Rust #45766).
type PlanLimitDimension string

const (
	PlanLimitDimensionThreadSource PlanLimitDimension = "thread_source"
	PlanLimitDimensionTurnTrigger  PlanLimitDimension = "turn_trigger"
	PlanLimitDimensionModel        PlanLimitDimension = "model"
	PlanLimitDimensionSurface      PlanLimitDimension = "surface"
	PlanLimitDimensionUnknown      PlanLimitDimension = "unknown"
)

// UnmarshalJSON maps every unrecognized dimension to Unknown so additive
// server dimensions keep their rows.
func (d *PlanLimitDimension) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch PlanLimitDimension(raw) {
	case PlanLimitDimensionThreadSource, PlanLimitDimensionTurnTrigger,
		PlanLimitDimensionModel, PlanLimitDimensionSurface:
		*d = PlanLimitDimension(raw)
	default:
		*d = PlanLimitDimensionUnknown
	}
	return nil
}

// PlanLimitValue is one breakdown row, measured in hundredths of a percent of
// the period's historical allowance.
type PlanLimitValue struct {
	Key         string  `json:"key"`
	BasisPoints float64 `json:"basis_points"`
}

// PlanLimitBreakdown groups breakdown rows by dimension.
type PlanLimitBreakdown struct {
	Dimension PlanLimitDimension `json:"dimension"`
	Rows      []PlanLimitValue   `json:"rows"`
}

// PlanLimitPeriod is one historical allowance window.
type PlanLimitPeriod struct {
	ID                 string               `json:"id"`
	WindowMinutes      uint32               `json:"window_minutes"`
	PlanType           string               `json:"plan_type"`
	StartsAt           string               `json:"starts_at"`
	EndsAt             string               `json:"ends_at"`
	AccountingComplete bool                 `json:"accounting_complete"`
	UsedBasisPoints    *float64             `json:"used_basis_points"`
	Breakdowns         []PlanLimitBreakdown `json:"breakdowns"`
}

// PlanLimitHistory is seven days of allowance snapshots. Approximate defaults
// to true when the server omits it (Rust approximate_by_default).
type PlanLimitHistory struct {
	DataAsOf                 *string           `json:"data_as_of"`
	CoverageStart            *string           `json:"coverage_start"`
	CoverageComplete         bool              `json:"coverage_complete"`
	Approximate              *bool             `json:"approximate"`
	BoundaryToleranceSeconds *uint32           `json:"boundary_tolerance_seconds"`
	Periods                  []PlanLimitPeriod `json:"periods"`
}

var errInvalidPlanHistoryResponse = errors.New("Invalid plan history response.")

// ApproximateOrDefault reports the server's approximate flag, defaulting to
// true when it was omitted.
func (h *PlanLimitHistory) ApproximateOrDefault() bool {
	if h == nil || h.Approximate == nil {
		return true
	}
	return *h.Approximate
}

// GetPlanLimitHistory fetches seven days of allowance snapshots. An undeployed
// route has no report, so a 404 returns (nil, nil).
func (c *CloudClient) GetPlanLimitHistory(ctx context.Context) (*PlanLimitHistory, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	var history PlanLimitHistory
	query := url.Values{"days": []string{"7"}}
	err := c.doJSON(ctx, http.MethodGet, c.apiPath("usage", "plan_limit_history"), query, nil, &history)
	if err != nil {
		var statusErr *HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		var decodeErr *cloudResponseDecodeError
		if errors.As(err, &decodeErr) {
			return nil, errInvalidPlanHistoryResponse
		}
		return nil, err
	}
	return &history, nil
}
