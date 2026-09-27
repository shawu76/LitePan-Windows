package strm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
)

type repairPathFiles struct{}

func (repairPathFiles) List(context.Context, int64, string, bool) ([]domain.FileItem, error) {
	return nil, nil
}

func (repairPathFiles) Info(context.Context, int64, string) (*domain.FileItem, error) {
	return nil, domain.Errf(domain.CodeNotFound)
}

func (repairPathFiles) ResolvePath(_ context.Context, accountID int64, rootID, relativePath string) (*domain.FileItem, error) {
	if accountID == 2 && rootID == "new-root" && relativePath == "电影/测试.mkv" {
		return &domain.FileItem{ID: "new-file", Name: "测试.mkv"}, nil
	}
	return nil, domain.Errf(domain.CodeNotFound)
}

func TestRepairAccountReferencesKeepsPathFormat(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "library")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	strmPath := filepath.Join(outputDir, "测试.strm")
	oldURL := BuildPathPlayURL("http://old.test", 1, "old-root", "电影/测试.mkv", "测试.mkv", "old-token", false, nil)
	if err := os.WriteFile(strmPath, []byte(oldURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RepairAccountReferences(context.Background(), repairPathFiles{}, root, "http://new.test", "new-token", false, nil, AccountRepairInput{
		AccountID: 2, OldAccountID: 1, ParentID: "new-root", OutputFolder: "library", Recursive: true,
	})
	if err != nil {
		t.Fatalf("RepairAccountReferences() error = %v", err)
	}
	if result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("result = %#v", result)
	}
	content, err := os.ReadFile(strmPath)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := ParsePlayReference(string(content))
	if !ok || !ref.PathBased || ref.AccountID != 2 || ref.RootID != "new-root" || ref.RelativePath != "电影/测试.mkv" || ref.Token != "new-token" {
		t.Fatalf("repaired reference = %#v, ok=%v", ref, ok)
	}
}
