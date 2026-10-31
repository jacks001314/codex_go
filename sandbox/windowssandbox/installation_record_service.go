package windowssandbox

// LoadRuntimeInstallation returns the recorded registered-Core installation, or
// nil when the record is missing or legacy (Rust installation_record::load_runtime).
func LoadRuntimeInstallation() (*InstallationRecord, error) {
	record, err := LoadInstallation()
	if err != nil || record == nil || record.Runtime == nil {
		return nil, err
	}
	return record, nil
}
