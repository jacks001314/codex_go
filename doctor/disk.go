package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Rust 6efcdad4c3 (#38795): storage diagnostics. GCODE_HOME and the active
// worktree free space is reported with a warning below 5 GiB and a failure
// below 1 GiB.
const (
	diskGiB             = uint64(1024 * 1024 * 1024)
	diskFailThreshold   = diskGiB
	diskWarningLimit    = 5 * diskGiB
	diskMiB             = uint64(1024 * 1024)
)

// diskCheck reports available space for GCODE_HOME and the active worktree.
func diskCheck(codexHome string, cwd string) *DoctorCheck {
	return diskCheckWithMeasure(codexHome, cwd, availableDiskSpace)
}

func diskCheckWithMeasure(codexHome string, cwd string, measure func(string) (uint64, bool)) *DoctorCheck {
	check := NewCheck("system.disk", "disk", CheckStatusOK, "sufficient free disk space").
		Detail("warning threshold: 5.0 GiB").
		Detail("failure threshold: 1.0 GiB")
	var lowest *uint64
	paths := []struct {
		label string
		path  string
	}{
		{"GCODE_HOME", codexHome},
		{"worktree", cwd},
	}
	for _, entry := range paths {
		field := entry.label + " available"
		path := strings.TrimSpace(entry.path)
		if path == "" {
			check.Status = maxCheckStatus(check.Status, CheckStatusWarning)
			check.Detail(field + ": unavailable (not found)")
			check.Issue(NewIssue(CheckStatusWarning, "disk space for "+entry.label+" could not be checked").
				WithMeasured("not found").
				WithExpected("readable filesystem capacity").
				WithRemedy("Check filesystem access and available disk space.").
				WithField(field))
			continue
		}
		if nearest, ok := nearestExistingDir(path); ok {
			path = nearest
		}
		available, ok := measure(path)
		if !ok {
			check.Status = maxCheckStatus(check.Status, CheckStatusWarning)
			check.Detail(field + ": unavailable (measurement failed)")
			check.Issue(NewIssue(CheckStatusWarning, "disk space for "+entry.label+" could not be checked").
				WithMeasured("unavailable").
				WithExpected("readable filesystem capacity").
				WithRemedy("Check filesystem access and available disk space.").
				WithField(field))
			continue
		}
		measured := formatDiskCapacity(available)
		check.Detail(fmt.Sprintf("%s: %s", field, measured))
		if lowest == nil || available < *lowest {
			value := available
			lowest = &value
		}
		status := CheckStatusOK
		switch {
		case available < diskFailThreshold:
			status = CheckStatusFail
		case available < diskWarningLimit:
			status = CheckStatusWarning
		}
		if status != CheckStatusOK {
			check.Status = maxCheckStatus(check.Status, status)
			check.Issue(NewIssue(status, entry.label+" has insufficient disk space").
				WithMeasured(measured).
				WithExpected("at least 5.0 GiB").
				WithRemedy("Free disk space or move the worktree to a larger volume.").
				WithField(field))
		}
	}
	check.Summary = diskSummary(check.Status, lowest)
	return check
}

func diskSummary(status CheckStatus, lowest *uint64) string {
	switch {
	case status == CheckStatusFail && lowest != nil:
		return "critically low disk space (" + formatDiskCapacity(*lowest) + ")"
	case status == CheckStatusWarning && lowest != nil && *lowest < diskWarningLimit:
		return "low disk space (" + formatDiskCapacity(*lowest) + ")"
	case status == CheckStatusWarning:
		return "disk capacity could not be fully verified"
	case status == CheckStatusOK && lowest != nil:
		return "sufficient free disk space (" + formatDiskCapacity(*lowest) + ")"
	default:
		return "disk capacity could not be verified"
	}
}

func formatDiskCapacity(bytes uint64) string {
	if bytes >= diskGiB {
		return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(diskGiB))
	}
	return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(diskMiB))
}

// nearestExistingDir walks up from path until an existing directory is found,
// mirroring Rust's ancestors().find(is_dir) in the disk check.
func nearestExistingDir(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = filepath.Clean(path)
	}
	for {
		if info, err := os.Stat(absolute); err == nil && info.IsDir() {
			return absolute, true
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", false
		}
		absolute = parent
	}
}

// availableDiskSpace reports free bytes on the volume containing path.
// Implemented per platform in disk_unix.go / disk_windows.go.
var availableDiskSpace = platformAvailableDiskSpace
