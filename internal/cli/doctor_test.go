package cli

import "testing"

func TestOverallStatus(t *testing.T) {
	if DoctorOverallStatus([]DoctorCheck{{Status: DoctorStatusOK}, {Status: DoctorStatusWarning}}) != DoctorStatusWarning {
		t.Fatalf("warning should dominate ok")
	}
	if DoctorOverallStatus([]DoctorCheck{{Status: DoctorStatusWarning}, {Status: DoctorStatusFail}}) != DoctorStatusFail {
		t.Fatalf("fail should dominate warning")
	}
}

func TestRunSortsChecksAndComputesOverall(t *testing.T) {
	report := RunDoctor("dev",
		func() DoctorCheck { return DoctorCheck{ID: "b", Category: "z", Status: DoctorStatusOK} },
		func() DoctorCheck { return DoctorCheck{ID: "a", Category: "a", Status: DoctorStatusWarning} },
	)
	if report.CodexVersion != "dev" || report.OverallStatus != DoctorStatusWarning {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.Checks[0].ID != "a" {
		t.Fatalf("checks were not sorted: %#v", report.Checks)
	}
}

func TestEnvironmentCheckReportsMissingVariables(t *testing.T) {
	t.Setenv("PRESENT", "1")
	check := DoctorEnvironmentCheck([]string{"PRESENT", "MISSING"})
	if check.Status != DoctorStatusWarning || len(check.Issues) != 1 || check.Issues[0].Fields[0] != "MISSING" {
		t.Fatalf("unexpected check: %#v", check)
	}
}
