package codexapi

import "encoding/json"

type SearchRequest struct {
	ID              string          `json:"id"`
	Model           string          `json:"model"`
	Reasoning       any             `json:"reasoning,omitempty"`
	Input           any             `json:"input,omitempty"`
	Commands        *SearchCommands `json:"commands,omitempty"`
	Settings        *SearchSettings `json:"settings,omitempty"`
	MaxOutputTokens *uint64         `json:"max_output_tokens,omitempty"`
}

type SearchCommands struct {
	SearchQuery    []SearchQuery         `json:"search_query,omitempty"`
	ImageQuery     []SearchQuery         `json:"image_query,omitempty"`
	Open           []OpenOperation       `json:"open,omitempty"`
	Click          []ClickOperation      `json:"click,omitempty"`
	Find           []FindOperation       `json:"find,omitempty"`
	Screenshot     []ScreenshotOperation `json:"screenshot,omitempty"`
	Finance        []FinanceOperation    `json:"finance,omitempty"`
	Weather        []WeatherOperation    `json:"weather,omitempty"`
	Sports         []SportsOperation     `json:"sports,omitempty"`
	Time           []TimeOperation       `json:"time,omitempty"`
	ResponseLength *SearchResponseLength `json:"response_length,omitempty"`
}

type SearchQuery struct {
	Q       string   `json:"q"`
	Recency *uint64  `json:"recency,omitempty"`
	Domains []string `json:"domains,omitempty"`
}

type OpenOperation struct {
	RefID  string  `json:"ref_id"`
	Lineno *uint64 `json:"lineno,omitempty"`
}

type ClickOperation struct {
	RefID string `json:"ref_id"`
	ID    uint64 `json:"id"`
}

type FindOperation struct {
	RefID   string `json:"ref_id"`
	Pattern string `json:"pattern"`
}

type ScreenshotOperation struct {
	RefID  string `json:"ref_id"`
	Pageno uint64 `json:"pageno"`
}

type FinanceOperation struct {
	Ticker string           `json:"ticker"`
	Type   FinanceAssetType `json:"type"`
	Market *string          `json:"market,omitempty"`
}

type FinanceAssetType string

const (
	FinanceEquity FinanceAssetType = "equity"
	FinanceFund   FinanceAssetType = "fund"
	FinanceCrypto FinanceAssetType = "crypto"
	FinanceIndex  FinanceAssetType = "index"
)

type WeatherOperation struct {
	Location string  `json:"location"`
	Start    *string `json:"start,omitempty"`
	Duration *uint64 `json:"duration,omitempty"`
}

type SportsOperation struct {
	Tool     *SportsToolName `json:"tool,omitempty"`
	Fn       SportsFunction  `json:"fn"`
	League   SportsLeague    `json:"league"`
	Team     *string         `json:"team,omitempty"`
	Opponent *string         `json:"opponent,omitempty"`
	DateFrom *string         `json:"date_from,omitempty"`
	DateTo   *string         `json:"date_to,omitempty"`
	NumGames *uint64         `json:"num_games,omitempty"`
	Locale   *string         `json:"locale,omitempty"`
}

type SportsToolName string

const SportsTool SportsToolName = "sports"

type SportsFunction string

const (
	SportsSchedule  SportsFunction = "schedule"
	SportsStandings SportsFunction = "standings"
)

type SportsLeague string

const (
	SportsNBA    SportsLeague = "nba"
	SportsWNBA   SportsLeague = "wnba"
	SportsNFL    SportsLeague = "nfl"
	SportsNHL    SportsLeague = "nhl"
	SportsMLB    SportsLeague = "mlb"
	SportsEPL    SportsLeague = "epl"
	SportsNCAAMB SportsLeague = "ncaamb"
	SportsNCAAWB SportsLeague = "ncaawb"
	SportsIPL    SportsLeague = "ipl"
)

type TimeOperation struct {
	UTCOffset string `json:"utc_offset"`
}

type SearchResponseLength string

const (
	SearchShort  SearchResponseLength = "short"
	SearchMedium SearchResponseLength = "medium"
	SearchLong   SearchResponseLength = "long"
)

type ExternalWebAccessMode string

const (
	ExternalWebCached  ExternalWebAccessMode = "cached"
	ExternalWebIndexed ExternalWebAccessMode = "indexed"
	ExternalWebLive    ExternalWebAccessMode = "live"
)

type ExternalWebAccess struct {
	Boolean *bool
	Mode    *ExternalWebAccessMode
}

func (a *ExternalWebAccess) MarshalJSON() ([]byte, error) {
	if a == nil {
		return []byte("null"), nil
	}
	if a.Boolean != nil {
		return json.Marshal(*a.Boolean)
	}
	if a.Mode != nil {
		return json.Marshal(*a.Mode)
	}
	return []byte("null"), nil
}

func (a *ExternalWebAccess) UnmarshalJSON(data []byte) error {
	var boolean bool
	if err := json.Unmarshal(data, &boolean); err == nil {
		a.Boolean = &boolean
		a.Mode = nil
		return nil
	}
	var mode ExternalWebAccessMode
	if err := json.Unmarshal(data, &mode); err != nil {
		return err
	}
	a.Boolean = nil
	a.Mode = &mode
	return nil
}

type SearchSettings struct {
	UserLocation      *ApproximateLocation `json:"user_location,omitempty"`
	SearchContextSize *SearchContextSize   `json:"search_context_size,omitempty"`
	Filters           *SearchFilters       `json:"filters,omitempty"`
	ImageSettings     *SearchImageSettings `json:"image_settings,omitempty"`
	AllowedCallers    []AllowedCaller      `json:"allowed_callers,omitempty"`
	ExternalWebAccess *ExternalWebAccess   `json:"external_web_access,omitempty"`
}

type ApproximateLocation struct {
	Type     LocationType `json:"type"`
	Country  *string      `json:"country,omitempty"`
	Region   *string      `json:"region,omitempty"`
	City     *string      `json:"city,omitempty"`
	Timezone *string      `json:"timezone,omitempty"`
}

type LocationType string

const LocationApproximate LocationType = "approximate"

type SearchContextSize string

const (
	SearchContextLow    SearchContextSize = "low"
	SearchContextMedium SearchContextSize = "medium"
	SearchContextHigh   SearchContextSize = "high"
)

type SearchFilters struct {
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

type SearchImageSettings struct {
	MaxResults *uint64 `json:"max_results,omitempty"`
	Caption    *bool   `json:"caption,omitempty"`
}

type AllowedCaller string

const (
	AllowedCallerDirect          AllowedCaller = "direct"
	AllowedCallerShell           AllowedCaller = "shell"
	AllowedCallerCodeInterpreter AllowedCaller = "code_interpreter"
)

type SearchResponse struct {
	EncryptedOutput *string `json:"encrypted_output,omitempty"`
	Output          string  `json:"output"`
	Results         []any   `json:"results,omitempty"`
}
