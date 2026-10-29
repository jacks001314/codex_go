package chatgptapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Consumer task accounting: current allowance percentages and actual balance
// debits. This contract is deliberately separate from enterprise estimated
// lifetime credits (Rust #45768, backend-client task_usage.rs).

const (
	taskUsageTaskLimit     = 100
	taskUsageThreadLimit   = 1000
	taskUsageIDLimit       = 512
	taskUsageCreditsLength = 128
)

const (
	taskUsageRequestMessage  = "Expected at most 100 disjoint tasks and 1,000 thread IDs."
	taskUsageResponseMessage = "Invalid task usage response."
)

// TaskUsageStatus reports how complete a task's accounting is.
type TaskUsageStatus string

const (
	TaskUsageStatusAvailable   TaskUsageStatus = "available"
	TaskUsageStatusPartial     TaskUsageStatus = "partial"
	TaskUsageStatusUnavailable TaskUsageStatus = "unavailable"
)

// TaskUsageThread is one root task plus the descendants that share its
// allowance.
type TaskUsageThread struct {
	ThreadID string `json:"thread_id"`
	// CreatedAt is an RFC 3339 timestamp when the runtime knows it; nil is
	// sent as an explicit null.
	CreatedAt           *string  `json:"created_at"`
	DescendantThreadIDs []string `json:"descendant_thread_ids"`
}

// TaskUsageCredits keeps the backend's decimal string verbatim so both
// rendering and sorting retain every digit.
type TaskUsageCredits struct {
	value string
}

// ParseTaskUsageCredits validates a decimal credits string.
func ParseTaskUsageCredits(value string) (TaskUsageCredits, error) {
	if len(value) > taskUsageCreditsLength {
		return TaskUsageCredits{}, errors.New("Invalid decimal credits.")
	}
	mantissa := value
	exponent := "0"
	if at := strings.IndexAny(value, "eE"); at >= 0 {
		mantissa = value[:at]
		exponent = value[at+1:]
	}
	unsigned := strings.TrimLeft(mantissa, "-+")
	if unsigned == "" {
		return TaskUsageCredits{}, errors.New("Invalid decimal credits.")
	}
	digits := false
	for index := 0; index < len(unsigned); index++ {
		character := unsigned[index]
		switch {
		case character >= '0' && character <= '9':
			digits = true
		case character == '.':
		default:
			return TaskUsageCredits{}, errors.New("Invalid decimal credits.")
		}
	}
	if !digits {
		return TaskUsageCredits{}, errors.New("Invalid decimal credits.")
	}
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return TaskUsageCredits{}, errors.New("Invalid decimal credits.")
	}
	if _, err := strconv.ParseInt(exponent, 10, 32); err != nil {
		return TaskUsageCredits{}, errors.New("Invalid decimal credits.")
	}
	return TaskUsageCredits{value: value}, nil
}

// String returns the backend's original decimal text.
func (c TaskUsageCredits) String() string {
	return c.value
}

// Compare orders credits by exact decimal value. It returns -1, 0, or 1 and
// treats every spelling of zero as equal.
func (c TaskUsageCredits) Compare(other TaskUsageCredits) int {
	negative, power, digits := c.parts()
	otherNegative, otherPower, otherDigits := other.parts()
	var magnitude int
	switch {
	case digits == "" && otherDigits == "":
		magnitude = 0
	case digits == "":
		magnitude = -1
	case otherDigits == "":
		magnitude = 1
	default:
		magnitude = compareInt64(power, otherPower)
		if magnitude == 0 {
			width := len(digits)
			if len(otherDigits) > width {
				width = len(otherDigits)
			}
			left := digits + strings.Repeat("0", width-len(digits))
			right := otherDigits + strings.Repeat("0", width-len(otherDigits))
			magnitude = strings.Compare(left, right)
		}
	}
	// A negative magnitude only matters when both sides share a sign; a
	// negative value always sorts below a non-negative one.
	switch {
	case otherNegative != negative:
		if otherNegative {
			return 1
		}
		return -1
	case negative:
		return -magnitude
	default:
		return magnitude
	}
}

// Less reports whether c sorts before other.
func (c TaskUsageCredits) Less(other TaskUsageCredits) bool {
	return c.Compare(other) < 0
}

// parts decomposes the decimal into sign, exponent, and significant digits.
func (c TaskUsageCredits) parts() (bool, int64, string) {
	mantissa := c.value
	exponent := "0"
	if at := strings.IndexAny(c.value, "eE"); at >= 0 {
		mantissa = c.value[:at]
		exponent = c.value[at+1:]
	}
	negative := strings.HasPrefix(mantissa, "-")
	unsigned := strings.TrimLeft(mantissa, "-+")
	fraction := 0
	if at := strings.Index(unsigned, "."); at >= 0 {
		fraction = len(unsigned) - at - 1
	}
	digits := strings.TrimLeft(strings.ReplaceAll(unsigned, ".", ""), "0")
	parsed, _ := strconv.ParseInt(exponent, 10, 64)
	power := parsed - int64(fraction) + int64(len(digits))
	return negative && digits != "", power, digits
}

func compareInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// TaskUsageAmounts is one accounting bucket: percentages of the current full
// limits plus exact balance debits.
type TaskUsageAmounts struct {
	FiveHourLimitPercent *float64
	WeeklyLimitPercent   *float64
	BalanceUsageCredits  *TaskUsageCredits
}

// TaskUsageGroup is one model/speed/experience bucket of a task's usage.
type TaskUsageGroup struct {
	ProductExperience *string
	Model             *string
	ReasoningEffort   *string
	Speed             *string
	Amounts           TaskUsageAmounts
}

// TaskUsage is the consumer accounting for one root task.
type TaskUsage struct {
	ThreadID    string
	DataStatus  TaskUsageStatus
	UsageSource string
	Amounts     TaskUsageAmounts
	Groups      []TaskUsageGroup
}

// TaskUsageResponse is one query_v2 page plus its freshness timestamp.
type TaskUsageResponse struct {
	DataAsOf *string
	Threads  []TaskUsage
}

type taskUsageQueryRequest struct {
	Threads []TaskUsageThread `json:"threads"`
}

type taskUsageAmountsWire struct {
	FiveHourLimitPercent *float64 `json:"five_hour_limit_percent"`
	WeeklyLimitPercent   *float64 `json:"weekly_limit_percent"`
	BalanceUsageCredits  *string  `json:"balance_usage_credits"`
}

type taskUsageGroupWire struct {
	ProductExperience    *string  `json:"product_experience"`
	Model                *string  `json:"model"`
	ReasoningEffort      *string  `json:"reasoning_effort"`
	Speed                *string  `json:"speed"`
	FiveHourLimitPercent *float64 `json:"five_hour_limit_percent"`
	WeeklyLimitPercent   *float64 `json:"weekly_limit_percent"`
	BalanceUsageCredits  *string  `json:"balance_usage_credits"`
}

type taskUsageWire struct {
	ThreadID             string                `json:"thread_id"`
	DataStatus           TaskUsageStatus       `json:"data_status"`
	UsageSource          string                `json:"usage_source"`
	FiveHourLimitPercent *float64              `json:"five_hour_limit_percent"`
	WeeklyLimitPercent   *float64              `json:"weekly_limit_percent"`
	BalanceUsageCredits  *string               `json:"balance_usage_credits"`
	Groups               *[]taskUsageGroupWire `json:"groups"`
}

type taskUsageResponseWire struct {
	DataAsOf *string          `json:"data_as_of"`
	Threads  *[]taskUsageWire `json:"threads"`
}

// GetTaskUsage queries disjoint root tasks, including their legacy
// descendants, without selecting a comparison plan (Rust get_task_usage).
func (c *CloudClient) GetTaskUsage(ctx context.Context, threads []TaskUsageThread) (*TaskUsageResponse, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	if len(threads) == 0 || len(threads) > taskUsageTaskLimit {
		return nil, errors.New(taskUsageRequestMessage)
	}
	seen := make(map[string]struct{})
	for _, thread := range threads {
		ids := make([]string, 0, 1+len(thread.DescendantThreadIDs))
		ids = append(ids, thread.ThreadID)
		ids = append(ids, thread.DescendantThreadIDs...)
		for _, id := range ids {
			if strings.TrimSpace(id) == "" || len(id) > taskUsageIDLimit {
				return nil, errors.New(taskUsageRequestMessage)
			}
			if _, duplicate := seen[id]; duplicate {
				return nil, errors.New(taskUsageRequestMessage)
			}
			seen[id] = struct{}{}
		}
	}
	if len(seen) > taskUsageThreadLimit {
		return nil, errors.New(taskUsageRequestMessage)
	}
	var wire taskUsageResponseWire
	if err := c.doJSON(ctx, http.MethodPost, c.apiPath("usage", "thread_usage", "query_v2"), nil,
		taskUsageQueryRequest{Threads: threads}, &wire); err != nil {
		var decodeErr *cloudResponseDecodeError
		if errors.As(err, &decodeErr) {
			return nil, errors.New(taskUsageResponseMessage)
		}
		return nil, err
	}
	if wire.Threads == nil {
		return nil, errors.New(taskUsageResponseMessage)
	}
	roots := make(map[string]struct{}, len(threads))
	for _, thread := range threads {
		roots[thread.ThreadID] = struct{}{}
	}
	response := &TaskUsageResponse{
		DataAsOf: wire.DataAsOf,
		Threads:  make([]TaskUsage, 0, len(*wire.Threads)),
	}
	returned := make(map[string]struct{}, len(*wire.Threads))
	for _, row := range *wire.Threads {
		if _, ok := roots[row.ThreadID]; !ok {
			return nil, errors.New(taskUsageResponseMessage)
		}
		if _, duplicate := returned[row.ThreadID]; duplicate {
			return nil, errors.New(taskUsageResponseMessage)
		}
		returned[row.ThreadID] = struct{}{}
		switch row.DataStatus {
		case TaskUsageStatusAvailable, TaskUsageStatusPartial, TaskUsageStatusUnavailable:
		default:
			return nil, errors.New(taskUsageResponseMessage)
		}
		if row.Groups == nil {
			return nil, errors.New(taskUsageResponseMessage)
		}
		amounts, err := taskUsageAmountsFromWire(taskUsageAmountsWire{
			FiveHourLimitPercent: row.FiveHourLimitPercent,
			WeeklyLimitPercent:   row.WeeklyLimitPercent,
			BalanceUsageCredits:  row.BalanceUsageCredits,
		})
		if err != nil {
			return nil, errors.New(taskUsageResponseMessage)
		}
		groups := make([]TaskUsageGroup, 0, len(*row.Groups))
		for _, group := range *row.Groups {
			groupAmounts, err := taskUsageAmountsFromWire(taskUsageAmountsWire{
				FiveHourLimitPercent: group.FiveHourLimitPercent,
				WeeklyLimitPercent:   group.WeeklyLimitPercent,
				BalanceUsageCredits:  group.BalanceUsageCredits,
			})
			if err != nil {
				return nil, errors.New(taskUsageResponseMessage)
			}
			groups = append(groups, TaskUsageGroup{
				ProductExperience: group.ProductExperience,
				Model:             group.Model,
				ReasoningEffort:   group.ReasoningEffort,
				Speed:             group.Speed,
				Amounts:           groupAmounts,
			})
		}
		response.Threads = append(response.Threads, TaskUsage{
			ThreadID:    row.ThreadID,
			DataStatus:  row.DataStatus,
			UsageSource: row.UsageSource,
			Amounts:     amounts,
			Groups:      groups,
		})
	}
	return response, nil
}

func taskUsageAmountsFromWire(wire taskUsageAmountsWire) (TaskUsageAmounts, error) {
	amounts := TaskUsageAmounts{
		FiveHourLimitPercent: wire.FiveHourLimitPercent,
		WeeklyLimitPercent:   wire.WeeklyLimitPercent,
	}
	if !taskUsagePercentValid(amounts.FiveHourLimitPercent) || !taskUsagePercentValid(amounts.WeeklyLimitPercent) {
		return TaskUsageAmounts{}, errors.New(taskUsageResponseMessage)
	}
	if wire.BalanceUsageCredits != nil {
		credits, err := ParseTaskUsageCredits(*wire.BalanceUsageCredits)
		if err != nil {
			return TaskUsageAmounts{}, err
		}
		amounts.BalanceUsageCredits = &credits
	}
	return amounts, nil
}

func taskUsagePercentValid(value *float64) bool {
	return value == nil || (!math.IsNaN(*value) && !math.IsInf(*value, 0))
}
