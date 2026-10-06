//go:build windows && !fuse

package fuse

import (
	"context"
	"errors"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	cgofs "github.com/winfsp/cgofuse/fuse"

	"litepan/internal/domain"
	"litepan/internal/playback"
	"litepan/internal/upload"
)

// winFuseFS 是基于 cgofuse（WinFsp 后端）的网盘文件系统实现。
// 语义与 go-fuse 版 nodes.go / write_support.go 对齐：
// 网盘文件只读，通过 staging 临时文件支持写入并在关闭时投递上传任务。
type winFuseFS struct {
	cgofs.FileSystemBase
	deps   Deps
	b      *backend
	mount  *domain.FuseMount
	dirs   sync.Map // path("/a/b") -> *dirCacheEntry
	staging sync.Map // path -> *stagingFileWin
	fhMu   sync.Mutex
	nextFH uint64
	handles map[uint64]*winHandle

	// mountReady 在 WinFsp 挂载成功（Init 回调）时被 close，
	// 用于让 Mount 调用方不必阻塞在 host.Mount 的 FUSE 主循环上。
	mountReady chan struct{}
	// mountDone 在 host.Mount 返回后写入挂载结果（true=成功保持挂载；false=挂载失败）。
	mountDone chan bool
}

type dirCacheEntry struct {
	dirID string
	items []domain.FileItem
}

// backend 的 Windows 版（backend.go 仅 fuse tag 编译）。
type backend struct {
	deps  Deps
	mount *domain.FuseMount
}

func (b *backend) accountID() int64 {
	if b == nil || b.mount == nil {
		return 0
	}
	return b.mount.AccountID
}

func newWinFuseFS(deps Deps, mount *domain.FuseMount) *winFuseFS {
	return &winFuseFS{
		deps:       deps,
		b:          &backend{deps: deps, mount: mount},
		mount:      mount,
		handles:    make(map[uint64]*winHandle),
		mountReady: make(chan struct{}),
		mountDone:  make(chan bool, 1),
	}
}

// Init 在 WinFsp 挂载创建成功时回调（host.Mount 阻塞于 FUSE 主循环内），
// 关闭 mountReady 通知调用方挂载已就绪。
func (f *winFuseFS) Init() {
	select {
	case <-f.mountReady:
	default:
		close(f.mountReady)
	}
}

// ---- 路径工具 ----

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func joinPath(parent, name string) string {
	if parent == "" || parent == "/" {
		return "/" + name
	}
	return strings.TrimSuffix(parent, "/") + "/" + name
}

func parentOf(p string) (string, string) {
	p = strings.TrimSuffix(p, "/")
	if p == "" || p == "/" {
		return "/", strings.TrimPrefix(p, "/")
	}
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/", strings.TrimPrefix(p, "/")
	}
	return p[:i], p[i+1:]
}

func normalizeChildName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return "", false
	}
	return name, true
}

// ---- inode / attr ----

func fileIno(accountID int64, fileID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte{byte(accountID), byte(accountID >> 8), byte(accountID >> 16), byte(accountID >> 24)})
	_, _ = h.Write([]byte(fileID))
	return h.Sum64()
}

func fillWinStat(st *cgofs.Stat_t, b *backend, isDir bool, size int64, modTime time.Time, ino uint64) {
	mode := uint32(cgofs.S_IFREG) | (b.mount.FileMode & 0o7777)
	nlink := uint32(1)
	if isDir {
		mode = uint32(cgofs.S_IFDIR) | (b.mount.DirMode & 0o7777)
		nlink = 2
	}
	st.Mode = mode
	st.Nlink = nlink
	st.Uid = b.mount.UID
	st.Gid = b.mount.GID
	st.Size = size
	if !modTime.IsZero() {
		sec := modTime.Unix()
		st.Atim.Sec = sec
		st.Mtim.Sec = sec
		st.Ctim.Sec = sec
	} else {
		sec := time.Now().Unix()
		st.Atim.Sec = sec
		st.Mtim.Sec = sec
		st.Ctim.Sec = sec
	}
	st.Blksize = 4096
	if size > 0 {
		st.Blocks = (size + 4095) / 4096
	}
	st.Ino = ino
}

func (f *winFuseFS) rootItem() domain.FileItem {
	name := strings.TrimSpace(f.mount.RootPath)
	if name == "" {
		name = strings.TrimSpace(f.mount.RootItemID)
	}
	return domain.FileItem{ID: f.mount.RootItemID, Name: name, IsDir: true}
}

// ---- 目录缓存与解析 ----

func (f *winFuseFS) listDir(ctx context.Context, path string) ([]domain.FileItem, error) {
	if v, ok := f.dirs.Load(path); ok {
		return v.(*dirCacheEntry).items, nil
	}
	var dirID string
	if path == "/" {
		dirID = f.mount.RootItemID
	} else {
		item, _, err := f.resolveRemote(ctx, path)
		if err != nil {
			return nil, err
		}
		if !item.IsDir {
			return nil, domain.Errorf(domain.CodeValidation, "路径不是目录: %s", path)
		}
		dirID = item.ID
	}
	items, err := f.deps.Files.List(ctx, f.b.accountID(), dirID, false)
	if err != nil {
		return nil, err
	}
	f.dirs.Store(path, &dirCacheEntry{dirID: dirID, items: items})
	return items, nil
}

func (f *winFuseFS) invalidateDir(path string) {
	f.dirs.Delete(path)
}

// resolveRemote 按路径逐级查找网盘文件，返回末级 item 与其父目录 ID。
func (f *winFuseFS) resolveRemote(ctx context.Context, path string) (domain.FileItem, string, error) {
	segs := splitPath(path)
	if len(segs) == 0 {
		return f.rootItem(), f.mount.RootItemID, nil
	}
	parentID := f.mount.RootItemID
	cur := "/"
	last := domain.FileItem{}
	for i, seg := range segs {
		items, err := f.listDir(ctx, cur)
		if err != nil {
			return domain.FileItem{}, "", err
		}
		var found *domain.FileItem
		for j := range items {
			if items[j].Name == seg {
				found = &items[j]
				break
			}
		}
		if found == nil {
			return domain.FileItem{}, "", domain.Errorf(domain.CodeNotFound, "路径不存在: %s", path)
		}
		last = *found
		if i == len(segs)-1 {
			return last, parentID, nil
		}
		parentID = last.ID
		cur = joinPath(cur, seg)
	}
	return last, parentID, nil
}

func (f *winFuseFS) lookupChildItem(ctx context.Context, parentPath, name string) (domain.FileItem, bool, error) {
	items, err := f.listDir(ctx, parentPath)
	if err != nil {
		return domain.FileItem{}, false, err
	}
	for _, it := range items {
		if it.Name == name {
			return it, true, nil
		}
	}
	return domain.FileItem{}, false, nil
}

// ---- staging（本地暂存写） ----

type stagingFileWin struct {
	mu          sync.RWMutex
	name        string
	tempPath    string
	size        int64
	modTime     time.Time
	parentID    string
	displayPath string
	uploads     UploadManager
	taskID      string
}

func (f *winFuseFS) getStaging(path string) *stagingFileWin {
	if v, ok := f.staging.Load(path); ok {
		return v.(*stagingFileWin)
	}
	return nil
}

// ---- FileSystemInterface ----

func (f *winFuseFS) Getattr(path string, st *cgofs.Stat_t, fh uint64) int {
	if st == nil {
		return -cgofs.EIO
	}
	if stg := f.getStaging(path); stg != nil {
		stg.mu.RLock()
		fillWinStat(st, f.b, false, stg.size, stg.modTime, fileIno(f.b.accountID(), "pending:"+stg.tempPath))
		stg.mu.RUnlock()
		return 0
	}
	ctx := context.Background()
	item, _, err := f.resolveRemote(ctx, path)
	if err != nil {
		if isNotFound(err) {
			return -cgofs.ENOENT
		}
		return winErr(err)
	}
	fillWinStat(st, f.b, item.IsDir, item.Size, item.ModTime, fileIno(f.b.accountID(), item.ID))
	return 0
}

func (f *winFuseFS) Opendir(path string) (int, uint64) {
	if path == "/" {
		return 0, 0
	}
	ctx := context.Background()
	item, _, err := f.resolveRemote(ctx, path)
	if err != nil {
		if isNotFound(err) {
			return -cgofs.ENOENT, 0
		}
		return winErr(err), 0
	}
	if !item.IsDir {
		return -cgofs.ENOTDIR, 0
	}
	return 0, 0
}

func (f *winFuseFS) Readdir(path string, fill func(name string, st *cgofs.Stat_t, ofst int64) bool, _ int64, _ uint64) int {
	ctx := context.Background()
	// 刷新该目录缓存，保证刚上传/刚创建的文件立即可见。
	f.invalidateDir(path)
	items, err := f.listDir(ctx, path)
	if err != nil {
		if isNotFound(err) {
			return -cgofs.ENOENT
		}
		return winErr(err)
	}
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		seen[it.Name] = struct{}{}
		if !fill(it.Name, nil, 0) {
			return 0
		}
	}
	// 合并本会话内已创建、但网盘列表尚未可见的 staging 文件。
	f.staging.Range(func(key, value any) bool {
		parent, name := parentOf(key.(string))
		if parent != path {
			return true
		}
		if _, ok := seen[name]; ok {
			return true
		}
		seen[name] = struct{}{}
		return fill(name, nil, 0)
	})
	return 0
}

func (f *winFuseFS) Open(path string, flags int) (int, uint64) {
	if flags&cgofs.O_ACCMODE != cgofs.O_RDONLY {
		return -cgofs.EROFS, 0
	}
	if stg := f.getStaging(path); stg != nil {
		stg.mu.RLock()
		tempPath := stg.tempPath
		stg.mu.RUnlock()
		file, err := os.Open(tempPath)
		if err != nil {
			if os.IsNotExist(err) {
				return -cgofs.ENOENT, 0
			}
			return -cgofs.EIO, 0
		}
		return 0, f.allocFH(&winHandle{kind: winKindStagingRead, file: file})
	}
	ctx := context.Background()
	item, _, err := f.resolveRemote(ctx, path)
	if err != nil {
		if isNotFound(err) {
			return -cgofs.ENOENT, 0
		}
		return winErr(err), 0
	}
	if item.IsDir {
		return -cgofs.EISDIR, 0
	}
	if f.deps.Playback == nil {
		return -cgofs.EIO, 0
	}
	reader, err := f.deps.Playback.OpenRemoteReader(ctx, f.b.accountID(), item.ID, "")
	if err != nil {
		return winErr(err), 0
	}
	return 0, f.allocFH(&winHandle{kind: winKindRemote, remote: &winRemoteHandle{reader: reader}})
}

func (f *winFuseFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
	h := f.getFH(fh)
	if h == nil {
		return -cgofs.EBADF
	}
	switch h.kind {
	case winKindRemote:
		if h.remote == nil || h.remote.reader == nil {
			return -cgofs.EIO
		}
		var n int
		var err error
		if rc := f.deps.ReadCache; rc != nil && rc.Enabled(context.Background()) {
			n, err = rc.ReadAt(context.Background(), f.b.accountID(), "", buff, ofst, h.remote.reader.ReadAt)
		} else {
			n, err = h.remote.reader.ReadAt(buff, ofst)
		}
		if n > 0 {
			return n
		}
		if err == io.EOF {
			return 0
		}
		if err != nil {
			return -cgofs.EIO
		}
		return 0
	case winKindStagingRead:
		if h.file == nil {
			return -cgofs.EBADF
		}
		n, err := h.file.ReadAt(buff, ofst)
		if n > 0 {
			return n
		}
		if err == nil || err == io.EOF {
			return 0
		}
		if os.IsNotExist(err) {
			return -cgofs.ENOENT
		}
		return -cgofs.EIO
	default:
		return -cgofs.EBADF
	}
}

func (f *winFuseFS) Create(path string, flags int, mode uint32) (int, uint64) {
	parent, name := parentOf(path)
	name, ok := normalizeChildName(name)
	if !ok {
		return -cgofs.EINVAL, 0
	}
	if f.deps.Uploads == nil {
		return -cgofs.EROFS, 0
	}
	ctx := context.Background()
	if _, found, err := f.lookupChildItem(ctx, parent, name); err != nil {
		return winErr(err), 0
	} else if found {
		return -cgofs.EEXIST, 0
	}
	tmp, tmpPath, cleanup, untrack, err := createWinTempFile(f.deps.Uploads, name)
	if err != nil {
		return winErr(err), 0
	}
	display := strings.TrimPrefix(parent, "/")
	node := &stagingFileWin{
		name:        name,
		tempPath:    tmpPath,
		modTime:     time.Now(),
		parentID:    f.dirIDFor(parent),
		displayPath: display,
	}
	if node.parentID == "" {
		node.parentID = f.mount.RootItemID
	}
	f.staging.Store(path, node)
	return 0, f.allocFH(&winHandle{kind: winKindStagingWrite, staging: &winStagingHandle{
		node:              node,
		uploads:           f.deps.Uploads,
		accountID:         f.b.accountID(),
		parentID:          node.parentID,
		targetDisplayPath: display,
		fileName:          name,
		tempPath:          tmpPath,
		file:              tmp,
		cleanup:           cleanup,
		untrack:           untrack,
		flags:             flags,
	}})
}

// dirIDFor 解析父目录路径对应的网盘目录 ID（尽量走缓存）。
func (f *winFuseFS) dirIDFor(path string) string {
	if path == "" || path == "/" {
		return f.mount.RootItemID
	}
	if v, ok := f.dirs.Load(path); ok {
		return v.(*dirCacheEntry).dirID
	}
	item, _, err := f.resolveRemote(context.Background(), path)
	if err != nil {
		return ""
	}
	return item.ID
}

func (f *winFuseFS) Write(path string, buff []byte, ofst int64, fh uint64) int {
	h := f.getFH(fh)
	if h == nil || h.kind != winKindStagingWrite || h.staging == nil {
		return -cgofs.EBADF
	}
	h.staging.mu.Lock()
	file := h.staging.file
	h.staging.mu.Unlock()
	if file == nil {
		return -cgofs.EBADF
	}
	n, err := file.WriteAt(buff, ofst)
	if n > 0 && h.staging.node != nil {
		h.staging.node.updateSize(ofst + int64(n))
	}
	if err != nil {
		return winErr(err)
	}
	return n
}

func (f *winFuseFS) Flush(path string, fh uint64) int {
	h := f.getFH(fh)
	if h == nil || h.kind != winKindStagingWrite || h.staging == nil {
		return -cgofs.EBADF
	}
	return h.staging.flush()
}

func (f *winFuseFS) Fsync(path string, datasync bool, fh uint64) int {
	h := f.getFH(fh)
	if h == nil || h.kind != winKindStagingWrite || h.staging == nil {
		return -cgofs.EBADF
	}
	return h.staging.fsync()
}

func (f *winFuseFS) Release(path string, fh uint64) int {
	h := f.freeFH(fh)
	if h == nil {
		return 0
	}
	switch h.kind {
	case winKindRemote:
		if h.remote != nil && h.remote.reader != nil {
			_ = h.remote.reader.Close()
		}
	case winKindStagingRead:
		if h.file != nil {
			_ = h.file.Close()
		}
	case winKindStagingWrite:
		if h.staging != nil {
			h.staging.release()
		}
	}
	return 0
}

func (f *winFuseFS) Truncate(path string, size int64, fh uint64) int {
	stg := f.getStaging(path)
	if stg == nil {
		return -cgofs.EROFS
	}
	stg.mu.RLock()
	tempPath := stg.tempPath
	stg.mu.RUnlock()
	if tempPath == "" {
		return -cgofs.ENOENT
	}
	if err := os.Truncate(tempPath, size); err != nil {
		return winErr(err)
	}
	stg.updateSize(size)
	return 0
}

func (f *winFuseFS) Mkdir(path string, mode uint32) int {
	parent, name := parentOf(path)
	name, ok := normalizeChildName(name)
	if !ok {
		return -cgofs.EINVAL
	}
	ctx := context.Background()
	if _, found, err := f.lookupChildItem(ctx, parent, name); err != nil {
		return winErr(err)
	} else if found {
		return -cgofs.EEXIST
	}
	parentID := f.dirIDFor(parent)
	if parentID == "" {
		return -cgofs.ENOENT
	}
	item, err := f.deps.Files.CreateFolder(ctx, f.b.accountID(), parentID, name)
	if err != nil {
		return winErr(err)
	}
	f.invalidateDir(parent)
	_ = item
	return 0
}

func (f *winFuseFS) Unlink(path string) int {
	parent, name := parentOf(path)
	name, ok := normalizeChildName(name)
	if !ok {
		return -cgofs.EINVAL
	}
	ctx := context.Background()
	if stg := f.getStaging(path); stg != nil {
		code := f.unlinkStaging(stg)
		f.staging.Delete(path)
		f.invalidateDir(parent)
		return code
	}
	item, found, err := f.lookupChildItem(ctx, parent, name)
	if err != nil {
		return winErr(err)
	}
	if !found {
		return -cgofs.ENOENT
	}
	if item.IsDir {
		return -cgofs.EISDIR
	}
	parentID := f.dirIDFor(parent)
	if err := f.deps.Files.DeleteFiles(ctx, f.b.accountID(), []string{item.ID}, parentID); err != nil {
		return winErr(err)
	}
	f.invalidateDir(parent)
	return 0
}

func (f *winFuseFS) Rmdir(path string) int {
	parent, name := parentOf(path)
	name, ok := normalizeChildName(name)
	if !ok {
		return -cgofs.EINVAL
	}
	ctx := context.Background()
	item, found, err := f.lookupChildItem(ctx, parent, name)
	if err != nil {
		return winErr(err)
	}
	if !found {
		return -cgofs.ENOENT
	}
	if !item.IsDir {
		return -cgofs.ENOTDIR
	}
	parentID := f.dirIDFor(parent)
	if err := f.deps.Files.DeleteFiles(ctx, f.b.accountID(), []string{item.ID}, parentID); err != nil {
		return winErr(err)
	}
	f.invalidateDir(parent)
	return 0
}

func (f *winFuseFS) Rename(oldpath string, newpath string) int {
	oldParent, oldName := parentOf(oldpath)
	oldName, ok := normalizeChildName(oldName)
	if !ok {
		return -cgofs.EINVAL
	}
	newParent, newName := parentOf(newpath)
	newName, ok2 := normalizeChildName(newName)
	if !ok2 {
		return -cgofs.EINVAL
	}
	ctx := context.Background()
	// 目标名冲突检查。
	if _, found, err := f.lookupChildItem(ctx, newParent, newName); err != nil {
		return winErr(err)
	} else if found {
		return -cgofs.EEXIST
	}
	// 源优先取 staging（刚创建、网盘不可见）。
	if stg := f.getStaging(oldpath); stg != nil {
		code := f.renameStaging(stg, newParent, newName)
		if code == 0 {
			f.staging.Delete(oldpath)
			f.staging.Store(newpath, stg)
			f.invalidateDir(oldParent)
			f.invalidateDir(newParent)
		}
		return code
	}
	item, found, err := f.lookupChildItem(ctx, oldParent, oldName)
	if err != nil {
		return winErr(err)
	}
	if !found {
		return -cgofs.ENOENT
	}
	oldParentID := f.dirIDFor(oldParent)
	newParentID := f.dirIDFor(newParent)
	if newParentID == "" || oldParentID == "" {
		return -cgofs.ENOENT
	}
	if newParentID != oldParentID {
		if err := f.deps.Files.MoveFiles(ctx, f.b.accountID(), []string{item.ID}, newParentID, oldParentID); err != nil {
			return winErr(err)
		}
		if newName != oldName {
			if err := f.deps.Files.RenameFile(ctx, f.b.accountID(), item.ID, newName, newParentID); err != nil {
				return winErr(err)
			}
		}
	} else if newName != oldName {
		if err := f.deps.Files.RenameFile(ctx, f.b.accountID(), item.ID, newName, newParentID); err != nil {
			return winErr(err)
		}
	}
	f.invalidateDir(oldParent)
	f.invalidateDir(newParent)
	return 0
}

func (f *winFuseFS) Statfs(path string, st *cgofs.Statfs_t) int {
	if st == nil {
		return -cgofs.EIO
	}
	const virtualCapacityBytes uint64 = 1 << 50
	const virtualBlockSize uint64 = 4096
	const virtualFileSlots uint64 = 1 << 32
	blocks := virtualCapacityBytes / virtualBlockSize
	st.Bsize = virtualBlockSize
	st.Frsize = virtualBlockSize
	st.Blocks = blocks
	st.Bfree = blocks
	st.Bavail = blocks
	st.Files = virtualFileSlots
	st.Ffree = virtualFileSlots
	st.Favail = virtualFileSlots
	st.Namemax = 255
	return 0
}

// ---- staging 内部逻辑（对齐 write_support.go） ----

func (f *winFuseFS) unlinkStaging(stg *stagingFileWin) int {
	stg.mu.Lock()
	taskID := stg.taskID
	tempPath := stg.tempPath
	stg.mu.Unlock()
	if taskID != "" && stg.uploads != nil {
		if _, err := stg.uploads.Delete(context.Background(), taskID, false); err != nil && f.deps.Log != nil {
			f.deps.Log.Warn("WinFsp staging unlink 取消上传任务失败", "task_id", taskID, "err", err)
		}
	}
	if tempPath != "" {
		_ = os.Remove(tempPath)
	}
	return 0
}

func (f *winFuseFS) renameStaging(stg *stagingFileWin, newParent, newName string) int {
	stg.mu.Lock()
	oldName := stg.name
	taskID := stg.taskID
	newParentID := f.dirIDFor(newParent)
	display := strings.TrimPrefix(newParent, "/")
	stg.name = newName
	stg.parentID = newParentID
	stg.displayPath = display
	stg.mu.Unlock()
	if taskID != "" && stg.uploads != nil {
		if _, err := stg.uploads.RenameTask(context.Background(), taskID, newName, newParentID, display); err != nil && f.deps.Log != nil {
			f.deps.Log.Warn("WinFsp staging rename 更新上传任务失败", "task_id", taskID, "old_name", oldName, "new_name", newName, "err", err)
		}
	}
	return 0
}

// ---- staging 写句柄（对齐 write_support.go stagingUploadHandle） ----

type winStagingHandle struct {
	node              *stagingFileWin
	uploads           UploadManager
	accountID         int64
	parentID          string
	targetDisplayPath string
	fileName          string
	tempPath          string
	file              *os.File
	cleanup           func()
	untrack           func()
	flags             int
	queued            bool
	released          bool
	mu                sync.Mutex
}

func (h *winStagingHandle) flush() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.queued {
		return 0
	}
	if h.file == nil {
		return -cgofs.EBADF
	}
	if h.flags&cgofs.O_ACCMODE == cgofs.O_RDONLY {
		return -cgofs.EBADF
	}
	if err := h.file.Sync(); err != nil {
		return winErr(err)
	}
	st, err := h.file.Stat()
	if err != nil {
		return winErr(err)
	}
	if h.node != nil {
		h.node.updateFromStat(st)
	}
	fileName := h.fileName
	parentID := h.parentID
	displayPath := h.targetDisplayPath
	if h.node != nil {
		h.node.mu.RLock()
		fileName = h.node.name
		parentID = h.node.parentID
		displayPath = h.node.displayPath
		h.node.mu.RUnlock()
	}
	if fileName == "" {
		fileName = h.fileName
	}
	if parentID == "" {
		parentID = h.parentID
	}
	if displayPath == "" {
		displayPath = h.targetDisplayPath
	}
	task, terr := h.uploads.Create(context.Background(), upload.CreateParams{
		AccountID:         h.accountID,
		FileName:          fileName,
		DisplayName:       fileName,
		TargetPath:        parentID,
		TargetDisplayPath: displayPath,
		LocalPath:         h.tempPath,
		TotalBytes:        st.Size(),
		ConflictPolicy:    "overwrite",
	})
	if terr != nil {
		return winErr(terr)
	}
	if h.node != nil && task != nil {
		h.node.mu.Lock()
		h.node.uploads = h.uploads
		h.node.taskID = task.TaskID
		h.node.mu.Unlock()
	}
	h.queued = true
	if h.untrack != nil {
		h.untrack()
		h.untrack = nil
	}
	return 0
}

func (h *winStagingHandle) fsync() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file == nil {
		return -cgofs.EBADF
	}
	if err := h.file.Sync(); err != nil {
		return winErr(err)
	}
	return 0
}

func (h *winStagingHandle) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return
	}
	h.released = true
	if h.file != nil {
		_ = h.file.Close()
		h.file = nil
	}
	if !h.queued && h.cleanup != nil {
		h.cleanup()
		h.cleanup = nil
	}
}

// ---- 句柄分配 ----

const (
	winKindRemote = iota + 1
	winKindStagingWrite
	winKindStagingRead
)

type winHandle struct {
	kind    int
	remote  *winRemoteHandle
	staging *winStagingHandle
	file    *os.File
}

type winRemoteHandle struct {
	reader *playback.RemoteReader
}

func (f *winFuseFS) allocFH(h *winHandle) uint64 {
	f.fhMu.Lock()
	defer f.fhMu.Unlock()
	f.nextFH++
	f.handles[f.nextFH] = h
	return f.nextFH
}

func (f *winFuseFS) getFH(fh uint64) *winHandle {
	f.fhMu.Lock()
	defer f.fhMu.Unlock()
	return f.handles[fh]
}

func (f *winFuseFS) freeFH(fh uint64) *winHandle {
	f.fhMu.Lock()
	defer f.fhMu.Unlock()
	h := f.handles[fh]
	delete(f.handles, fh)
	return h
}

// ---- staging 状态更新 ----

func (s *stagingFileWin) updateSize(size int64) {
	s.mu.Lock()
	if size > s.size {
		s.size = size
	}
	s.modTime = time.Now()
	s.mu.Unlock()
}

func (s *stagingFileWin) updateFromStat(st os.FileInfo) {
	if st == nil {
		return
	}
	s.mu.Lock()
	s.size = st.Size()
	s.modTime = st.ModTime()
	s.mu.Unlock()
}

func createWinTempFile(uploads UploadManager, fileName string) (*os.File, string, func(), func(), error) {
	dir := uploads.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", nil, nil, err
	}
	safeName := filepath.Base(fileName)
	if safeName == "" || safeName == "." {
		safeName = "upload.bin"
	}
	f, err := os.CreateTemp(dir, "fuse_*_"+safeName)
	if err != nil {
		return nil, "", nil, nil, err
	}
	path := filepath.Clean(f.Name())
	untrack := func() {}
	if registry := uploads.TempRegistry(); registry != nil {
		untrack = registry.Track(path)
	}
	cleanup := func() {
		untrack()
		_ = os.Remove(path)
	}
	return f, path, cleanup, untrack, nil
}

// ---- errno ----

func isNotFound(err error) bool {
	if ae, ok := domain.AsAppError(err); ok {
		return ae.Code == domain.CodeNotFound
	}
	return false
}

func winErr(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, syscall.ENOSPC) {
		return -cgofs.ENOSPC
	}
	if errors.Is(err, syscall.EFBIG) {
		return -cgofs.EFBIG
	}
	if ae, ok := domain.AsAppError(err); ok {
		switch ae.Code {
		case domain.CodeNotFound:
			return -cgofs.ENOENT
		case domain.CodePermissionDenied:
			return -cgofs.EACCES
		case domain.CodeValidation:
			return -cgofs.EINVAL
		}
	}
	return -cgofs.EIO
}
