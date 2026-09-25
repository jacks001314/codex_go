package agent

import "testing"

// Rust parity: codex-rs/protocol/src/agent_path.rs tests.
func TestAgentPathNamesJoinAndResolveLikeRust(t *testing.T) {
	if !AgentPathRoot.IsRoot() || AgentPathRoot.AgentName() != "root" {
		t.Fatalf("root name = %q, isRoot=%v", AgentPathRoot.AgentName(), AgentPathRoot.IsRoot())
	}
	if AgentPathRoot.String() != "/root" || AgentPathRootValue != "/root" {
		t.Fatalf("root value = %q", AgentPathRoot.String())
	}
	if AgentPathMorpheus.IsRoot() || AgentPathMorpheus.AgentName() != "morpheus" {
		t.Fatalf("morpheus = %q, isRoot=%v", AgentPathMorpheus.AgentName(), AgentPathMorpheus.IsRoot())
	}
	if AgentPathMorpheus.String() != "/morpheus" || AgentPathMorpheusValue != "/morpheus" {
		t.Fatalf("morpheus value = %q", AgentPathMorpheus.String())
	}
	child, err := AgentPathRoot.Join("researcher")
	if err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if child != AgentPath("/root/researcher") || child.AgentName() != "researcher" {
		t.Fatalf("child = %q name %q", child, child.AgentName())
	}
	current := AgentPath("/root/researcher")
	relative, err := current.ResolvePath("worker")
	if err != nil || relative != AgentPath("/root/researcher/worker") {
		t.Fatalf("relative resolve = %q, %v", relative, err)
	}
	absolute, err := current.ResolvePath("/root/other")
	if err != nil || absolute != AgentPath("/root/other") {
		t.Fatalf("absolute resolve = %q, %v", absolute, err)
	}
	// `/root` is special-cased before the absolute-path branch; `/morpheus` is
	// not, so it resolves through the absolute-path validator.
	root, err := current.ResolvePath(AgentPathRootValue)
	if err != nil || root != AgentPathRoot {
		t.Fatalf("root resolve = %q, %v", root, err)
	}
	morpheus, err := current.ResolvePath(AgentPathMorpheusValue)
	if err != nil || morpheus != AgentPathMorpheus {
		t.Fatalf("morpheus resolve = %q, %v", morpheus, err)
	}
	// A relative reference may itself contain `/`.
	nested, err := current.ResolvePath("a/b")
	if err != nil || nested != AgentPath("/root/researcher/a/b") {
		t.Fatalf("nested resolve = %q, %v", nested, err)
	}
}

func TestAgentPathRejectsInvalidNamesAndPathsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		run     func() error
		wantErr string
	}{
		{
			name: "uppercase name",
			run: func() error {
				_, err := AgentPathRoot.Join("BadName")
				return err
			},
			wantErr: "agent_name must use only lowercase letters, digits, and underscores",
		},
		{
			name: "not rooted",
			run: func() error {
				_, err := NewAgentPath("/not-root")
				return err
			},
			wantErr: "absolute agent paths must start with `/root` or be `/morpheus`",
		},
		{
			name: "bare slash",
			run: func() error {
				_, err := NewAgentPath("/")
				return err
			},
			wantErr: "absolute agent paths must start with `/root` or be `/morpheus`",
		},
		{
			name: "missing leading slash",
			run: func() error {
				_, err := NewAgentPath("root/worker")
				return err
			},
			wantErr: "absolute agent paths must start with `/root` or be `/morpheus`",
		},
		{
			name: "parent reference",
			run: func() error {
				_, err := AgentPathRoot.ResolvePath("../sibling")
				return err
			},
			wantErr: "agent_name `..` is reserved",
		},
		{
			name: "reserved root name",
			run: func() error {
				_, err := AgentPathRoot.Join("root")
				return err
			},
			wantErr: "agent_name `root` is reserved",
		},
		{
			name: "trailing slash",
			run: func() error {
				_, err := NewAgentPath("/root/worker/")
				return err
			},
			wantErr: "absolute agent path must not end with `/`",
		},
		{
			name: "empty agent name",
			run: func() error {
				_, err := AgentPathRoot.Join("")
				return err
			},
			wantErr: "agent_name must not be empty",
		},
		{
			name: "name containing slash",
			run: func() error {
				_, err := AgentPathRoot.Join("a/b")
				return err
			},
			wantErr: "agent_name must not contain `/`",
		},
		{
			name: "current-directory agent name",
			run: func() error {
				_, err := AgentPathRoot.Join(".")
				return err
			},
			wantErr: "agent_name `.` is reserved",
		},
		{
			name: "empty relative reference",
			run: func() error {
				_, err := AgentPath("/root/worker").ResolvePath("")
				return err
			},
			wantErr: "agent path must not be empty",
		},
		{
			name: "relative reference with trailing slash",
			run: func() error {
				_, err := AgentPathRoot.ResolvePath("worker/")
				return err
			},
			wantErr: "relative agent path must not end with `/`",
		},
		{
			name: "morpheus child",
			run: func() error {
				_, err := AgentPathMorpheus.Join("worker")
				return err
			},
			wantErr: "absolute agent paths must start with `/root` or be `/morpheus`",
		},
		{
			name: "empty segment",
			run: func() error {
				_, err := NewAgentPath("/root//worker")
				return err
			},
			wantErr: "agent_name must not be empty",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.run()
			if err == nil || err.Error() != testCase.wantErr {
				t.Fatalf("error = %v, want %q", err, testCase.wantErr)
			}
		})
	}
}

// The registry keeps its own alias of the protocol root so a spawn reservation
// key and the protocol validator can never disagree.
func TestRootAgentPathAliasMatchesProtocolRoot(t *testing.T) {
	if rootAgentPath != AgentPathRoot {
		t.Fatalf("rootAgentPath = %q, want %q", rootAgentPath, AgentPathRoot)
	}
}
