package integrity

// platformIntegrityDependencies mirrors Rust's Windows MXC backend (#51831/#59250):
// deny-glob expansion runs in-process, so no external scanner is part of the
// launcher dependency inventory.
func platformIntegrityDependencies(_ *RunnerRequest, _ *PreparedPolicy) []integrityDependency {
	return nil
}
