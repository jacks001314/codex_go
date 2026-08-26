package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	FeedbackDoctorReportTimeout  = 25 * time.Second
	FeedbackMaxDoctorTagValueLen = 256
)

type FeedbackDoctorReport struct {
	Attachment FeedbackAttachment
	Tags       map[string]string
}

type FeedbackDoctorReportOptions struct {
	Executable string
	Timeout    time.Duration
}

func FeedbackDoctorReportFromExecutable(ctx context.Context, options *FeedbackDoctorReportOptions) (*FeedbackDoctorReport, error) {
	if options == nil {
		options = &FeedbackDoctorReportOptions{}
	}
	executable := strings.TrimSpace(options.Executable)
	if executable == "" {
		path, err := os.Executable()
		if err != nil {
			return nil, err
		}
		executable = path
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = FeedbackDoctorReportTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := runCommandCombinedOutput(exec.CommandContext(ctx, executable, "doctor", "--json", "--feedback"))
	if err != nil && len(output) == 0 {
		return nil, err
	}
	return ParseFeedbackDoctorReport(output)
}

func ParseFeedbackDoctorReport(stdout []byte) (*FeedbackDoctorReport, error) {
	start := bytes.IndexByte(stdout, '{')
	if start < 0 {
		return nil, errNoDoctorJSON
	}
	raw := bytes.TrimSpace(stdout[start:])
	var report any
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, err
	}
	pretty, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		pretty = append([]byte(nil), raw...)
	}
	return &FeedbackDoctorReport{
		Attachment: FeedbackAttachment{
			Filename:    FeedbackDoctorReportAttachmentFilename,
			ContentType: "application/json",
			Buffer:      pretty,
		},
		Tags: FeedbackDoctorReportTags(report),
	}, nil
}

var errNoDoctorJSON = &doctorReportError{message: "doctor report did not produce JSON"}

type doctorReportError struct {
	message string
}

func (e *doctorReportError) Error() string {
	return e.message
}

func FeedbackDoctorReportTags(report any) map[string]string {
	tags := map[string]string{}
	object, ok := report.(map[string]any)
	if !ok {
		return map[string]string{
			"doctor_ok_count":      "0",
			"doctor_warning_count": "0",
			"doctor_fail_count":    "0",
		}
	}
	if overallStatus, ok := object["overallStatus"].(string); ok {
		tags["doctor_overall_status"] = TruncateFeedbackDoctorTagValue(overallStatus)
	}
	okCount := 0
	warningCount := 0
	failCount := 0
	var warningChecks []string
	var failedChecks []string
	for _, check := range FeedbackDoctorCheckValues(object["checks"]) {
		status, _ := check["status"].(string)
		id, _ := check["id"].(string)
		if id == "" {
			id = "unknown"
		}
		switch status {
		case "ok":
			okCount++
		case "warning":
			warningCount++
			warningChecks = append(warningChecks, id)
		case "fail":
			failCount++
			failedChecks = append(failedChecks, id)
		}
	}
	tags["doctor_ok_count"] = strconv.Itoa(okCount)
	tags["doctor_warning_count"] = strconv.Itoa(warningCount)
	tags["doctor_fail_count"] = strconv.Itoa(failCount)
	if len(failedChecks) > 0 {
		tags["doctor_failed_checks"] = TruncateFeedbackDoctorTagValue(strings.Join(failedChecks, ","))
	}
	if len(warningChecks) > 0 {
		tags["doctor_warning_checks"] = TruncateFeedbackDoctorTagValue(strings.Join(warningChecks, ","))
	}
	return tags
}

func FeedbackDoctorCheckValues(checks any) []map[string]any {
	switch values := checks.(type) {
	case []any:
		out := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if check, ok := value.(map[string]any); ok {
				out = append(out, check)
			}
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]map[string]any, 0, len(values))
		for _, key := range keys {
			if check, ok := values[key].(map[string]any); ok {
				out = append(out, check)
			}
		}
		return out
	default:
		return nil
	}
}

func TruncateFeedbackDoctorTagValue(value string) string {
	if len([]rune(value)) <= FeedbackMaxDoctorTagValueLen {
		return value
	}
	runes := []rune(value)
	return string(runes[:FeedbackMaxDoctorTagValueLen-3]) + "..."
}
