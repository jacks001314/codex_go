package windowssandbox

import "fmt"

const (
	WFPSetupSuccessMetric = "codex.windows_sandbox.wfp_setup_success"
	WFPSetupFailureMetric = "codex.windows_sandbox.wfp_setup_failure"
)

type WFPSetupMetricOutcome string

const (
	WFPSetupMetricSuccess WFPSetupMetricOutcome = "success"
	WFPSetupMetricFailure WFPSetupMetricOutcome = "failure"
)

type WFPSetupMetric struct {
	Outcome              WFPSetupMetricOutcome
	CodexHome            string
	TargetAccount        string
	InstalledFilterCount int
	Error                string
}

type WFPSetupMetricEmitter func(WFPSetupMetric) error

func (m WFPSetupMetric) Name() string {
	if m.Outcome == WFPSetupMetricSuccess {
		return WFPSetupSuccessMetric
	}
	return WFPSetupFailureMetric
}

func (m WFPSetupMetric) Tags() map[string]string {
	tags := map[string]string{
		"target_account": SanitizeSetupMetricTagValue(m.TargetAccount),
	}
	if m.Outcome == WFPSetupMetricSuccess {
		tags["installed_filter_count"] = fmt.Sprintf("%d", m.InstalledFilterCount)
	} else if m.Error != "" {
		tags["message"] = SanitizeSetupMetricTagValue(m.Error)
	}
	return tags
}

func InstallWFPFilters(codexHome string, offlineUsername string, log func(string), emitters ...WFPSetupMetricEmitter) WFPSetupMetric {
	return installWFPFiltersWithInstaller(codexHome, offlineUsername, log, InstallWFPFiltersForAccount, emitters...)
}

func installWFPFiltersWithInstaller(
	codexHome string,
	offlineUsername string,
	log func(string),
	installer func(string) (int, error),
	emitters ...WFPSetupMetricEmitter,
) WFPSetupMetric {
	if log == nil {
		log = func(string) {}
	}
	metric := runWFPFilterInstall(codexHome, offlineUsername, log, installer)
	emitWFPSetupMetricSafely(metric, log, emitters...)
	return metric
}

func runWFPFilterInstall(codexHome string, offlineUsername string, log func(string), installer func(string) (int, error)) (metric WFPSetupMetric) {
	metric = WFPSetupMetric{
		Outcome:       WFPSetupMetricFailure,
		CodexHome:     codexHome,
		TargetAccount: offlineUsername,
	}
	defer func() {
		if payload := recover(); payload != nil {
			errorText := panicPayloadToString(payload)
			log(fmt.Sprintf("WFP setup panicked for %s: %s; continuing elevated setup", offlineUsername, errorText))
			metric.Outcome = WFPSetupMetricFailure
			metric.InstalledFilterCount = 0
			metric.Error = "panic: " + errorText
		}
	}()
	if installer == nil {
		metric.Error = "missing WFP installer"
		log(fmt.Sprintf("WFP setup failed for %s: %s; continuing elevated setup", offlineUsername, metric.Error))
		return metric
	}
	installedFilterCount, err := installer(offlineUsername)
	if err != nil {
		metric.Error = err.Error()
		log(fmt.Sprintf("WFP setup failed for %s: %s; continuing elevated setup", offlineUsername, metric.Error))
		return metric
	}
	log(fmt.Sprintf("WFP setup succeeded for %s with %d installed filters", offlineUsername, installedFilterCount))
	metric.Outcome = WFPSetupMetricSuccess
	metric.InstalledFilterCount = installedFilterCount
	return metric
}

func emitWFPSetupMetricSafely(metric WFPSetupMetric, log func(string), emitters ...WFPSetupMetricEmitter) {
	for _, emitter := range emitters {
		if emitter == nil {
			continue
		}
		func() {
			defer func() {
				if payload := recover(); payload != nil {
					log(fmt.Sprintf("WFP setup metric emission panicked for %s: %s", metric.TargetAccount, panicPayloadToString(payload)))
				}
			}()
			if err := emitter(metric); err != nil {
				log(fmt.Sprintf("failed to emit WFP setup metric for %s: %s", metric.TargetAccount, err))
			}
		}()
	}
}

func panicPayloadToString(payload any) string {
	switch value := payload.(type) {
	case string:
		return value
	case error:
		return value.Error()
	case fmt.Stringer:
		return value.String()
	default:
		return "unknown panic payload"
	}
}
