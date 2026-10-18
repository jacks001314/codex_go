//go:build !linux && !darwin

package appserverdaemon

func readNativePIDProcessIdentity(uint32) (*ProcessIdentity, error) {
	return nil, nil
}

func matchNativePIDProcessIdentity(identity *ProcessIdentity, pid uint32) (bool, bool, bool, error) {
	return false, false, false, nil
}

func (b *PIDBackend) PromoteLegacyIdentity() error {
	return nil
}
