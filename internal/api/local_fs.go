package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/fusemount"
)

type localDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type localBrowseResult struct {
	Path     string          `json:"path"`
	Parent   *string         `json:"parent"`
	Dirs     []localDirEntry `json:"dirs"`
	Volumes  []localDirEntry `json:"volumes,omitempty"`
	Exists   bool            `json:"exists"`
	Writable bool            `json:"writable"`
}

// browseDefaultPath 返回本地目录浏览器默认落点：
// STRM 输出目录、数据目录、FUSE 挂载根优先，再回退到常见挂载位置。
func (h *Handler) browseDefaultPath() string {
	seen := map[string]struct{}{}
	var candidates []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		candidates = append(candidates, p)
	}
	add(h.strmDir)
	add(h.dataDir)
	add(fusemount.MountRoot)

	// Windows 移植：无 Linux 挂载点语义，优先落到系统盘根（可看到全部盘符）。
	if runtime.GOOS == "windows" {
		if roots := listVolumeRoots(); len(roots) > 0 {
			return roots[0].Path
		}
	}
	for _, p := range []string{"/data", "/mnt", "/media", "/"} {
		add(p)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}
	return "/"
}

func (h *Handler) browseLocalFS(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		raw = h.browseDefaultPath()
	}
	// Windows 移植：Linux 版仅接受 / 开头的绝对路径；Windows 盘符路径（如 C:\Users）同样合法。
	if !filepath.IsAbs(raw) {
		writeJSON(w, http.StatusOK, Resp{
			Success:   false,
			Message:   "请使用绝对路径（以 / 开头）",
			ErrorType: string(domain.CodeValidation),
		})
		return
	}

	target, err := filepath.Abs(raw)
	if err != nil {
		writeErr(w, domain.Wrap(domain.CodeValidation, err))
		return
	}
	target = filepath.Clean(target)

	parent := parentPath(target)
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, Resp{
				Success: false,
				Message: "目录不存在: " + target,
				Data: localBrowseResult{
					Path:    target,
					Parent:  parent,
					Dirs:    []localDirEntry{},
					Volumes: listVolumeRoots(),
					Exists:  false,
				},
			})
			return
		}
		writeErr(w, domain.Wrap(domain.CodeDriverError, err))
		return
	}
	if !info.IsDir() {
		writeJSON(w, http.StatusOK, Resp{
			Success:   false,
			Message:   "该路径不是目录: " + target,
			ErrorType: string(domain.CodeValidation),
		})
		return
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		if os.IsPermission(err) {
			writeJSON(w, http.StatusOK, Resp{
				Success:   false,
				Message:   "无读取权限: " + target,
				ErrorType: string(domain.CodePermissionDenied),
			})
			return
		}
		writeErr(w, domain.Wrap(domain.CodeDriverError, err))
		return
	}

	dirs := make([]localDirEntry, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, localDirEntry{
			Name: e.Name(),
			Path: filepath.Join(target, e.Name()),
		})
	}
	// Windows 移植：盘符根（如 C:\）额外列出其它存在的盘符，实现跨盘切换。
	if runtime.GOOS == "windows" && isVolumeRoot(target) {
		for _, v := range listVolumeRoots() {
			if strings.EqualFold(v.Path, target) {
				continue
			}
			dirs = append(dirs, v)
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})

	writeOK(w, localBrowseResult{
		Path:     target,
		Parent:   parent,
		Dirs:     dirs,
		Volumes:  listVolumeRoots(),
		Exists:   true,
		Writable: true,
	})
}

func parentPath(path string) *string {
	clean := filepath.Clean(path)
	if clean == "/" {
		return nil
	}
	p := filepath.Dir(clean)
	// Windows 盘根（C:\）的 Dir 仍是自身，此时应视为无父级，避免前端死循环。
	if p == clean {
		return nil
	}
	return &p
}
