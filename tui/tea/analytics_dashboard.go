package tea

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/auth"
	"codex_go/chatgptapi"
	codextui "codex_go/tui"
	"codex_go/tui/analytics"
	"codex_go/tui/chatwidget"
	"codex_go/tui/styles"
	"codex_go/utils"
)

// Full-screen account analytics dashboard opened from the /usage menu
// (Rust #45764 analytics::AnalyticsView). The retained view keeps navigation
// state across reopen while every report loads and fails independently.

// analyticsPollInterval bounds how long a completed report waits for its next
// frame. Loads run on their own goroutines; the dashboard polls them without
// blocking.
const analyticsPollInterval = 60 * time.Millisecond

// analyticsPollMsg asks the open dashboard to poll its pending report loads.
type analyticsPollMsg struct {
	generation int
}

type analyticsDashboard struct {
	active     bool
	generation int

	live        *analytics.Live
	liveFactory func(endDate time.Time) *analytics.Live
	modelNames  map[string]string

	sections             [analytics.AnalyticsSectionCount]analytics.AnalyticsSectionState
	chats                analytics.AnalyticsLoad[analytics.AnalyticsChats]
	chatLister           analytics.AnalyticsChatLister
	tasks                analytics.AnalyticsLoad[analytics.AnalyticsTaskChats]
	taskLister           analytics.AnalyticsTaskDescendantLister
	chatMetric           int
	profile              analytics.AnalyticsLoad[chatgptapi.AccountProfile]
	showZeroCreditGroups bool
	account              analytics.AnalyticsLoad[auth.PlanType]
	reportsStarted       bool

	section         analytics.AnalyticsSection
	zoomed          bool
	scrollOffset    int
	followSelection bool
	viewportHeight  int
	ranges          [3]int
	endDate         time.Time
	tokenModel      *string
	groupPicker     *int
}

func newAnalyticsDashboard() *analyticsDashboard {
	dashboard := &analyticsDashboard{
		account:         analytics.UnavailableAnalyticsLoad[auth.PlanType](),
		chats:           analytics.UnavailableAnalyticsLoad[analytics.AnalyticsChats](),
		tasks:           analytics.UnavailableAnalyticsLoad[analytics.AnalyticsTaskChats](),
		profile:         analytics.UnavailableAnalyticsLoad[chatgptapi.AccountProfile](),
		section:         analytics.AnalyticsSectionSummary,
		zoomed:          true,
		followSelection: true,
		viewportHeight:  1,
		endDate:         analyticsDay(time.Now()),
	}
	for index := range dashboard.sections {
		dashboard.sections[index] = analytics.DefaultAnalyticsSectionState()
	}
	dashboard.sections[analytics.AnalyticsSectionUsage].Group = 1
	dashboard.sections[analytics.AnalyticsSectionActivity].Group = 2
	dashboard.sections[analytics.AnalyticsSectionChats].Cursor = 0
	return dashboard
}

// analyticsDay normalizes a timestamp to UTC midnight, like chrono's
// NaiveDate.
func analyticsDay(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// openAnalyticsDashboard opens the retained analytics view from the /usage
// menu (Rust AppEvent::OpenAnalytics).
func (m *Model) openAnalyticsDashboard() bubbletea.Cmd {
	if m == nil || !m.hasChatGPTAccount {
		return nil
	}
	modelNames := make(map[string]string, len(m.modelCatalogOpts))
	for _, option := range m.modelCatalogOpts {
		if strings.TrimSpace(option.ID) != "" && strings.TrimSpace(option.Label) != "" {
			modelNames[option.ID] = option.Label
		}
	}
	if m.analyticsDashboard == nil {
		m.analyticsDashboard = newAnalyticsDashboard()
	}
	factory := m.analyticsLive
	if factory == nil {
		codexHome := m.codexHome
		factory = func(endDate time.Time) *analytics.Live {
			return analytics.NewLive(analytics.AnalyticsSessionConfig{CodexHome: codexHome}, endDate)
		}
	}
	m.analyticsDashboard.open(factory, modelNames, m.analyticsChats, m.analyticsTaskThreads)
	return m.analyticsDashboard.nextPollCmd()
}

// openAnalyticsSummary opens the retained dashboard on the Summary tab with an
// optional activity view (Rust AppEvent::OpenAnalytics { view }, #45769).
func (m *Model) openAnalyticsSummary(view *chatwidget.TokenActivityView) bubbletea.Cmd {
	cmd := m.openAnalyticsDashboard()
	if m == nil || m.analyticsDashboard == nil {
		return cmd
	}
	dashboard := m.analyticsDashboard
	dashboard.section = analytics.AnalyticsSectionSummary
	dashboard.zoomed = true
	dashboard.scrollOffset = 0
	dashboard.groupPicker = nil
	if view != nil {
		for index, candidate := range analytics.AnalyticsSummaryViews {
			if candidate == *view {
				dashboard.sections[analytics.AnalyticsSectionSummary].Group = index
				break
			}
		}
	}
	return cmd
}

// open attaches a live session factory and restarts every report. Navigation
// state from a previous open is retained (Rust AnalyticsView::open).
func (d *analyticsDashboard) open(factory func(endDate time.Time) *analytics.Live, modelNames map[string]string, chatLister analytics.AnalyticsChatLister, taskLister analytics.AnalyticsTaskDescendantLister) {
	if d == nil {
		return
	}
	d.active = true
	d.generation++
	d.liveFactory = factory
	d.modelNames = modelNames
	d.chatLister = chatLister
	d.taskLister = taskLister
	d.refresh()
}

// refresh rebuilds the live client and every load (Rust
// AnalyticsView::refresh).
func (d *analyticsDashboard) refresh() {
	if d == nil {
		return
	}
	d.endDate = analyticsDay(time.Now())
	d.groupPicker = nil
	d.live = nil
	if d.liveFactory != nil {
		d.live = d.liveFactory(d.endDate)
	}
	for index := range d.sections {
		d.sections[index].History.Invalidate()
	}
	d.reportsStarted = false
	d.tokenModel = nil
	d.sections[analytics.AnalyticsSectionChats].Detail = nil
	d.chats.Invalidate()
	d.tasks.Invalidate()
	d.profile.Invalidate()
	d.account.Invalidate()
	if d.live != nil {
		live := d.live
		load := func(ctx context.Context) (*auth.PlanType, error) {
			session, err := live.Session(ctx)
			if err != nil {
				return nil, err
			}
			plan := session.Backend.Account().PlanType
			return &plan, nil
		}
		d.account = *analytics.StartAnalyticsLoad(context.Background(), load, nil)
	}
	d.startReports()
}

// cancelLoads invalidates every report, including loaded data, when the
// retained view is discarded (Rust AnalyticsView::cancel_loads).
func (d *analyticsDashboard) cancelLoads() {
	if d == nil {
		return
	}
	for index := range d.sections {
		d.sections[index].History.Invalidate()
	}
	d.chats.Invalidate()
	d.tasks.Invalidate()
	d.profile.Invalidate()
	d.account.Invalidate()
	d.live = nil
	d.liveFactory = nil
	d.chatLister = nil
	d.taskLister = nil
}

// finish closes the dashboard while keeping its navigation state. Pending
// loads are aborted so a late response can never repaint a closed view
// (Rust AnalyticsView::is_done handling).
func (d *analyticsDashboard) finish() {
	if d == nil {
		return
	}
	d.groupPicker = nil
	d.active = false
	for index := range d.sections {
		if d.sections[index].History.State() == analytics.AnalyticsLoadStateLoading {
			d.sections[index].History.Invalidate()
		}
	}
	if d.chats.State() == analytics.AnalyticsLoadStateLoading {
		d.chats.Invalidate()
	}
	if d.tasks.State() == analytics.AnalyticsLoadStateLoading {
		d.tasks.Invalidate()
	}
	if d.profile.State() == analytics.AnalyticsLoadStateLoading {
		d.profile.Invalidate()
	}
	d.account.Invalidate()
	d.live = nil
}

func (d *analyticsDashboard) loadReport(section analytics.AnalyticsSection) {
	if d == nil {
		return
	}
	if !section.HasReport() {
		return
	}
	if d.live == nil || d.liveFactory == nil {
		d.sections[section].History.Invalidate()
		return
	}
	report := section.Report()
	group := d.sections[section].Group
	rangeIndex := d.ranges[section.RangeGroup(d.business())]
	days := uint32(7)
	if rangeIndex != 0 {
		days = 30
	}
	grouping := analytics.AnalyticsGroupingAt(group)
	live := d.live
	model := d.tokenModel
	load := func(ctx context.Context) (*analytics.AccountAnalyticsHistory, error) {
		if model != nil && report == analytics.AccountAnalyticsReportUsage {
			return live.FilteredHistory(ctx, report, days, grouping, model)
		}
		return live.History(ctx, report, days, grouping)
	}
	d.sections[section].History.Cancel()
	d.sections[section].History = *analytics.StartAnalyticsLoad(context.Background(), load, nil)
}

// loadSummary starts the profile load independently of the report loads
// (Rust AnalyticsView::load_summary, #45769).
func (d *analyticsDashboard) loadSummary() {
	if d == nil {
		return
	}
	if d.live == nil || d.liveFactory == nil {
		d.profile.Invalidate()
		return
	}
	live := d.live
	d.profile = *analytics.StartAnalyticsLoad(context.Background(), func(ctx context.Context) (*chatgptapi.AccountProfile, error) {
		return analytics.LoadAnalyticsProfile(ctx, live)
	}, nil)
}

func (d *analyticsDashboard) business() bool {
	if d == nil {
		return false
	}
	plan, ok := d.account.Ready()
	if !ok {
		return false
	}
	kind := analytics.AccountKindFor(plan)
	return kind == analytics.AccountKindBusiness || kind == analytics.AccountKindEnterprise
}

func (d *analyticsDashboard) consumerAttribution() bool {
	if d == nil || d.business() {
		return false
	}
	var attributed *bool
	if d.live != nil {
		value := d.live.AttributedUsage()
		attributed = &value
	}
	return analytics.AnalyticsConsumerAttribution(false, attributed)
}

func (d *analyticsDashboard) visibleSections() []analytics.AnalyticsSection {
	if d == nil {
		return nil
	}
	plan, ok := d.account.Ready()
	return analytics.AnalyticsVisibleSections(plan, ok)
}

func (d *analyticsDashboard) groupOptions() []int {
	if d == nil || len(d.visibleSections()) == 0 {
		return nil
	}
	var creditGroups []int
	if d.live != nil {
		creditGroups = d.live.CreditGroups()
	}
	return analytics.AnalyticsGroupOptions(d.section, d.business(), d.consumerAttribution(), creditGroups)
}

// pollReports advances every load and starts the reports once the account is
// known (Rust AnalyticsView::poll_reports).
func (d *analyticsDashboard) pollReports() {
	if d == nil {
		return
	}
	for index := range d.sections {
		d.sections[index].History.Poll()
	}
	d.chats.Poll()
	d.tasks.Poll()
	d.profile.Poll()
	d.account.Poll()
	d.startReports()
	if !d.business() && !d.consumerAttribution() {
		group := d.sections[analytics.AnalyticsSectionUsage].Group
		if group != 0 && group != 2 {
			d.sections[analytics.AnalyticsSectionUsage].Group = 0
		}
	}
	if d.groupPicker != nil && *d.groupPicker >= len(d.groupOptions()) {
		d.groupPicker = nil
	}
	if chats, ok := d.chats.Ready(); ok {
		cursor := &d.sections[analytics.AnalyticsSectionChats].Cursor
		*cursor = analyticsMin(*cursor, analyticsMax(len(chats.Rows)-1, 0))
	}
	if tasks, ok := d.tasks.Ready(); ok {
		cursor := &d.sections[analytics.AnalyticsSectionChats].Cursor
		*cursor = analyticsMin(*cursor, analyticsMax(len(tasks.Rows)-1, 0))
	}
}

// startReports loads the account-specific sections exactly once per refresh.
func (d *analyticsDashboard) startReports() {
	if d == nil || d.reportsStarted {
		return
	}
	plan, ok := d.account.Ready()
	if !ok {
		return
	}
	kind := analytics.AccountKindFor(plan)
	d.reportsStarted = true
	visible := d.visibleSections()
	if len(visible) == 0 {
		return
	}
	if !analyticsSectionVisible(visible, d.section) {
		d.section = visible[0]
	}
	usageGroups := []int{1, 2, 0, 3}
	if d.business() {
		usageGroups = []int{6, 2}
	}
	usage := &d.sections[analytics.AnalyticsSectionUsage]
	if !analyticsContainsInt(usageGroups, usage.Group) {
		if kind == analytics.AccountKindBusiness {
			usage.Group = 2
		} else {
			usage.Group = usageGroups[0]
		}
	}
	if d.live != nil {
		if creditGroups := d.live.CreditGroups(); len(creditGroups) > 0 &&
			!analyticsContainsInt(creditGroups, d.sections[analytics.AnalyticsSectionCredits].Group) {
			d.sections[analytics.AnalyticsSectionCredits].Group = creditGroups[0]
		}
	}
	d.loadSummary()
	for _, section := range visible {
		d.loadReport(section)
	}
	if analyticsSectionVisible(visible, analytics.AnalyticsSectionChats) {
		if !d.business() {
			if d.live != nil && d.chatLister != nil && d.taskLister != nil {
				live := d.live
				roots := d.chatLister
				descendants := d.taskLister
				d.tasks = *analytics.StartAnalyticsLoadWithTimeout(context.Background(), func(ctx context.Context) (*analytics.AnalyticsTaskChats, error) {
					return analytics.LoadAnalyticsTasks(ctx, live, roots, descendants)
				}, nil, 120*time.Second)
			} else {
				d.tasks = analytics.UnavailableAnalyticsLoad[analytics.AnalyticsTaskChats]()
			}
		} else if d.live != nil && d.chatLister != nil {
			live := d.live
			lister := d.chatLister
			d.chats = *analytics.StartAnalyticsLoadWithTimeout(context.Background(), func(ctx context.Context) (*analytics.AnalyticsChats, error) {
				return analytics.LoadAnalyticsChats(ctx, live, lister)
			}, nil, 90*time.Second)
		} else {
			d.chats = analytics.UnavailableAnalyticsLoad[analytics.AnalyticsChats]()
		}
	}
}

// changeRange flips the shared 7/30 day range and keeps each section's
// selection on the same calendar day when it is still in range (Rust
// AnalyticsView::change_range).
func (d *analyticsDashboard) changeRange() {
	if d == nil {
		return
	}
	group := d.section.RangeGroup(d.business())
	d.ranges[group] ^= 1
	toThirty := d.ranges[group] == 1
	for _, section := range d.visibleSections() {
		if section.RangeGroup(d.business()) != group {
			continue
		}
		state := &d.sections[section]
		state.Cursor, state.Detail = analytics.AnalyticsShiftSectionSelection(state.Cursor, state.Detail, toThirty)
		d.loadReport(section)
	}
}

func (d *analyticsDashboard) rowCount() int {
	if d == nil {
		return 7
	}
	if d.section == analytics.AnalyticsSectionChats {
		if !d.business() {
			if tasks, ok := d.tasks.Ready(); ok {
				return len(tasks.Rows)
			}
			return 0
		}
		if chats, ok := d.chats.Ready(); ok {
			return len(chats.Rows)
		}
		return 0
	}
	return analytics.AnalyticsSectionDays(d.ranges[d.section.RangeGroup(d.business())])
}

func (d *analyticsDashboard) dayHasDetails(section analytics.AnalyticsSection, cursor int) bool {
	history, ok := d.sections[section].History.Ready()
	if !ok {
		return false
	}
	start := analytics.AnalyticsSectionStartDate(d.endDate, d.ranges[section.RangeGroup(d.business())])
	return analytics.AnalyticsDayHasDetails(&history, start, cursor)
}

func (d *analyticsDashboard) modelName(key string) string {
	if name, ok := d.modelNames[key]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	return key
}

// summaryProfile returns the decoded profile, its load message, readiness and
// the resolved plan for the Summary panel and card.
func (d *analyticsDashboard) summaryProfile() (*chatgptapi.AccountProfile, string, bool, *auth.PlanType) {
	if d == nil {
		return nil, "", false, nil
	}
	profile, ready := d.profile.Ready()
	var loaded *chatgptapi.AccountProfile
	if ready {
		loaded = &profile
	}
	message, _ := d.profile.Message()
	var planPtr *auth.PlanType
	if plan, ok := d.account.Ready(); ok {
		planPtr = &plan
	}
	return loaded, message, ready, planPtr
}

// cycleTokenModel advances the business usage model filter, wrapping to no
// filter after the last model (Rust AnalyticsView's `m` handling).
func (d *analyticsDashboard) cycleTokenModel() {
	var models []string
	if d.live != nil {
		models = d.live.TokenModels()
	}
	var next *string
	switch {
	case d.tokenModel == nil:
		if len(models) > 0 {
			value := models[0]
			next = &value
		}
	default:
		position := -1
		for index, model := range models {
			if model == *d.tokenModel {
				position = index
				break
			}
		}
		switch {
		case position >= 0 && position+1 < len(models):
			value := models[position+1]
			next = &value
		case position < 0 && len(models) > 0:
			value := models[0]
			next = &value
		}
	}
	d.tokenModel = next
	d.loadReport(analytics.AnalyticsSectionUsage)
}

// nextPollCmd schedules the next poll while any load is pending.
func (d *analyticsDashboard) nextPollCmd() bubbletea.Cmd {
	if d == nil || !d.active {
		return nil
	}
	if !d.anyLoading() {
		return nil
	}
	generation := d.generation
	return bubbletea.Tick(analyticsPollInterval, func(time.Time) bubbletea.Msg {
		return analyticsPollMsg{generation: generation}
	})
}

// anyLoading reports whether any account or report load is still pending.
func (d *analyticsDashboard) anyLoading() bool {
	if d == nil {
		return false
	}
	if d.account.State() == analytics.AnalyticsLoadStateLoading {
		return true
	}
	for index := range d.sections {
		if d.sections[index].History.State() == analytics.AnalyticsLoadStateLoading {
			return true
		}
	}
	if d.chats.State() == analytics.AnalyticsLoadStateLoading {
		return true
	}
	if d.tasks.State() == analytics.AnalyticsLoadStateLoading {
		return true
	}
	if d.profile.State() == analytics.AnalyticsLoadStateLoading {
		return true
	}
	return false
}

func analyticsSectionVisible(visible []analytics.AnalyticsSection, section analytics.AnalyticsSection) bool {
	for _, candidate := range visible {
		if candidate == section {
			return true
		}
	}
	return false
}

func analyticsContainsInt(values []int, value int) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func analyticsMax(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func analyticsMin(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// analyticsPlainRune returns a single unmodified rune key.
func analyticsPlainRune(msg bubbletea.KeyMsg) (rune, bool) {
	if msg.Type != bubbletea.KeyRunes || len(msg.Runes) != 1 || msg.Alt || msg.Paste {
		return 0, false
	}
	return msg.Runes[0], true
}

// analyticsListAction resolves the shared list keymap action for a key.
func (m *Model) analyticsListAction(msg bubbletea.KeyMsg) string {
	spec := keySpecFromKeyMsg(msg)
	if spec == "" {
		return ""
	}
	for _, action := range []string{
		"move_up", "move_down", "move_left", "move_right",
		"page_up", "page_down", "jump_top", "jump_bottom",
		"accept", "cancel",
	} {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
		for _, binding := range bindings {
			normalized, err := codextui.NormalizeKeybindingSpec(binding)
			if err != nil {
				continue
			}
			if normalized == spec {
				return action
			}
		}
	}
	return ""
}

func (d *analyticsDashboard) selectSection(section analytics.AnalyticsSection) {
	if d.zoomed && section != d.section {
		d.scrollOffset = 0
	}
	d.section = section
	d.groupPicker = nil
}

// summaryAction scrolls the Summary body. A missing action leaves the view
// unchanged (Rust AnalyticsView::summary_action, #45769).
func (d *analyticsDashboard) summaryAction(action string) {
	if d == nil {
		return
	}
	switch action {
	case "move_up":
		d.scrollOffset--
	case "move_down":
		d.scrollOffset++
	case "page_up":
		d.scrollOffset -= d.viewportHeight
	case "page_down":
		d.scrollOffset += d.viewportHeight
	case "jump_top":
		d.scrollOffset = 0
	case "jump_bottom":
		d.scrollOffset = int(^uint(0) >> 1)
	case "cancel":
		d.finish()
		return
	default:
		return
	}
	if d.scrollOffset < 0 {
		d.scrollOffset = 0
	}
	d.followSelection = false
}

// handleKey applies one key press. It mirrors Rust AnalyticsView::handle_key.
func (d *analyticsDashboard) handleKey(m *Model, msg bubbletea.KeyMsg) bool {
	if d == nil || !d.active {
		return false
	}
	if runeKey, ok := analyticsPlainRune(msg); ok && runeKey == 'q' {
		d.finish()
		return true
	}
	if msg.Type == bubbletea.KeyCtrlC {
		d.finish()
		return true
	}
	action := ""
	if runeKey, ok := analyticsPlainRune(msg); ok && runeKey == ' ' {
		action = "accept"
	} else {
		action = m.analyticsListAction(msg)
	}
	if d.groupPicker != nil {
		options := d.groupOptions()
		choice := *d.groupPicker
		if choice < 0 || choice >= len(options) {
			d.groupPicker = nil
			return true
		}
		switch action {
		case "move_up", "move_left":
			next := (choice + len(options) - 1) % len(options)
			d.groupPicker = &next
		case "move_down", "move_right":
			next := (choice + 1) % len(options)
			d.groupPicker = &next
		case "accept":
			d.sections[d.section].Group = options[choice]
			d.groupPicker = nil
			d.loadReport(d.section)
		case "cancel":
			d.groupPicker = nil
		}
		d.followSelection = true
		return true
	}
	d.followSelection = true
	if runeKey, ok := analyticsPlainRune(msg); ok {
		switch runeKey {
		case 'z':
			d.zoomed = !d.zoomed
			d.scrollOffset = 0
			return true
		case 'R':
			d.refresh()
			return true
		case 'm':
			if d.section == analytics.AnalyticsSectionUsage && d.business() {
				d.cycleTokenModel()
				return true
			}
		case 'r':
			if d.section != analytics.AnalyticsSectionChats &&
				d.section != analytics.AnalyticsSectionSummary &&
				len(d.visibleSections()) > 0 {
				d.changeRange()
				return true
			}
		case 'a':
			if d.section == analytics.AnalyticsSectionChats && d.sections[d.section].Detail != nil {
				d.showZeroCreditGroups = !d.showZeroCreditGroups
				return true
			}
		case 's':
			if d.section == analytics.AnalyticsSectionChats && !d.business() {
				tasks, ok := d.tasks.Ready()
				var loaded *analytics.AnalyticsTaskChats
				if ok {
					loaded = &tasks
				}
				metrics := analytics.AnalyticsTaskMetrics(loaded)
				current := 0
				for index, metric := range metrics {
					if metric == d.chatMetric {
						current = index
						break
					}
				}
				d.chatMetric = metrics[(current+1)%len(metrics)]
				d.sections[d.section].Detail = nil
				return true
			}
		case 'g':
			if options := d.groupOptions(); len(options) > 1 {
				choice := 0
				for index, group := range options {
					if group == d.sections[d.section].Group {
						choice = index
						break
					}
				}
				d.groupPicker = &choice
				return true
			}
		}
	}
	if !d.zoomed && action == "accept" {
		d.zoomed = true
		d.scrollOffset = 0
		return true
	}
	visible := d.visibleSections()
	if len(visible) == 0 {
		if action == "cancel" {
			d.finish()
		}
		return true
	}
	if runeKey, ok := analyticsPlainRune(msg); ok && runeKey >= '1' && runeKey <= '7' {
		if index := int(runeKey - '1'); index < len(visible) {
			d.selectSection(visible[index])
		}
		return true
	}
	current := 0
	for index, section := range visible {
		if section == d.section {
			current = index
			break
		}
	}
	switch msg.Type {
	case bubbletea.KeyTab:
		d.selectSection(visible[(current+1)%len(visible)])
		return true
	case bubbletea.KeyShiftTab:
		d.selectSection(visible[(current+len(visible)-1)%len(visible)])
		return true
	}
	if d.section == analytics.AnalyticsSectionSummary {
		d.summaryAction(action)
		return true
	}
	if (d.section == analytics.AnalyticsSectionPlugins ||
		d.section == analytics.AnalyticsSectionActivity ||
		d.section == analytics.AnalyticsSectionSkills) &&
		action == "accept" && !d.dayHasDetails(d.section, d.sections[d.section].Cursor) {
		return true
	}
	chart := d.section != analytics.AnalyticsSectionChats
	if !chart && (action == "accept" || action == "move_right") {
		cursor := d.sections[d.section].Cursor
		if d.business() {
			chats, ok := d.chats.Ready()
			if !ok || cursor < 0 || cursor >= len(chats.Rows) || chats.Rows[cursor].Usage == nil {
				return true
			}
		} else {
			tasks, ok := d.tasks.Ready()
			if !ok || cursor < 0 || cursor >= len(tasks.Rows) ||
				analytics.AnalyticsTaskAvailable(tasks.Rows[cursor]) == nil {
				return true
			}
		}
	}
	count := analyticsMax(d.rowCount(), 1)
	state := &d.sections[d.section]
	previousCursor := state.Cursor
	switch action {
	case "move_up":
		state.Cursor = analyticsMax(state.Cursor-1, 0)
	case "move_down":
		state.Cursor = analyticsMin(state.Cursor+1, count-1)
	case "move_left":
		if chart {
			state.Cursor = analyticsMax(state.Cursor-1, 0)
		} else {
			state.Detail = nil
		}
	case "move_right":
		if chart {
			state.Cursor = analyticsMin(state.Cursor+1, count-1)
		} else if state.Detail != nil && *state.Detail == state.Cursor {
			state.Detail = nil
		} else {
			cursor := state.Cursor
			state.Detail = &cursor
		}
	case "jump_top":
		state.Cursor = 0
	case "jump_bottom":
		state.Cursor = count - 1
	case "accept":
		if state.Detail != nil && *state.Detail == state.Cursor {
			state.Detail = nil
		} else {
			cursor := state.Cursor
			state.Detail = &cursor
		}
	case "cancel":
		if !d.zoomed || state.Detail == nil {
			d.finish()
			return true
		}
		state.Detail = nil
	case "page_down":
		d.scrollOffset += d.viewportHeight
		d.followSelection = false
	case "page_up":
		d.scrollOffset = analyticsMax(d.scrollOffset-d.viewportHeight, 0)
		d.followSelection = false
	}
	if state.Detail != nil {
		cursor := state.Cursor
		state.Detail = &cursor
	}
	if !chart && state.Cursor != previousCursor {
		state.Detail = nil
	}
	return true
}

// render draws the whole dashboard inside the terminal area (Rust
// analytics/render.rs AnalyticsView::render).
func (d *analyticsDashboard) render(m *Model, width, height int) string {
	d.pollReports()
	innerWidth := width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	visible := d.visibleSections()
	// Top chats always covers 30 days, so it never shows the 7/30d range row
	// (Rust render.rs show_range).
	showRange := len(visible) > 0 &&
		d.section != analytics.AnalyticsSectionChats &&
		d.section != analytics.AnalyticsSectionSummary
	theme := m.styles()

	controls := d.controls(m, showRange)
	footer := d.footerLines(theme, controls, innerWidth)
	if d.groupPicker == nil && (d.section == analytics.AnalyticsSectionPlugins ||
		d.section == analytics.AnalyticsSectionActivity ||
		d.section == analytics.AnalyticsSectionSkills) &&
		!d.dayHasDetails(d.section, d.sections[d.section].Cursor) {
		height := len(footer)
		controls = strings.Replace(controls, " · "+analyticsHintLabel(m, "accept")+" details", "", 1)
		footer = d.footerLines(theme, controls, innerWidth)
		for len(footer) < height {
			footer = append(footer, "")
		}
		if len(footer) > height {
			footer = footer[:height]
		}
	}

	lines := []string{analyticsTitleLine(theme, d.live)}
	if showRange {
		lines = append(lines, analyticsRangeLine(theme, d.section.RangeGroup(d.business()), d.ranges, d.endDate), "")
	} else {
		lines = append(lines, "")
	}
	if tabs := analyticsTabLines(theme, d, innerWidth); len(tabs) > 0 {
		lines = append(lines, tabs...)
		lines = append(lines, "")
	}

	bodyHeight := height - len(lines) - len(footer)
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	d.viewportHeight = analyticsMax(bodyHeight, 1)
	body, selectionStart, selectionEnd := d.bodyLines(innerWidth, bodyHeight)
	if d.followSelection {
		if selectionStart < d.scrollOffset {
			d.scrollOffset = selectionStart
		} else {
			end := selectionStart + analyticsMin(selectionEnd-selectionStart, d.viewportHeight)
			if end > d.scrollOffset+d.viewportHeight {
				d.scrollOffset = end - d.viewportHeight
			}
		}
	}
	d.scrollOffset = analyticsMin(analyticsMax(d.scrollOffset, 0), analyticsMax(len(body)-d.viewportHeight, 0))
	d.followSelection = false
	for index := 0; index < bodyHeight; index++ {
		position := d.scrollOffset + index
		if position < len(body) {
			lines = append(lines, body[position])
		} else {
			lines = append(lines, "")
		}
	}
	lines = append(lines, footer...)

	rendered := make([]string, len(lines))
	for index, line := range lines {
		if line == "" {
			rendered[index] = ""
			continue
		}
		rendered[index] = "  " + analyticsTruncateStyled(line, width-2)
	}
	if height > 0 && len(rendered) > height {
		rendered = rendered[:height]
	}
	return strings.Join(rendered, "\n")
}

// bodyLines renders the maximized panel or the overview grid.
func (d *analyticsDashboard) bodyLines(width, height int) ([]string, int, int) {
	if len(d.visibleSections()) == 0 {
		return []string{analytics.AnalyticsStatusLine(d.account)}, 0, 1
	}
	if d.zoomed {
		chartHeight := height / 5
		chartHeight = analyticsMin(analyticsMax(chartHeight, 4), 10)
		panel := d.panel(d.section, width, chartHeight)
		if panel.ChartBands > 0 {
			overhead := len(panel.Lines) - chartHeight*panel.ChartBands
			adjusted := (height - overhead - 1) / panel.ChartBands
			if adjusted < 2 {
				adjusted = 2
			}
			if adjusted != chartHeight {
				panel = d.panel(d.section, width, adjusted)
			}
		}
		_, profileReady := d.profile.Ready()
		centerSummary := d.section == analytics.AnalyticsSectionSummary &&
			profileReady &&
			d.groupPicker == nil
		if centerSummary {
			// Keep the heading and rule fixed and center the profile below
			// them (Rust render.rs center_summary, #45769).
			padding := (height - len(panel.Lines)) / 2
			if padding > 0 {
				insert := make([]string, padding)
				panel.Lines = append(panel.Lines[:2], append(insert, panel.Lines[2:]...)...)
			}
		}
		start, end := 0, len(panel.Lines)
		if len(panel.Lines) > height {
			start, end = panel.SelectionStart, panel.SelectionEnd
		}
		lines := make([]string, 0, len(panel.Lines)+1)
		lines = append(lines, panel.Lines...)
		if !centerSummary {
			lines = append(lines, "")
		}
		return lines, start, end
	}
	groups := [analytics.AnalyticsSectionCount]int{}
	for index := range d.sections {
		groups[index] = d.sections[index].Group
	}
	chats, chatsReady := d.chats.Ready()
	var loadedChats *analytics.AnalyticsChats
	if chatsReady {
		loadedChats = &chats
	}
	chatMessage, _ := d.chats.Message()
	tasks, tasksReady := d.tasks.Ready()
	var loadedTasks *analytics.AnalyticsTaskChats
	if tasksReady {
		loadedTasks = &tasks
	}
	taskMessage, _ := d.tasks.Message()
	profile, profileMessage, profileReady, planPtr := d.summaryProfile()
	lines, start, end := analytics.AnalyticsDashboardLines(analytics.AnalyticsDashboardOptions{
		Width:               width,
		Height:              height,
		Visible:             d.visibleSections(),
		Section:             d.section,
		Ranges:              d.ranges,
		Groups:              groups,
		GroupOptions:        d.groupOptions(),
		GroupPicker:         d.groupPicker,
		Business:            d.business(),
		ConsumerAttribution: d.consumerAttribution(),
		TokenModel:          d.tokenModel,
		Chats:               loadedChats,
		ChatMessage:         chatMessage,
		ChatReady:           chatsReady,
		Tasks:               loadedTasks,
		TaskMessage:         taskMessage,
		TaskReady:           tasksReady,
		ChatMetric:          d.chatMetric,
		Profile:             profile,
		ProfileMessage:      profileMessage,
		ProfileReady:        profileReady,
		Plan:                planPtr,
		History:             d.historyLines,
	})
	return lines, start, end
}

func (d *analyticsDashboard) panel(section analytics.AnalyticsSection, width, chartHeight int) analytics.AnalyticsPanel {
	history, ok := d.sections[section].History.Ready()
	var loaded *analytics.AccountAnalyticsHistory
	if ok {
		loaded = &history
	}
	chats, chatsReady := d.chats.Ready()
	var loadedChats *analytics.AnalyticsChats
	if chatsReady {
		loadedChats = &chats
	}
	chatMessage, _ := d.chats.Message()
	tasks, tasksReady := d.tasks.Ready()
	var loadedTasks *analytics.AnalyticsTaskChats
	if tasksReady {
		loadedTasks = &tasks
	}
	taskMessage, _ := d.tasks.Message()
	profile, profileMessage, profileReady, planPtr := d.summaryProfile()
	return analytics.AnalyticsPanelLines(analytics.AnalyticsPanelOptions{
		Section:              section,
		State:                d.sections[section],
		History:              loaded,
		Chats:                loadedChats,
		ChatMessage:          chatMessage,
		ChatReady:            chatsReady,
		Tasks:                loadedTasks,
		TaskMessage:          taskMessage,
		TaskReady:            tasksReady,
		ChatMetric:           d.chatMetric,
		Profile:              profile,
		ProfileMessage:       profileMessage,
		ProfileReady:         profileReady,
		Plan:                 planPtr,
		ShowZeroCreditGroups: d.showZeroCreditGroups,
		ViewportHeight:       d.viewportHeight,
		Width:                width,
		ChartHeight:          chartHeight,
		RangeIndex:           d.ranges[section.RangeGroup(d.business())],
		EndDate:              d.endDate,
		Focused:              section == d.section,
		Business:             d.business(),
		ConsumerAttribution:  d.consumerAttribution(),
		TokenModel:           d.tokenModel,
		GroupOptions:         d.groupOptions(),
		GroupPicker:          d.groupPicker,
		ModelName:            d.modelName,
	})
}

func (d *analyticsDashboard) historyLines(section analytics.AnalyticsSection, width, height int) []string {
	history, ok := d.sections[section].History.Ready()
	var loaded *analytics.AccountAnalyticsHistory
	if ok {
		loaded = &history
	}
	lines, _ := analytics.AnalyticsHistoryLines(section, d.sections[section], loaded, analytics.AnalyticsHistoryOptions{
		Width:               width,
		ChartHeight:         height,
		RangeIndex:          d.ranges[section.RangeGroup(d.business())],
		EndDate:             d.endDate,
		Zoomed:              d.zoomed,
		Business:            d.business(),
		ConsumerAttribution: d.consumerAttribution(),
		ModelName:           d.modelName,
	})
	return lines
}

// controls is the footer hint text, one line per row before wrapping.
func (d *analyticsDashboard) controls(m *Model, showRange bool) string {
	cancelHint := analyticsHintLabel(m, "cancel")
	if len(d.visibleSections()) == 0 {
		return "R refresh · " + cancelHint + " back · q close"
	}
	if d.groupPicker != nil {
		target := "group"
		if d.section == analytics.AnalyticsSectionSummary {
			target = "view"
		}
		return analyticsHintLabel(m, "move_up") + "/" + analyticsHintLabel(m, "move_down") + " " + target + " · " +
			analyticsHintLabel(m, "accept") + " select · " + cancelHint + " cancel"
	}
	navigation := analyticsHintLabel(m, "accept") + "/z maximize"
	if d.zoomed {
		if d.section == analytics.AnalyticsSectionChats {
			navigation = analyticsHintLabel(m, "move_up") + "/" + analyticsHintLabel(m, "move_down") + " row · " +
				analyticsHintLabel(m, "accept") + " details"
		} else if d.section == analytics.AnalyticsSectionSummary {
			navigation = analyticsHintLabel(m, "move_up") + "/" + analyticsHintLabel(m, "move_down") + " scroll"
		} else {
			navigation = analyticsHintLabel(m, "move_left") + "/" + analyticsHintLabel(m, "move_right") + " day · " +
				analyticsHintLabel(m, "accept") + " details"
		}
	}
	zoomHint := ""
	switch {
	case d.section == analytics.AnalyticsSectionChats && !d.business() && !d.zoomed:
		zoomHint = "s sort · "
	case d.section == analytics.AnalyticsSectionChats && !d.business():
		zoomHint = "s sort · z dashboard · "
	case d.zoomed:
		zoomHint = "z dashboard · "
	}
	rangeHint := ""
	if showRange {
		rangeHint = "r 7/30d · "
	}
	groupHint := ""
	if len(d.groupOptions()) >= 2 {
		if d.section == analytics.AnalyticsSectionSummary {
			groupHint = "g view · "
		} else {
			groupHint = "g group · "
		}
	}
	return "tab/1–" + strconv.Itoa(len(d.visibleSections())) + " section · " + zoomHint + navigation + "\n" +
		rangeHint + groupHint + "R refresh · " +
		analyticsHintLabel(m, "page_up") + "/" + analyticsHintLabel(m, "page_down") + " scroll · " +
		cancelHint + " back · q close"
}

// footerLines wraps the control hints and highlights their keys.
func (d *analyticsDashboard) footerLines(theme styles.Styles, controls string, width int) []string {
	wrapped := analytics.AnalyticsWrapLines(strings.Split(controls, "\n"), width)
	lines := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		lines = append(lines, analyticsStyledControls(theme, line))
	}
	return lines
}

// analyticsStyledControls dims the footer and highlights each key token.
func analyticsStyledControls(theme styles.Styles, line string) string {
	reset := theme.ExecCell.Reset
	if reset == "" {
		return line
	}
	dim := theme.Chat.DimText
	bold := theme.Dialog.Highlight + theme.ExecCell.Bold
	parts := strings.Split(line, " · ")
	var builder strings.Builder
	for index, part := range parts {
		if index > 0 {
			builder.WriteString(" · ")
		}
		keys, description := part, ""
		if at := strings.LastIndex(part, " "); at >= 0 {
			keys = part[:at]
			description = part[at:]
		}
		builder.WriteString(bold)
		builder.WriteString(keys)
		builder.WriteString(reset)
		builder.WriteString(dim)
		builder.WriteString(description)
		builder.WriteString(reset)
	}
	return dim + builder.String() + reset
}

func analyticsTitleLine(theme styles.Styles, live *analytics.Live) string {
	suffix := "  ·  Live account"
	if live != nil {
		if label := live.AccountLabel(); label != nil && strings.TrimSpace(*label) != "" {
			suffix += " · " + *label
		}
	}
	reset := theme.ExecCell.Reset
	if reset == "" {
		return "Analytics" + suffix
	}
	return theme.ExecCell.Bold + "Analytics" + reset + theme.Chat.DimText + suffix + reset
}

func analyticsRangeLine(theme styles.Styles, group analytics.AnalyticsRangeGroup, ranges [3]int, endDate time.Time) string {
	accent := theme.Dialog.Highlight + theme.ExecCell.Bold
	reset := theme.ExecCell.Reset
	sevenDay, thirtyDay := "7d", "30d"
	if ranges[group] == 0 {
		if reset != "" {
			sevenDay = accent + "[7d]" + reset
		} else {
			sevenDay = "[7d]"
		}
	} else if reset != "" {
		thirtyDay = accent + "[30d]" + reset
	} else {
		thirtyDay = "[30d]"
	}
	start := analytics.AnalyticsSectionStartDate(endDate, ranges[group])
	dates := start.Format("Jan 2") + "–" + endDate.Format("Jan 2, 2006")
	if reset == "" {
		return sevenDay + "  " + thirtyDay + "  ·  " + dates
	}
	return sevenDay + "  " + thirtyDay + theme.Chat.DimText + "  ·  " + dates + reset
}

func analyticsTabLines(theme styles.Styles, d *analyticsDashboard, width int) []string {
	if !d.zoomed {
		return nil
	}
	visible := d.visibleSections()
	if len(visible) == 0 {
		return nil
	}
	var builder strings.Builder
	for index, section := range visible {
		label := analytics.AnalyticsSectionTitle(section, d.business(), d.consumerAttribution())
		if width >= 64 {
			label = fmt.Sprintf("%d %s", index+1, label)
		}
		if section == d.section {
			builder.WriteString("[" + label + "]  ")
		} else {
			builder.WriteString(label + "  ")
		}
	}
	wrapped := analytics.AnalyticsWrapLines([]string{builder.String()}, width)
	lines := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		lines = append(lines, analyticsStyledTabs(theme, line))
	}
	return lines
}

// analyticsStyledTabs accents the bracketed (focused) tab and dims the rest.
func analyticsStyledTabs(theme styles.Styles, line string) string {
	reset := theme.ExecCell.Reset
	if reset == "" {
		return line
	}
	secondary := theme.Chat.DimText
	accent := theme.Dialog.Highlight
	var builder strings.Builder
	builder.WriteString(secondary)
	for {
		open := strings.Index(line, "[")
		if open < 0 {
			builder.WriteString(line)
			break
		}
		close := strings.Index(line[open:], "]")
		if close < 0 {
			builder.WriteString(line)
			break
		}
		close += open
		builder.WriteString(line[:open])
		builder.WriteString(reset)
		builder.WriteString(accent)
		builder.WriteString(line[open : close+1])
		builder.WriteString(reset)
		builder.WriteString(secondary)
		line = line[close+1:]
	}
	builder.WriteString(reset)
	return builder.String()
}

// analyticsTruncateStyled truncates a styled line by its visible width so the
// escape sequences survive the cut.
func analyticsTruncateStyled(line string, width int) string {
	if width <= 0 || analytics.AnalyticsDisplayWidth(utils.StripANSI(line)) <= width {
		return line
	}
	var builder strings.Builder
	used := 0
	for index := 0; index < len(line); {
		if line[index] == 0x1b {
			end := index + 1
			if end < len(line) && line[end] == '[' {
				end++
				for end < len(line) && line[end] != 'm' {
					end++
				}
				if end < len(line) {
					end++
				}
			}
			builder.WriteString(line[index:end])
			index = end
			continue
		}
		r, size := utf8.DecodeRuneInString(line[index:])
		charWidth := analytics.AnalyticsDisplayWidth(string(r))
		if used+charWidth > width {
			break
		}
		builder.WriteString(line[index : index+size])
		used += charWidth
		index += size
	}
	return builder.String()
}

// analyticsHintLabel renders the primary list binding for an action.
func analyticsHintLabel(m *Model, action string) string {
	if m == nil {
		return ""
	}
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
	if len(bindings) == 0 {
		return ""
	}
	normalized, err := codextui.NormalizeKeybindingSpec(bindings[0])
	if err != nil {
		return bindings[0]
	}
	return analyticsKeyLabel(normalized)
}

func analyticsKeyLabel(spec string) string {
	switch spec {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "page-up":
		return "pgup"
	case "page-down":
		return "pgdn"
	case "space":
		return "space"
	}
	return strings.ReplaceAll(spec, "-", "+")
}
