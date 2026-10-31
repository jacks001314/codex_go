//go:build !windows

package windowssandbox

import "errors"

func readInstallationRecordValue(key string) ([]byte, bool, error) {
	return nil, false, nil
}

func writeInstallationRecordValue(key string, text string) error {
	return errors.New("the Windows sandbox installation record is only available on Windows")
}

func deleteInstallationRecordValue(key string) error {
	return errors.New("the Windows sandbox installation record is only available on Windows")
}

func flushInstallationRecordKey(key string) error {
	return errors.New("the Windows sandbox installation record is only available on Windows")
}
