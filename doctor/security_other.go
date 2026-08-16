//go:build !windows && !darwin

package doctor

func endpointProtectionInspection() endpointInspection {
	return endpointInspection{}
}
