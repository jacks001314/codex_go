package protocol

import "strings"

// AdmittedUserInputIDPrefix is the reserved namespace for physical user inputs
// admitted by the host (Rust #57538 ResponseItemId::for_admitted_user_input).
// Raw item ingestion must reject caller-supplied ids in this namespace.
const AdmittedUserInputIDPrefix = "msg_input_"

// AdmittedUserInputItemID builds the Responses item id for a host-admitted user
// input.
func AdmittedUserInputItemID(uuid string) string {
	return AdmittedUserInputIDPrefix + strings.TrimSpace(uuid)
}

// IsAdmittedUserInputItemID reports whether the id belongs to the reserved
// host-admitted user input namespace.
func IsAdmittedUserInputItemID(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), AdmittedUserInputIDPrefix)
}
