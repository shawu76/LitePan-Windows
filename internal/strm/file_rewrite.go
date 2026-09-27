package strm

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// rewriteStrmFiles 遍历 STRM 根目录，并只改写文件首行。
// transform 返回 matched=false 时保持原文件不变。
func rewriteStrmFiles(root string, transform func(string) (line string, matched bool)) (total, matched, updated int, err error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "strm"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 0, 0, 0, err
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".strm") {
			return nil
		}
		total++
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		lines := strings.Split(string(content), "\n")
		first := strings.TrimSpace(lines[0])
		if first == "" {
			return nil
		}
		replaced, ok := transform(first)
		if !ok {
			return nil
		}
		matched++
		if replaced == first {
			return nil
		}
		lines[0] = replaced
		out := strings.Join(lines, "\n")
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if writeErr := os.WriteFile(path, []byte(out), 0o644); writeErr == nil {
			updated++
		}
		return nil
	})
	return total, matched, updated, err
}
