package agentboard

// Storage selection for local boards (Rust #55971).

const (
	// DefaultBoardStorageNamespace keeps the historical board database name.
	DefaultBoardStorageNamespace = "agent_message_board"
	// localBoardStorageNamespaceMaxBytes mirrors Rust's 64-byte namespace cap.
	localBoardStorageNamespaceMaxBytes = 64
)

// LocalBoardOptions selects storage independently of tool selection and
// connection permissions. The zero value selects the default namespace, which
// is Rust's `LocalBoardOptions::default()`.
type LocalBoardOptions struct {
	StorageNamespace string
}

// databaseFile mirrors Rust's `LocalBoardOptions::database_file`: the default
// namespace keeps `agent_message_board_1.sqlite`, while a custom namespace uses
// `message_board_<namespace>_1.sqlite` so it never shares a filename with
// another runtime database.
func (o LocalBoardOptions) databaseFile() (string, error) {
	namespace := o.StorageNamespace
	if namespace == "" {
		namespace = DefaultBoardStorageNamespace
	}
	if len(namespace) > localBoardStorageNamespaceMaxBytes || !validBoardStorageNamespace(namespace) {
		return "", invalid("board storage namespaces require 1–64 lowercase ASCII letters, digits, underscores or hyphens")
	}
	if namespace == DefaultBoardStorageNamespace {
		return AgentMessageBoardDatabaseFile, nil
	}
	return "message_board_" + namespace + "_1.sqlite", nil
}

func validBoardStorageNamespace(namespace string) bool {
	if namespace == "" {
		return false
	}
	for index := 0; index < len(namespace); index++ {
		character := namespace[index]
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
