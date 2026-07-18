package app

type ThreadSessionState struct {
	ThreadID                string
	ForkedFromID            *string
	ForkParentTitle         *string
	ThreadName              *string
	Model                   string
	ModelProviderID         string
	ServiceTier             *string
	ApprovalPolicy          string
	ApprovalsReviewer       string
	PermissionProfile       any
	ActivePermissionProfile any
	CWD                     string
	RuntimeWorkspaceRoots   []string
	InstructionSourcePaths  []string
	ReasoningEffort         *string
	CollaborationMode       *string
	Personality             *string
	MessageHistory          any
	NetworkProxy            any
	RolloutPath             *string
	Originator              string
}

type ThreadPermissionSettings struct {
	ApprovalPolicy          string
	ApprovalsReviewer       string
	PermissionProfile       any
	ActivePermissionProfile any
}

type ThreadSessionCache struct {
	PrimaryThreadID string
	ActiveThreadID  string
	PrimarySession  *ThreadSessionState
	StoreSessions   map[string]*ThreadSessionState
}

type ThreadSessionDefaults struct {
	Model                   string
	ModelProviderID         string
	ServiceTier             *string
	ApprovalPolicy          string
	ApprovalsReviewer       string
	PermissionProfile       any
	ActivePermissionProfile any
	CWD                     string
	RuntimeWorkspaceRoots   []string
	ReasoningEffort         *string
}

type ThreadReadSnapshot struct {
	ThreadID      string
	ThreadName    *string
	ModelProvider string
	CWD           string
	Path          *string
	ReadModel     *string
}

func SyncActiveThreadServiceTierToCachedSession(cache *ThreadSessionCache, serviceTier *string) {
	if cache == nil || cache.ActiveThreadID == "" {
		return
	}
	update := func(session *ThreadSessionState) {
		if session != nil {
			session.ServiceTier = cloneStringPointer(serviceTier)
		}
	}
	if cache.PrimaryThreadID == cache.ActiveThreadID {
		update(cache.PrimarySession)
	}
	if cache.StoreSessions != nil {
		update(cache.StoreSessions[cache.ActiveThreadID])
	}
}

func SyncActiveThreadPermissionSettingsToCachedSession(cache *ThreadSessionCache, settings ThreadPermissionSettings) {
	if cache == nil || cache.ActiveThreadID == "" {
		return
	}
	update := func(session *ThreadSessionState) {
		if session == nil {
			return
		}
		session.ApprovalPolicy = settings.ApprovalPolicy
		session.ApprovalsReviewer = settings.ApprovalsReviewer
		session.PermissionProfile = settings.PermissionProfile
		session.ActivePermissionProfile = settings.ActivePermissionProfile
	}
	if cache.PrimaryThreadID == cache.ActiveThreadID {
		update(cache.PrimarySession)
	}
	if cache.StoreSessions != nil {
		update(cache.StoreSessions[cache.ActiveThreadID])
	}
}

func SessionStateForThreadRead(primary *ThreadSessionState, defaults ThreadSessionDefaults, thread ThreadReadSnapshot) ThreadSessionState {
	var session ThreadSessionState
	if primary != nil {
		session = primary.Clone()
		if session.ThreadID != thread.ThreadID {
			session.CollaborationMode = nil
			session.Personality = nil
		}
	} else {
		session = ThreadSessionState{
			Model:                   defaults.Model,
			ModelProviderID:         defaults.ModelProviderID,
			ServiceTier:             cloneStringPointer(defaults.ServiceTier),
			ApprovalPolicy:          defaults.ApprovalPolicy,
			ApprovalsReviewer:       defaults.ApprovalsReviewer,
			PermissionProfile:       defaults.PermissionProfile,
			ActivePermissionProfile: defaults.ActivePermissionProfile,
			CWD:                     defaults.CWD,
			RuntimeWorkspaceRoots:   append([]string(nil), defaults.RuntimeWorkspaceRoots...),
			InstructionSourcePaths:  []string{},
			ReasoningEffort:         cloneStringPointer(defaults.ReasoningEffort),
			RolloutPath:             cloneStringPointer(thread.Path),
		}
	}

	session.ThreadID = thread.ThreadID
	session.ThreadName = cloneStringPointer(thread.ThreadName)
	session.ModelProviderID = thread.ModelProvider
	session.setCWDRetargetingImplicitRuntimeWorkspaceRoot(thread.CWD)
	session.PermissionProfile = defaults.PermissionProfile
	session.ActivePermissionProfile = defaults.ActivePermissionProfile
	session.InstructionSourcePaths = []string{}
	session.RolloutPath = cloneStringPointer(thread.Path)
	if thread.ReadModel != nil {
		session.Model = *thread.ReadModel
	} else if thread.Path != nil {
		session.Model = ""
	}
	session.MessageHistory = nil
	return session
}

func (s ThreadSessionState) Clone() ThreadSessionState {
	clone := s
	clone.ForkedFromID = cloneStringPointer(s.ForkedFromID)
	clone.ForkParentTitle = cloneStringPointer(s.ForkParentTitle)
	clone.ThreadName = cloneStringPointer(s.ThreadName)
	clone.ServiceTier = cloneStringPointer(s.ServiceTier)
	clone.RuntimeWorkspaceRoots = append([]string(nil), s.RuntimeWorkspaceRoots...)
	clone.InstructionSourcePaths = append([]string(nil), s.InstructionSourcePaths...)
	clone.ReasoningEffort = cloneStringPointer(s.ReasoningEffort)
	clone.CollaborationMode = cloneStringPointer(s.CollaborationMode)
	clone.Personality = cloneStringPointer(s.Personality)
	clone.RolloutPath = cloneStringPointer(s.RolloutPath)
	return clone
}

func (s *ThreadSessionState) setCWDRetargetingImplicitRuntimeWorkspaceRoot(cwd string) {
	if s == nil {
		return
	}
	oldCWD := s.CWD
	s.CWD = cwd
	if len(s.RuntimeWorkspaceRoots) == 0 || (len(s.RuntimeWorkspaceRoots) == 1 && s.RuntimeWorkspaceRoots[0] == oldCWD) {
		s.RuntimeWorkspaceRoots = []string{cwd}
	}
}
