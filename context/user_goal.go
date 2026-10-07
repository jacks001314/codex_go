package context

import (
	"bytes"
	"encoding/json"
	"strings"
)

// User-authored goal changes, separate from agent-created goals and runtime
// steering. Host annotations establish provenance and objective completeness;
// text markers control visibility. Oversized objectives are omitted whole so
// truncation cannot turn a restriction into a grant.
//
// Mirrors Rust `UserGoalUpdate` (codex-rs/core/src/context/user_goal.rs,
// upstream #49598 "Persist explicit user goal edits in model history").
const (
	// UserGoalContentKind classifies a goal instruction as model-visible content
	// (Rust `"user.goal"`).
	UserGoalContentKind = "user.goal"
	// UserGoalOmittedObjectiveKind classifies a goal instruction whose objective
	// exceeded the evidence limit and was therefore omitted whole.
	UserGoalOmittedObjectiveKind = "user.goal.omitted"

	userGoalOpenTag  = `<codex_internal_context source="user_goal">`
	userGoalCloseTag = "</codex_internal_context>"

	// maxUserGoalObjectiveBytes bounds the JSON-encoded objective. Even one token
	// per byte leaves room for the wrapper and status below 1K.
	maxUserGoalObjectiveBytes = 700
)

// UserGoalUpdate is one explicit goal mutation accepted from the user-facing
// goal API. It is never constructed from tool output or an automatic
// continuation. A nil Objective with Clear=false renders only a status line.
type UserGoalUpdate struct {
	// Objective is the trimmed user objective, or nil when unchanged.
	Objective *string
	// Status is the requested goal status as a wire string (for example
	// "paused"), or nil when unchanged.
	Status *string
	// Clear marks a `thread/goal/clear` instruction.
	Clear bool
}

// NewUserGoalSet builds a `thread/goal/set` instruction (Rust
// `UserGoalUpdate::Set`).
func NewUserGoalSet(objective *string, status *string) *UserGoalUpdate {
	return &UserGoalUpdate{Objective: objective, Status: status}
}

// NewUserGoalClear builds a `thread/goal/clear` instruction (Rust
// `UserGoalUpdate::Clear`).
func NewUserGoalClear() *UserGoalUpdate {
	return &UserGoalUpdate{Clear: true}
}

func (u *UserGoalUpdate) Role() string {
	return RoleUser
}

func (u *UserGoalUpdate) Markers() (string, string) {
	return userGoalOpenTag, userGoalCloseTag
}

// ContentKind reports the host annotation classification. A set instruction
// whose objective exceeds the evidence limit is classified as omitted; every
// other instruction is a plain goal instruction.
func (u *UserGoalUpdate) ContentKind() string {
	if u != nil && !u.Clear && u.Objective != nil &&
		len(userGoalJSONString(*u.Objective)) > maxUserGoalObjectiveBytes {
		return UserGoalOmittedObjectiveKind
	}
	return UserGoalContentKind
}

// Body renders the model-visible text between the fragment markers.
func (u *UserGoalUpdate) Body() string {
	if u == nil {
		return ""
	}
	if u.Clear {
		return "\nUser cleared the goal.\n"
	}
	var builder strings.Builder
	builder.WriteString("\n")
	if u.Objective != nil {
		// Preserve the objective as data, including quotes and wrapper-like text.
		objective := userGoalJSONString(*u.Objective)
		if len(objective) <= maxUserGoalObjectiveBytes {
			builder.WriteString("User set the goal: " + objective)
		} else {
			builder.WriteString("User set the goal: [objective omitted; exceeds the evidence limit].")
		}
		builder.WriteString("\n")
	}
	if u.Status != nil {
		builder.WriteString("User set goal status: " + userGoalJSONString(*u.Status) + ".\n")
	}
	return builder.String()
}

// userGoalJSONString renders a Go string the way `serde_json::json!(value)`
// does in Rust: a quoted JSON string with HTML characters left unescaped.
func userGoalJSONString(value string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return `""`
	}
	return strings.TrimRight(buffer.String(), "\n")
}
