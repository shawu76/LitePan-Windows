package crosstransfer

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"

	"litepan/internal/domain"
	"litepan/internal/upload"
)

// EnqueuePlainInput 跨盘普传入队参数：不探测秒传指纹，源文件按「下载到临时目录再上传目标盘」的 relay 任务入队。
type EnqueuePlainInput struct {
	SourceAccountID   int64
	SourceAccountName string
	SourceDriverType  string
	TargetAccountID   int64
	TargetAccountName string
	TargetDriverType  string
	TargetParentID    string
	TargetDisplayPath string
	Sources           []ScanRoot
	Conflict          string
}

// EnqueuePlainResult 入队汇总，失败原因只给简要说明，明细到任务面板查看。
type EnqueuePlainResult struct {
	BatchID       string `json:"batch_id,omitempty"`
	Enqueued      int    `json:"enqueued"`
	Skipped       int    `json:"skipped"`
	Failed        int    `json:"failed"`
	Truncated     bool   `json:"truncated"`
	Message       string `json:"message,omitempty"`
	FailedName    string `json:"failed_name,omitempty"`
	FailedMessage string `json:"failed_message,omitempty"`
}

// EnqueuePlain 递归枚举各源目录（上限同秒传扫描），按源结构在目标目录建镜像目录并创建 relay 上传任务；
// 入队即返回，任务由 upload.Manager 持久化执行，不依赖浏览器连接。
func (s *Service) EnqueuePlain(ctx context.Context, in EnqueuePlainInput) (*EnqueuePlainResult, error) {
	return s.enqueuePlain(ctx, in, nil)
}

// EnqueuePlainStream 在扫描和入队过程中持续返回真实进度。
func (s *Service) EnqueuePlainStream(ctx context.Context, in EnqueuePlainInput, emit func(StreamEvent) error) error {
	// 浏览器离开后写回可能失败，但已开始的持久化任务入队不应中断。
	report := func(event StreamEvent) error {
		_ = emit(event)
		return nil
	}
	result, err := s.enqueuePlain(ctx, in, report)
	if err != nil {
		return err
	}
	_ = emit(StreamEvent{"event": "end", "result": result})
	return nil
}

func (s *Service) enqueuePlain(ctx context.Context, in EnqueuePlainInput, emit func(StreamEvent) error) (*EnqueuePlainResult, error) {
	conflict := normalizeConflictPolicy(in.Conflict)
	roots, err := normalizeScanRoots(in.Sources)
	if err != nil {
		return nil, err
	}
	if s.uploads == nil {
		return nil, domain.Errorf(domain.CodeInternal, "上传服务未就绪")
	}

	scan, err := s.enumeratePlainSourcesProgress(ctx, in.SourceAccountID, roots, func(directories, files int) error {
		if emit == nil {
			return nil
		}
		return emit(StreamEvent{"event": "progress", "stage": "scan", "directories": directories, "files": files})
	})
	if err != nil {
		return nil, err
	}

	batchID := uuid.NewString()
	res := &EnqueuePlainResult{BatchID: batchID}
	dirCache := map[string]string{"": in.TargetParentID}
	// 同名检查按目录缓存目标已有文件名，避免同目录每个文件都触发一次远程 List。
	nameCache := make(map[string]map[string]struct{})
	for index, f := range scan.files {
		if emit != nil && index%10 == 0 {
			if err := emit(StreamEvent{
				"event": "progress", "stage": "enqueue", "total": len(scan.files),
				"processed": index, "enqueued": res.Enqueued, "skipped": res.Skipped, "failed": res.Failed,
			}); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		folderID, ensureErr := EnsureTargetDir(ctx, s.files, in.TargetAccountID, in.TargetParentID, f.relDir, dirCache, nil)
		if ensureErr != nil {
			res.Failed++
			res.recordFailure(f, ensureErr)
			continue
		}
		if conflict == "skip" {
			exists, checkErr := s.targetNameExists(ctx, in.TargetAccountID, folderID, f.name, nameCache)
			if checkErr != nil {
				res.Failed++
				res.recordFailure(f, checkErr)
				continue
			}
			if exists {
				res.Skipped++
				continue
			}
		}
		if _, createErr := s.enqueueRelay(ctx, upload.CreateParams{
			BatchID:           batchID,
			BatchName:         "跨盘普传",
			AccountID:         in.TargetAccountID,
			AccountName:       in.TargetAccountName,
			DriverType:        in.TargetDriverType,
			FileName:          f.name,
			SourceAccountID:   in.SourceAccountID,
			SourceAccountName: in.SourceAccountName,
			SourceDriverType:  in.SourceDriverType,
			SourceFileID:      f.id,
			RelPath:           joinPlainRelPath(f.relDir, f.name),
			RelDir:            f.relDir,
			TargetPath:        in.TargetParentID,
			TargetDisplayPath: in.TargetDisplayPath,
			TotalBytes:        f.size,
			ConflictPolicy:    conflict,
		}); createErr != nil {
			res.Failed++
			res.recordFailure(f, createErr)
			continue
		}
		rememberTargetName(nameCache, folderID, f.name)
		res.Enqueued++
	}
	if emit != nil {
		if err := emit(StreamEvent{
			"event": "progress", "stage": "enqueue", "total": len(scan.files),
			"processed": len(scan.files), "enqueued": res.Enqueued, "skipped": res.Skipped, "failed": res.Failed,
		}); err != nil {
			return nil, err
		}
	}

	res.Truncated = scan.truncated
	switch {
	case scan.truncated:
		res.Message = scan.truncatedReason
	case res.Failed > 0 && res.FailedMessage != "":
		res.Message = fmt.Sprintf("%d 个文件入队失败，首个错误：%s", res.Failed, res.FailedMessage)
	}
	return res, nil
}

func rememberTargetName(cache map[string]map[string]struct{}, folderID, name string) {
	if names := cache[folderID]; names != nil {
		names[name] = struct{}{}
	}
}

func (r *EnqueuePlainResult) recordFailure(f plainScanFile, err error) {
	if r.FailedName == "" {
		r.FailedName = f.relPath
		r.FailedMessage = err.Error()
	}
}

func joinPlainRelPath(relDir, name string) string {
	if relDir == "" {
		return name
	}
	return relDir + "/" + name
}

// targetNameExists 按目录缓存目标已有文件名集合后做同名判断。
func (s *Service) targetNameExists(
	ctx context.Context,
	accountID int64,
	folderID, name string,
	cache map[string]map[string]struct{},
) (bool, error) {
	names, ok := cache[folderID]
	if !ok {
		items, err := s.files.List(ctx, accountID, folderID, false)
		if err != nil {
			return false, err
		}
		names = make(map[string]struct{}, len(items))
		for _, item := range items {
			if !item.IsDir {
				names[item.Name] = struct{}{}
			}
		}
		cache[folderID] = names
	}
	_, exists := names[name]
	return exists, nil
}

type plainScanFile struct {
	id      string
	name    string
	size    int64
	relDir  string
	relPath string
}

type plainScan struct {
	files           []plainScanFile
	truncated       bool
	truncatedReason string
}

type plainScanNode struct {
	id     string
	relDir string
	depth  int
}

type plainListOutcome struct {
	node  plainScanNode
	items []domain.FileItem
	err   error
}

// enumeratePlainSourcesProgress BFS 枚举源目录文件，progress 回传进度，上限与秒传扫描一致。
func (s *Service) enumeratePlainSourcesProgress(ctx context.Context, sourceAccountID int64, roots []ScanRoot, progress func(int, int) error) (*plainScan, error) {
	acc := &plainScan{}
	queue := make([]plainScanNode, 0, len(roots))
	for _, source := range roots {
		queue = append(queue, plainScanNode{
			id:     source.ParentID,
			relDir: strings.TrimSuffix(sourceRootPrefix(source.DisplayPath), "/"),
			depth:  0,
		})
	}
	listedDirs := 0
	for len(queue) > 0 && acc.truncatedReason == "" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remainingDirs := maxScanDirs - listedDirs
		if remainingDirs <= 0 {
			acc.truncated = true
			acc.truncatedReason = fmt.Sprintf("目录数量超过 %d 个，已停止枚举，请缩小选择范围", maxScanDirs)
			break
		}
		batchSize := min(len(queue), scanDirConcurrency, remainingDirs)
		batch := queue[:batchSize]
		queue = queue[batchSize:]

		outcomes := make([]plainListOutcome, len(batch))
		var wg sync.WaitGroup
		for i, node := range batch {
			wg.Go(func() {
				items, err := s.files.List(ctx, sourceAccountID, node.id, false)
				outcomes[i] = plainListOutcome{node: node, items: items, err: err}
			})
		}
		wg.Wait()

		for _, outcome := range outcomes {
			if outcome.err != nil {
				return nil, fmt.Errorf("读取源目录 %s 失败: %w", outcome.node.relDir, outcome.err)
			}
			listedDirs++
			childDirs := make([]plainScanNode, 0, 8)
			for _, item := range outcome.items {
				if item.IsDir {
					child := plainScanNode{
						id:     item.ID,
						relDir: joinPlainRelDir(outcome.node.relDir, item.Name),
						depth:  outcome.node.depth + 1,
					}
					childDirs = append(childDirs, child)
				} else {
					if len(acc.files) >= maxScanFiles {
						acc.truncated = true
						acc.truncatedReason = fmt.Sprintf("文件数量超过 %d 个，已停止枚举，请分批选择目录", maxScanFiles)
						break
					}
					acc.files = append(acc.files, plainScanFile{
						id:      item.ID,
						name:    item.Name,
						size:    item.Size,
						relDir:  outcome.node.relDir,
						relPath: joinPlainRelPath(outcome.node.relDir, item.Name),
					})
				}
			}
			if acc.truncatedReason != "" {
				break
			}
			for _, child := range childDirs {
				if child.depth > maxScanDepth {
					acc.truncated = true
					acc.truncatedReason = fmt.Sprintf("目录层级超过 %d 层，已停止枚举，请缩小选择范围", maxScanDepth)
					break
				}
				queue = append(queue, child)
			}
			if acc.truncatedReason != "" {
				break
			}
			if progress != nil {
				if err := progress(listedDirs, len(acc.files)); err != nil {
					return nil, err
				}
			}
		}
	}
	return acc, nil
}

func joinPlainRelDir(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}
