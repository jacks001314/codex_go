package tool

const (
	GoalGetToolName    = "get_goal"
	GoalCreateToolName = "create_goal"
	GoalUpdateToolName = "update_goal"
)

// GoalGetToolSpec returns the model-visible get_goal tool spec.
func GoalGetToolSpec() Spec {
	return Spec{
		Name:        PlainName(GoalGetToolName),
		Description: "Get the current goal for this thread, including status, budgets, token and elapsed-time usage, and remaining token budget.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Exposure: ExposureModelVisible,
	}
}

// GoalCreateToolSpec returns the model-visible create_goal tool spec.
func GoalCreateToolSpec() Spec {
	return Spec{
		Name: PlainName(GoalCreateToolName),
		Description: `Create a goal only when explicitly requested by the user or system/developer instructions; do not infer goals from ordinary tasks.
Set token_budget only when an explicit token budget is requested. Fails if an unfinished goal exists; use update_goal only for status.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objective": map[string]any{
					"type":        "string",
					"description": "Required. The concrete objective to start pursuing. This starts a new active goal when no goal exists or replaces the current goal when it is complete.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Positive token budget for the new goal. Omit unless explicitly requested.",
				},
			},
			"required":             []string{"objective"},
			"additionalProperties": false,
		},
		Exposure: ExposureModelVisible,
	}
}

// GoalUpdateToolSpec returns the model-visible update_goal tool spec.
func GoalUpdateToolSpec() Spec {
	return Spec{
		Name: PlainName(GoalUpdateToolName),
		Description: `Update the existing goal.
Use this tool only to mark the goal achieved or genuinely blocked.
Set status to complete only when the objective has actually been achieved and no required work remains.
Set status to blocked only when the same blocking condition has repeated for at least three consecutive goal turns, counting the original/user-triggered turn and any automatic continuations, and the agent cannot make meaningful progress without user input or an external-state change.
If the user resumes a goal that was previously marked blocked, treat the resumed run as a fresh blocked audit. If the same blocking condition then repeats for at least three consecutive resumed goal turns, set status to blocked again.
Once the blocked threshold is satisfied, do not keep reporting that you are still blocked while leaving the goal active; set status to blocked.
Do not use blocked merely because the work is hard, slow, uncertain, incomplete, or would benefit from clarification.
Do not mark a goal complete merely because its budget is nearly exhausted or because you are stopping work.
You cannot use this tool to pause, resume, budget-limit, or usage-limit a goal; those status changes are controlled by the user or system.
When marking a budgeted goal achieved with status complete, report the final token usage from the tool result to the user.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"complete", "blocked"},
					"description": "Required. Set to `complete` only when the objective is achieved and no required work remains. Set to `blocked` only after the same blocking condition has recurred for at least three consecutive goal turns and the agent is at an impasse. After a previously blocked goal is resumed, the resumed run starts a fresh blocked audit.",
				},
			},
			"required":             []string{"status"},
			"additionalProperties": false,
		},
		Exposure: ExposureModelVisible,
	}
}
