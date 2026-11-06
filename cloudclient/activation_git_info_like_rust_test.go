package cloudclient

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func appendStringField(dst []byte, number protowire.Number, value string) []byte {
	dst = protowire.AppendTag(dst, number, protowire.BytesType)
	return protowire.AppendString(dst, value)
}

func appendMessageField(dst []byte, number protowire.Number, message []byte) []byte {
	dst = protowire.AppendTag(dst, number, protowire.BytesType)
	return protowire.AppendBytes(dst, message)
}

func appendVarintField(dst []byte, number protowire.Number, value uint64) []byte {
	dst = protowire.AppendTag(dst, number, protowire.VarintType)
	return protowire.AppendVarint(dst, value)
}

// Rust #59828: the cloud thread snapshot carries optional repository metadata.
func TestDecodeThreadSnapshotGitInfoLikeRust(t *testing.T) {
	git := appendStringField(nil, 1, "main")
	git = appendStringField(git, 2, "https://example.com/repo.git")
	git = appendStringField(git, 3, "abc123")

	data := appendStringField(nil, 14, "thread-1")
	data = appendStringField(data, 27, "session-1")
	data = appendStringField(data, 21, "preview")
	data = appendVarintField(data, 9, 0)
	data = appendStringField(data, 16, "openai")
	data = appendVarintField(data, 5, 1)
	data = appendVarintField(data, 32, 2)
	data = appendStringField(data, 6, "/repo")
	data = appendStringField(data, 4, "0.1.0")
	data = appendMessageField(data, 29, appendMessageField(nil, 2, nil))
	data = appendMessageField(data, 28, appendVarintField(nil, 1, 1))
	data = appendMessageField(data, 12, git)

	snapshot, err := decodeThreadSnapshot(data)
	if err != nil {
		t.Fatalf("decodeThreadSnapshot() error = %v", err)
	}
	if snapshot.GitInfo == nil {
		t.Fatal("snapshot lost git info")
	}
	if snapshot.GitInfo.Branch == nil || *snapshot.GitInfo.Branch != "main" {
		t.Fatalf("branch = %#v", snapshot.GitInfo.Branch)
	}
	if snapshot.GitInfo.OriginURL == nil || *snapshot.GitInfo.OriginURL != "https://example.com/repo.git" {
		t.Fatalf("origin = %#v", snapshot.GitInfo.OriginURL)
	}
	if snapshot.GitInfo.SHA == nil || *snapshot.GitInfo.SHA != "abc123" {
		t.Fatalf("sha = %#v", snapshot.GitInfo.SHA)
	}

	withoutGit := appendStringField(nil, 14, "thread-2")
	withoutGit = appendStringField(withoutGit, 27, "session-2")
	withoutGit = appendStringField(withoutGit, 21, "preview")
	withoutGit = appendVarintField(withoutGit, 9, 0)
	withoutGit = appendStringField(withoutGit, 16, "openai")
	withoutGit = appendVarintField(withoutGit, 5, 1)
	withoutGit = appendVarintField(withoutGit, 32, 2)
	withoutGit = appendStringField(withoutGit, 6, "/repo")
	withoutGit = appendStringField(withoutGit, 4, "0.1.0")
	withoutGit = appendMessageField(withoutGit, 29, appendMessageField(nil, 2, nil))
	withoutGit = appendMessageField(withoutGit, 28, appendVarintField(nil, 1, 1))
	plain, err := decodeThreadSnapshot(withoutGit)
	if err != nil {
		t.Fatalf("decodeThreadSnapshot() error = %v", err)
	}
	if plain.GitInfo != nil {
		t.Fatalf("snapshot invented git info: %#v", plain.GitInfo)
	}
}
