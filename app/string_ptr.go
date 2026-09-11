package app

// stringPtrValue dereferences an optional string, returning "" for nil.
func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
