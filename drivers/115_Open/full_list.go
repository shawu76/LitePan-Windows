package pan115open

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	fullListPageSize       = 1150
	fullListEmptyRetryWait = 250 * time.Millisecond
)

// ListAllFiles 使用 cur=0 让服务端递归展开 rootID 下全部文件，分页拉取。
// 该模式不返回文件夹，条目自带 pid，由上层结合 pid→路径 缓存还原目录结构。
// 分页仍只以连续空页为结束信号，不用 Count 或短页提前结束；
// Count 仅在结束时用于拦截“明确少于服务端声明数量”的不完整清单。
func (d *Driver) ListAllFiles(ctx context.Context, rootID string) ([]driver.FullListEntry, error) {
	root := d.normalizeParent(rootID)
	return collectFullListPages(ctx, func(ctx context.Context, offset, limit int) (listPageResp, error) {
		query := urlValues(map[string]string{
			"cid":      root,
			"limit":    strconv.Itoa(limit),
			"offset":   strconv.Itoa(offset),
			"show_dir": "0",
			"cur":      "0",
		})
		var page listPageResp
		if err := d.apiCallFull(ctx, http.MethodGet, pathList, query, nil, &page); err != nil {
			return listPageResp{}, err
		}
		return page, nil
	})
}

type fullListPageFetcher func(context.Context, int, int) (listPageResp, error)

func collectFullListPages(ctx context.Context, fetch fullListPageFetcher) ([]driver.FullListEntry, error) {
	var entries []driver.FullListEntry
	offset := 0
	expectedCount := int64(0)
	seenIDs := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := fetch(ctx, offset, fullListPageSize)
		if err != nil {
			return nil, err
		}
		if page.Count > expectedCount {
			expectedCount = page.Count
		}
		if len(page.Data) == 0 {
			timer := time.NewTimer(fullListEmptyRetryWait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			page, err = fetch(ctx, offset, fullListPageSize)
			if err != nil {
				return nil, err
			}
			if page.Count > expectedCount {
				expectedCount = page.Count
			}
			if len(page.Data) == 0 {
				if expectedCount > int64(len(seenIDs)) {
					return nil, domain.Errorf(domain.CodeDriverError,
						"115 全量清单不完整：应有 %d 个文件，实际只获取 %d 个，已停止扫描",
						expectedCount, len(seenIDs))
				}
				break
			}
		}
		newIDs := 0
		for _, f := range page.Data {
			fileID := strings.TrimSpace(f.entryID())
			if fileID == "" {
				return nil, domain.Errorf(domain.CodeDriverError, "115 全量清单返回了缺少文件 ID 的条目，已停止扫描")
			}
			if _, duplicate := seenIDs[fileID]; duplicate {
				continue
			}
			seenIDs[fileID] = struct{}{}
			newIDs++
			if isTrashed(f) {
				continue
			}
			entries = append(entries, driver.FullListEntry{
				FileID:   fileID,
				ParentID: f.parentID(),
				Name:     f.entryName(),
				Size:     f.entrySize(),
				Sha1:     strings.TrimSpace(f.Sha1),
				PickCode: f.pickCode(),
				MTime:    f.entryMTime(),
			})
		}
		if newIDs == 0 {
			return nil, domain.Errorf(domain.CodeDriverError, "115 全量清单分页重复，已停止扫描以避免使用不完整结果")
		}
		offset += len(page.Data)
	}
	return entries, nil
}

// ResolveDirPath 通过 /open/folder/get_info 拼出相对于账号挂载根的目录路径。
func (d *Driver) ResolveDirPath(ctx context.Context, dirID string) (string, error) {
	id := strings.TrimSpace(dirID)
	rootID := d.rootID()
	if id == "" || id == "0" || id == rootID {
		return "", nil
	}
	query := urlValues(map[string]string{"file_id": id})
	var info fileEntry
	if err := d.apiCall(ctx, http.MethodGet, pathFileInfo, query, nil, &info); err != nil {
		return "", err
	}
	if info.entryID() == "" {
		return "", domain.Errf(domain.CodeNotFound)
	}
	path, ok := buildDirPath(info.Paths, info.entryName(), rootID)
	if !ok {
		return "", domain.Errorf(domain.CodeDriverError, "115 目录不在账号挂载根下，已停止扫描")
	}
	return path, nil
}

func buildDirPath(paths []dirPathEntry, selfName, rootID string) (string, bool) {
	segs := make([]string, 0, len(paths)+1)
	foundRoot := rootID == "0"
	for _, p := range paths {
		id := strings.TrimSpace(p.FileID.String())
		if id == rootID {
			foundRoot = true
			segs = segs[:0]
			continue
		}
		if id == "0" || !foundRoot {
			continue
		}
		if name := pathSegmentName(p.FileName); name != "" {
			segs = append(segs, name)
		}
	}
	if !foundRoot {
		return "", false
	}
	if name := pathSegmentName(selfName); name != "" {
		segs = append(segs, name)
	}
	return strings.Join(segs, "/"), true
}

// pathSegmentName 把目录名里会被误当成层级的分隔符替换掉。
//
// 网盘目录名允许包含 "/"。直接把名字拼进以 "/" 分隔的路径里，上层（STRM 扫描）
// 就再也分不清「一个名字带斜杠的目录」和「多层目录」，会把本地 strm 建到错误的多层
// 目录下，并把正确位置的文件当成过期清理掉。段内去掉分隔符即可杜绝伪造层级。
func pathSegmentName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "/", "_")
	return strings.ReplaceAll(name, "\\", "_")
}

var (
	_ driver.FullListLister = (*Driver)(nil)
)
