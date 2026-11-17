package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Rust #60007 cloud_project_tests: GitHub origins normalize without
// credentials or query strings.
func TestGitHubCloudProjectNormalizesOriginsLikeRust(t *testing.T) {
	for _, origin := range []string{
		"git@github.com:Org/Repo.git",
		"ssh://git@github.com/Org/Repo.git",
		"https://user:password@github.com/Org/Repo.git?token=secret#fragment",
	} {
		project := githubCloudProject(origin)
		if project == nil {
			t.Fatalf("origin %q was rejected", origin)
		}
		if project.Name != "repo" || project.CWD != "/workspace/org/repo" ||
			len(project.OriginURLs) != 1 || project.OriginURLs[0] != "https://github.com/org/repo" {
			t.Fatalf("origin %q project = %#v", origin, project)
		}
	}
	if project := githubCloudProject("ssh://git@ssh.github.com:443/Org/Second.git"); project == nil ||
		project.Name != "second" || project.CWD != "/workspace/org/second" {
		t.Fatalf("ssh.github.com:443 project = %#v", project)
	}
}

func TestGitHubCloudProjectRejectsUnsupportedOriginsLikeRust(t *testing.T) {
	for _, origin := range []string{
		"/local/repository",
		"github.com/org/repo",
		"https://git.example.com/org/repo",
		"http://ssh.github.com:443/org/repo.git",
		"ssh://git@ssh.github.com:8443/org/repo.git",
		"https://github.com/org",
		"https://github.com/org/repo/tree/main",
		"https://github.com/org/repo%2fother",
		"https://example.com?target=@github.com/org/wrong",
	} {
		if project := githubCloudProject(origin); project != nil {
			t.Fatalf("origin %q unexpectedly produced %#v", origin, project)
		}
	}
}

// Resolving from a nested directory uses the repository's origin.
func TestResolveCloudProjectFromCWDReadsGitOriginLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", "git@github.com:org/first.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	nested := filepath.Join(repo, "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	project := resolveCloudProjectFromCWD(context.Background(), nested)
	if project == nil || project.Name != "first" || project.CWD != "/workspace/org/first" {
		t.Fatalf("nested project = %#v", project)
	}
	if project := resolveCloudProjectFromCWD(context.Background(), t.TempDir()); project != nil {
		t.Fatalf("projectless temp dir produced %#v", project)
	}
}

// Rust #61956: a saved environment start never inspects the local working
// directory, while a project start still resolves and retains it.
func TestCloudThreadStartEnvironmentSelectionLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", "git@github.com:org/first.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	client := &remoteAppServerTUIClient{}
	selection := client.resolveCloudEnvironment(context.Background(), cloudThreadStart{
		savedEnvironment: "ws~asenvcfg_saved",
		projectCWD:       repo,
	})
	if selection == nil || selection.SavedID != "ws~asenvcfg_saved" || selection.Project != nil {
		t.Fatalf("saved selection = %#v", selection)
	}
	if remembered := client.rememberedHostProject(); remembered != nil {
		t.Fatalf("saved start retained local project %#v", remembered)
	}

	selection = client.resolveCloudEnvironment(context.Background(), cloudThreadStart{projectCWD: repo})
	if selection == nil || selection.Project == nil || selection.Project.Name != "first" || selection.Project.CWD != "/workspace/org/first" {
		t.Fatalf("project selection = %#v", selection)
	}
	if remembered := client.rememberedHostProject(); remembered == nil || remembered.Name != "first" {
		t.Fatalf("remembered project = %#v", remembered)
	}

	selection = client.resolveCloudEnvironment(context.Background(), cloudThreadStart{
		savedEnvironment: "ws~asenvcfg_saved",
		projectCWD:       t.TempDir(),
	})
	if selection == nil || selection.SavedID != "ws~asenvcfg_saved" || selection.Project != nil {
		t.Fatalf("saved selection with project = %#v", selection)
	}
	if remembered := client.rememberedHostProject(); remembered == nil || remembered.Name != "first" {
		t.Fatalf("saved start changed the retained project to %#v", remembered)
	}
}
