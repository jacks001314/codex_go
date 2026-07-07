package cli

import (
	"os"
	"runtime"
	"sort"
	"time"
)

type DoctorStatus string

const (
	DoctorStatusOK      DoctorStatus = "ok"
	DoctorStatusWarning DoctorStatus = "warning"
	DoctorStatusFail    DoctorStatus = "fail"
)

type DoctorIssue struct {
	Severity DoctorStatus `json:"severity"`
	Cause    string       `json:"cause"`
	Measured string       `json:"measured,omitempty"`
	Expected string       `json:"expected,omitempty"`
	Remedy   string       `json:"remedy,omitempty"`
	Fields   []string     `json:"fields,omitempty"`
}

type DoctorCheck struct {
	ID          string        `json:"id"`
	Category    string        `json:"category"`
	Status      DoctorStatus  `json:"status"`
	Summary     string        `json:"summary"`
	Details     []string      `json:"details,omitempty"`
	Issues      []DoctorIssue `json:"issues,omitempty"`
	Remediation string        `json:"remediation,omitempty"`
	Duration    time.Duration `json:"duration"`
}

type DoctorReport struct {
	SchemaVersion int           `json:"schemaVersion"`
	GeneratedAt   time.Time     `json:"generatedAt"`
	OverallStatus DoctorStatus  `json:"overallStatus"`
	CodexVersion  string        `json:"codexVersion"`
	Checks        []DoctorCheck `json:"checks"`
}

type DoctorCheckFunc func() DoctorCheck

func RunDoctor(version string, checks ...DoctorCheckFunc) *DoctorReport {
	out := make([]DoctorCheck, 0, len(checks))
	for _, checkFunc := range checks {
		started := time.Now()
		check := checkFunc()
		if check.Duration == 0 {
			check.Duration = time.Since(started)
		}
		out = append(out, check)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].ID < out[j].ID
	})
	return &DoctorReport{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC(),
		OverallStatus: DoctorOverallStatus(out),
		CodexVersion:  version,
		Checks:        out,
	}
}

func DoctorOverallStatus(checks []DoctorCheck) DoctorStatus {
	status := DoctorStatusOK
	for _, check := range checks {
		if check.Status == DoctorStatusFail {
			return DoctorStatusFail
		}
		if check.Status == DoctorStatusWarning {
			status = DoctorStatusWarning
		}
	}
	return status
}

func DoctorSystemCheck() DoctorCheck {
	return DoctorCheck{
		ID:       "system.runtime",
		Category: "system",
		Status:   DoctorStatusOK,
		Summary:  runtime.GOOS + "/" + runtime.GOARCH,
		Details:  []string{"go: " + runtime.Version()},
	}
}

func DoctorEnvironmentCheck(required []string) DoctorCheck {
	missing := []string{}
	for _, name := range required {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return DoctorCheck{ID: "environment.required", Category: "environment", Status: DoctorStatusOK, Summary: "required environment is present"}
	}
	return DoctorCheck{
		ID:       "environment.required",
		Category: "environment",
		Status:   DoctorStatusWarning,
		Summary:  "required environment variables are missing",
		Issues: []DoctorIssue{{
			Severity: DoctorStatusWarning,
			Cause:    "missing environment variables",
			Expected: "present",
			Fields:   missing,
		}},
	}
}
