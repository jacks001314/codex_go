package chatwidget

import "strings"

const HooksBrowserViewID = "hooks-browser"

type HookStatus string

const (
	HookStatusStarted   HookStatus = "started"
	HookStatusCompleted HookStatus = "completed"
	HookStatusFailed    HookStatus = "failed"
	HookStatusBlocked   HookStatus = "blocked"
	HookStatusStopped   HookStatus = "stopped"
)

type HookRun struct {
	ID      string
	Name    string
	Command string
	Status  HookStatus
	Output  string
	Issue   string
	Managed bool
	Review  bool
}

func NewHooksBrowserView(runs []HookRun) SelectionView {
	items := make([]SelectionItem, 0, len(runs))
	for _, run := range runs {
		id := strings.TrimSpace(run.ID)
		if id == "" {
			id = strings.TrimSpace(run.Name)
		}
		if id == "" {
			continue
		}
		description := strings.TrimSpace(run.Command)
		if strings.TrimSpace(string(run.Status)) != "" {
			if description != "" {
				description += " "
			}
			description += "(" + string(run.Status) + ")"
		}
		if strings.TrimSpace(run.Issue) != "" {
			if description != "" {
				description += "\n"
			}
			description += run.Issue
		}
		badges := []string{}
		if run.Managed {
			badges = append(badges, "managed")
		}
		if run.Review {
			badges = append(badges, "review")
		}
		if len(badges) > 0 {
			if description != "" {
				description += "\n"
			}
			description += strings.Join(badges, ", ")
		}
		items = append(items, SelectionItem{
			ID:          id,
			Name:        firstNonEmptyRequestID(strings.TrimSpace(run.Name), id),
			Description: description,
			SearchValue: strings.TrimSpace(run.Name) + " " + strings.TrimSpace(run.Command) + " " + strings.TrimSpace(run.Output),
		})
	}
	if len(items) == 0 {
		items = append(items, SelectionItem{Name: "No hooks configured", Disabled: true})
	}
	return SelectionView{
		ViewID:            HooksBrowserViewID,
		Title:             "Hooks",
		FooterHint:        standardPopupHintLine,
		AllowCancel:       true,
		Searchable:        true,
		SearchPlaceholder: "Type to search hooks",
		Items:             items,
	}
}
