package pan123open

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
	"litepan/internal/httpx"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	pathUploadCreate   = "/upload/v2/file/create"
	pathUploadComplete = "/upload/v2/file/upload_complete"
	pathUploadSlice    = "/upload/v2/file/slice"

	sliceResumePersistEvery  = 5
	sliceProgressMinInterval = 250 * time.Millisecond
	uploadSliceConcurrency   = 3
)

type uploadCreateData struct {
	FileID      json.Number `json:"fileID"`
	FileId      json.Number `json:"fileId"`
	PreuploadID string      `json:"preuploadID"`
	PreuploadId string      `json:"preuploadId"`
	Reuse       bool        `json:"reuse"`
	SliceSize   int64       `json:"sliceSize"`
	Servers     []any       `json:"servers"`
}

type uploadCompleteData struct {
	Completed bool        `json:"completed"`
	FileID    json.Number `json:"fileID"`
	FileId    json.Number `json:"fileId"`
}

func (d *Driver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	targetName := strings.TrimSpace(req.FileName)
	if targetName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "上传文件名不能为空")
	}
	if err := uploadutil.ValidateFileName(targetName); err != nil {
		return nil, err
	}
	localPath := strings.TrimSpace(req.LocalPath)
	localFile, err := uploadutil.StatLocalFile(localPath)
	if err != nil {
		return nil, err
	}
	fileSize := localFile.Size
	localPath = localFile.Path

	parentID := d.normalizeParent(req.ParentID)
	policy := uploadutil.NormalizeConflictPolicy(req.ConflictPolicy)

	if policy == "skip" {
		if existing, err := d.findExistingFileInParent(ctx, parentID, targetName); err != nil {
			return nil, err
		} else if existing != nil {
			return &driver.LocalUploadResult{
				FileID:   existing.ID,
				ParentID: parentID,
				FileName: targetName,
				Size:     fileSize,
				Message:  fmt.Sprintf("文件 '%s' 已存在，已跳过", targetName),
				Skipped:  true,
			}, nil
		}
	}

	resumeUploaded := uploadutil.ResumeStateUploadedBytes(req.ResumeState)
	resume := normalizePan123ResumeState(req.ResumeState, parentID, targetName, fileSize, "")
	var fileMD5 string
	if resume != nil {
		fileMD5 = resume.fileMD5
		if resumeUploaded > 0 || len(resume.completedSlices) > 0 {
			uploadutil.NotifyProgress(req.OnProgress, resume.uploadedBytes, fileSize, "正在继续上传到123云盘Open")
		}
	} else {
		if resumeUploaded > 0 {
			uploadutil.NotifyProgress(req.OnProgress, resumeUploaded, fileSize, "正在继续上传到123云盘Open")
		} else {
			uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在计算文件校验值")
		}
		fileMD5, err = uploadutil.HashMD5(ctx, localPath)
		if err != nil {
			return nil, err
		}
		resume = normalizePan123ResumeState(req.ResumeState, parentID, targetName, fileSize, fileMD5)
	}

	var preuploadID string
	var sliceSize int64
	var servers []string

	if resume != nil {
		preuploadID = resume.preuploadID
		sliceSize = resume.sliceSize
		servers = resume.servers
	} else {
		uploadutil.NotifyProgress(req.OnProgress, 0, fileSize, "正在准备上传")
		createData, err := d.createUploadFile(ctx, parentID, targetName, fileSize, fileMD5, policy)
		if err != nil {
			return nil, err
		}

		fileID := firstNonEmptyNumber(createData.FileID, createData.FileId)
		if createData.Reuse {
			uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "秒传成功")
			resolvedID, resolvedName := d.resolveUploadedFile(ctx, parentID, targetName, fileSize, fileID)
			msg := fmt.Sprintf("文件 '%s' 秒传成功", resolvedName)
			return &driver.LocalUploadResult{
				FileID:   resolvedID,
				ParentID: parentID,
				FileName: resolvedName,
				Size:     fileSize,
				Message:  msg,
			}, nil
		}

		preuploadID = strings.TrimSpace(createData.PreuploadID)
		if preuploadID == "" {
			preuploadID = strings.TrimSpace(createData.PreuploadId)
		}
		sliceSize = createData.SliceSize
		servers = normalizeUploadServers(createData.Servers)
		if preuploadID == "" {
			return nil, domain.Errorf(domain.CodeDriverError, "上传初始化失败：响应中缺少 preuploadID")
		}
		if fileSize > 0 && sliceSize <= 0 {
			return nil, domain.Errorf(domain.CodeDriverError, "上传初始化失败：响应中缺少有效 sliceSize")
		}
		if fileSize > 0 && len(servers) == 0 {
			return nil, domain.Errorf(domain.CodeDriverError, "上传初始化失败：响应中缺少上传域名")
		}
		resume = &pan123ResumeCtx{
			parentID:        parentID,
			targetName:      targetName,
			fileSize:        fileSize,
			fileMD5:         fileMD5,
			preuploadID:     preuploadID,
			sliceSize:       sliceSize,
			servers:         servers,
			completedSlices: map[int]struct{}{},
		}
		persistPan123ResumeState(req.OnResumeState, resume)
	}

	if err := d.uploadFileSlices(ctx, localPath, fileSize, preuploadID, sliceSize, servers, req.OnProgress, resume, req.OnResumeState); err != nil {
		return nil, err
	}

	completedID, err := d.completeUpload(ctx, preuploadID, fileSize, req.OnProgress)
	if err != nil {
		return nil, err
	}
	resolvedID, resolvedName := d.resolveUploadedFile(ctx, parentID, targetName, fileSize, completedID)
	uploadutil.NotifyProgress(req.OnProgress, fileSize, fileSize, "上传成功")
	return &driver.LocalUploadResult{
		FileID:   resolvedID,
		ParentID: parentID,
		FileName: resolvedName,
		Size:     fileSize,
		Message:  fmt.Sprintf("文件 '%s' 上传成功", resolvedName),
	}, nil
}

func mapConflictPolicyToDuplicate(policy string) int {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "overwrite":
		return 2
	default:
		return 1
	}
}

func toAPIParentFileID(parentID string) any {
	normalized := strings.TrimSpace(parentID)
	if normalized == "" {
		normalized = "0"
	}
	if n, err := strconv.ParseInt(normalized, 10, 64); err == nil {
		return n
	}
	return normalized
}

func (d *Driver) findExistingFileInParent(ctx context.Context, parentID, targetName string) (*domain.FileItem, error) {
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Name == targetName {
			return &items[i], nil
		}
	}
	return nil, nil
}

func (d *Driver) createUploadFile(ctx context.Context, parentID, targetName string, size int64, etag, policy string) (*uploadCreateData, error) {
	body := map[string]any{
		"parentFileID": toAPIParentFileID(parentID),
		"filename":     targetName,
		"etag":         strings.ToLower(etag),
		"size":         size,
		"duplicate":    mapConflictPolicyToDuplicate(policy),
		"containDir":   false,
	}
	var out uploadCreateData
	err := d.apiCall(ctx, http.MethodPost, pathUploadCreate, nil, body, &out)
	if err != nil {
		body["parentFileId"] = body["parentFileID"]
		delete(body, "parentFileID")
		err = d.apiCall(ctx, http.MethodPost, pathUploadCreate, nil, body, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func normalizeUploadServers(servers []any) []string {
	var out []string
	for _, server := range servers {
		var value string
		switch v := server.(type) {
		case string:
			value = v
		default:
			value = strings.TrimSpace(fmt.Sprint(v))
		}
		value = strings.TrimSpace(strings.TrimSuffix(value, "/"))
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			value = "https://" + value
		}
		out = append(out, value)
	}
	return out
}

func (d *Driver) uploadFileSlices(
	ctx context.Context,
	localPath string,
	fileSize int64,
	preuploadID string,
	sliceSize int64,
	servers []string,
	onProgress driver.UploadProgress,
	resume *pan123ResumeCtx,
	onState driver.UploadStateCallback,
) error {
	// 123 Open 的空文件只需 create + complete，不应伪造一个 0 字节分片。
	if fileSize == 0 {
		return nil
	}
	totalSlices := int((fileSize + sliceSize - 1) / sliceSize)
	if totalSlices <= 0 {
		totalSlices = 1
	}

	completed := map[int]struct{}{}
	if resume != nil {
		for n := range resume.completedSlices {
			completed[n] = struct{}{}
		}
	}
	uploaded := uploadutil.UploadedBytesByParts(fileSize, sliceSize, completed)
	progressMsg := "正在上传到123云盘Open"
	progress := uploadutil.NewParallelProgress(uploaded, fileSize, onProgress, progressMsg)
	if len(completed) > 0 {
		uploadutil.NotifyProgress(onProgress, uploaded, fileSize, "正在继续上传到123云盘Open")
	}

	pending := make([]int, 0, totalSlices-len(completed))
	for sliceNo := 1; sliceNo <= totalSlices; sliceNo++ {
		if _, ok := completed[sliceNo]; !ok {
			pending = append(pending, sliceNo)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	workerCount := min(uploadSliceConcurrency, len(pending))
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, len(pending))
	for _, sliceNo := range pending {
		jobs <- sliceNo
	}
	close(jobs)

	var completedMu sync.Mutex
	var firstErr error
	var errOnce sync.Once
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			f, err := os.Open(localPath)
			if err != nil {
				errOnce.Do(func() {
					firstErr = domain.Wrap(domain.CodeDriverError, err)
					cancel()
				})
				return
			}
			defer f.Close()

			for sliceNo := range jobs {
				if workerCtx.Err() != nil {
					return
				}
				offset := int64(sliceNo-1) * sliceSize
				chunkLen := min(sliceSize, fileSize-offset)
				reader := io.NewSectionReader(f, offset, chunkLen)
				hasher := md5.New()
				if _, err := io.Copy(hasher, reader); err != nil {
					errOnce.Do(func() {
						firstErr = domain.Wrap(domain.CodeDriverError, err)
						cancel()
					})
					return
				}
				if err := d.uploadSingleSliceReader(workerCtx, preuploadID, sliceNo, hex.EncodeToString(hasher.Sum(nil)), reader, chunkLen, servers, func(sent int64) {
					progress.Update(sliceNo, sent, chunkLen)
				}); err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}

				completedMu.Lock()
				completed[sliceNo] = struct{}{}
				completedCount := len(completed)
				if resume != nil && completedCount%sliceResumePersistEvery == 0 {
					resume.completedSlices = cloneCompletedSlices(completed)
					persistPan123ResumeState(onState, resume)
				}
				completedMu.Unlock()
				progress.Complete(sliceNo, chunkLen)
			}
		}()
	}
	workers.Wait()

	completedMu.Lock()
	if resume != nil {
		resume.completedSlices = cloneCompletedSlices(completed)
		persistPan123ResumeState(onState, resume)
	}
	completedMu.Unlock()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

func cloneCompletedSlices(src map[int]struct{}) map[int]struct{} {
	out := make(map[int]struct{}, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (d *Driver) uploadSingleSliceReader(
	ctx context.Context,
	preuploadID string,
	sliceNo int,
	sliceMD5 string,
	chunk io.ReadSeeker,
	chunkLen int64,
	servers []string,
	onSliceProgress func(sent int64),
) error {
	maxAttempts := len(servers)
	if maxAttempts < 3 {
		maxAttempts = 3
	}
	primaryHost := strings.TrimSuffix(servers[0], "/")
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		host := primaryHost
		if attempt > 0 {
			host = strings.TrimSuffix(servers[(sliceNo+attempt-1)%len(servers)], "/")
		}
		if _, err := chunk.Seek(0, io.SeekStart); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
		err := d.postUploadSliceReader(ctx, host, preuploadID, sliceNo, sliceMD5, chunk, chunkLen, onSliceProgress)
		if err == nil {
			return nil
		}
		lastErr = err
		if ae, ok := domain.AsAppError(err); ok && ae.Code == domain.CodeAuthExpired {
			if _, rerr := d.doRefresh(ctx); rerr != nil {
				return rerr
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(500*(attempt+1)) * time.Millisecond):
		}
	}
	if lastErr != nil {
		return domain.Errorf(domain.CodeDriverError, "上传分片 %d 失败: %s", sliceNo, lastErr.Error())
	}
	return domain.Errorf(domain.CodeDriverError, "上传分片 %d 失败", sliceNo)
}

func buildSliceMultipartEnvelope(preuploadID string, sliceNo int, sliceMD5 string) (prefix, suffix []byte, contentType string) {
	boundary := fmt.Sprintf("----LitePan123Open%d", time.Now().UnixNano())
	var pb bytes.Buffer
	writeField := func(name, value string) {
		fmt.Fprintf(&pb, "--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n", boundary, name, value)
	}
	writeField("preuploadID", preuploadID)
	writeField("sliceNo", strconv.Itoa(sliceNo))
	writeField("sliceMD5", sliceMD5)
	fmt.Fprintf(&pb, "--%s\r\nContent-Disposition: form-data; name=\"slice\"; filename=\"slice-%d\"\r\nContent-Type: application/octet-stream\r\n\r\n", boundary, sliceNo)
	prefix = append([]byte(nil), pb.Bytes()...)
	suffix = []byte(fmt.Sprintf("\r\n--%s--\r\n", boundary))
	contentType = fmt.Sprintf("multipart/form-data; boundary=%s", boundary)
	return prefix, suffix, contentType
}

type sliceByteCounter struct {
	n atomic.Int64
}

type sliceCountingReader struct {
	r io.Reader
	c *sliceByteCounter
}

func (r *sliceCountingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.c.n.Add(int64(n))
	}
	return n, err
}

func startSliceProgressReporter(
	parent context.Context,
	counter *sliceByteCounter,
	chunkLen int64,
	onProgress func(sent int64),
) context.CancelFunc {
	if onProgress == nil || chunkLen <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(sliceProgressMinInterval)
		defer ticker.Stop()
		var lastSent int64 = -1
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sent := counter.n.Load()
				if sent <= 0 {
					continue
				}
				if sent > chunkLen {
					sent = chunkLen
				}
				if sent == lastSent {
					continue
				}
				lastSent = sent
				onProgress(sent)
			}
		}
	}()
	return func() {
		cancel()
		// 分片完成后不允许旧回调再次加入 active，避免重复累计已完成字节。
		<-done
	}
}

func (d *Driver) postUploadSliceReader(
	ctx context.Context,
	server, preuploadID string,
	sliceNo int,
	sliceMD5 string,
	chunk io.Reader,
	chunkLen int64,
	onProgress func(sent int64),
) error {
	prefix, suffix, contentType := buildSliceMultipartEnvelope(preuploadID, sliceNo, sliceMD5)

	var counter sliceByteCounter
	stopProgress := startSliceProgressReporter(ctx, &counter, chunkLen, onProgress)
	defer stopProgress()

	body := io.MultiReader(
		bytes.NewReader(prefix),
		&sliceCountingReader{r: chunk, c: &counter},
		bytes.NewReader(suffix),
	)

	u := strings.TrimSuffix(server, "/") + pathUploadSlice
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	// 与 OpenList 的分片请求一致：HTTP/1.1 下使用 chunked 流式发送。
	req.ContentLength = -1
	req.Header.Set("Platform", "open_platform")
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", httpx.DefaultUserAgent)
	req.Header.Set("Authorization", "Bearer "+d.currentToken())

	resp, err := d.uploadClient.Do(req)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode == http.StatusUnauthorized {
		return domain.Errf(domain.CodeAuthExpired)
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Errorf(domain.CodeDriverError, "123 分片 HTTP %d: %s", resp.StatusCode, httpx.Truncate(data, 300))
	}

	var env apiEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if env.Code != 0 {
		return mapAPIError(env.Code, env.Message)
	}
	if onProgress != nil {
		onProgress(chunkLen)
	}
	return nil
}

func (d *Driver) completeUpload(ctx context.Context, preuploadID string, fileSize int64, onProgress driver.UploadProgress) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 90; attempt++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		uploadutil.NotifyProgress(onProgress, fileSize, fileSize, "正在校验文件")

		var out uploadCompleteData
		err := d.apiCall(ctx, http.MethodPost, pathUploadComplete, nil, map[string]any{
			"preuploadID": preuploadID,
		}, &out)
		if err != nil {
			if isUploadCheckingError(err) {
				lastErr = err
			} else {
				return "", err
			}
		} else {
			fileID := firstNonEmptyNumber(out.FileID, out.FileId)
			if out.Completed {
				return fileID, nil
			}
		}

		delay := 500 * time.Millisecond
		if attempt >= 3 {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
	}
	if lastErr != nil {
		return "", domain.Errorf(domain.CodeDriverError, "上传完成确认超时，请稍后刷新目录查看结果: %s", lastErr.Error())
	}
	return "", domain.Errorf(domain.CodeDriverError, "上传完成确认超时，请稍后刷新目录查看结果")
}

func isUploadCheckingError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "文件正在校验") ||
		strings.Contains(msg, "正在校验") ||
		strings.Contains(msg, "请间隔1秒后再试") ||
		strings.Contains(msg, "请间隔 1 秒后再试")
}

func firstNonEmptyNumber(nums ...json.Number) string {
	for _, n := range nums {
		s := strings.TrimSpace(n.String())
		if s != "" && s != "0" {
			return s
		}
	}
	return ""
}

func (d *Driver) resolveUploadedFile(ctx context.Context, parentID, targetName string, fileSize int64, preferredID string) (string, string) {
	if preferredID != "" {
		if item, err := d.GetFileInfo(ctx, preferredID); err == nil && item != nil && !item.IsDir {
			return item.ID, item.Name
		}
	}
	items, err := d.ListFiles(ctx, parentID)
	if err != nil {
		return preferredID, targetName
	}
	var candidates []domain.FileItem
	for _, item := range items {
		if item.IsDir || item.Size != fileSize {
			continue
		}
		candidates = append(candidates, item)
		if item.Name == targetName {
			return item.ID, item.Name
		}
	}
	if len(candidates) == 1 {
		return candidates[0].ID, candidates[0].Name
	}
	return preferredID, targetName
}

type pan123ResumeCtx struct {
	parentID        string
	targetName      string
	fileSize        int64
	fileMD5         string
	preuploadID     string
	sliceSize       int64
	servers         []string
	completedSlices map[int]struct{}
	uploadedBytes   int64
	progress        int
}

func normalizePan123ResumeState(
	state map[string]any,
	parentID, targetName string,
	fileSize int64,
	fileMD5 string,
) *pan123ResumeCtx {
	if len(state) == 0 {
		return nil
	}
	resumeParent := strings.TrimSpace(uploadutil.AnyString(state["parent_id"]))
	resumeName := strings.TrimSpace(uploadutil.AnyString(state["target_name"]))
	resumeMD5 := strings.ToLower(strings.TrimSpace(uploadutil.AnyString(state["file_md5"])))
	resumeSize, _ := uploadutil.MapInt64(state["file_size"])
	preuploadID := strings.TrimSpace(uploadutil.AnyString(state["preupload_id"]))
	sliceSize, _ := uploadutil.MapInt64(state["slice_size"])
	if resumeParent != parentID || resumeName != targetName ||
		resumeSize != fileSize ||
		preuploadID == "" || sliceSize <= 0 {
		return nil
	}
	if fileMD5 != "" && resumeMD5 != strings.ToLower(fileMD5) {
		return nil
	}
	if resumeMD5 == "" {
		return nil
	}
	useMD5 := resumeMD5
	if fileMD5 != "" {
		useMD5 = strings.ToLower(fileMD5)
	}
	servers := []string{}
	switch raw := state["servers"].(type) {
	case []string:
		servers = raw
	case []any:
		for _, item := range raw {
			if s := strings.TrimSpace(uploadutil.AnyString(item)); s != "" {
				servers = append(servers, s)
			}
		}
	}
	if len(servers) == 0 {
		return nil
	}
	totalSlices := int((fileSize + sliceSize - 1) / sliceSize)
	if totalSlices <= 0 {
		totalSlices = 1
	}
	completed := map[int]struct{}{}
	switch raw := state["completed_slices"].(type) {
	case []any:
		for _, item := range raw {
			if n, ok := uploadutil.MapInt(item); ok && n >= 1 && n <= totalSlices {
				completed[n] = struct{}{}
			}
		}
	case []int:
		for _, n := range raw {
			if n >= 1 && n <= totalSlices {
				completed[n] = struct{}{}
			}
		}
	}
	uploaded := uploadutil.UploadedBytesByParts(fileSize, sliceSize, completed)
	progress := int(uploaded * 100 / uploadutil.Max64(fileSize, 1))
	if uploaded >= fileSize {
		progress = 100
	} else if progress > 99 {
		progress = 99
	}
	return &pan123ResumeCtx{
		parentID:        parentID,
		targetName:      targetName,
		fileSize:        fileSize,
		fileMD5:         useMD5,
		preuploadID:     preuploadID,
		sliceSize:       sliceSize,
		servers:         servers,
		completedSlices: completed,
		uploadedBytes:   uploaded,
		progress:        progress,
	}
}

func persistPan123ResumeState(onState driver.UploadStateCallback, ctx *pan123ResumeCtx) {
	if onState == nil || ctx == nil {
		return
	}
	completed := make([]int, 0, len(ctx.completedSlices))
	for n := range ctx.completedSlices {
		completed = append(completed, n)
	}
	uploaded := uploadutil.UploadedBytesByParts(ctx.fileSize, ctx.sliceSize, ctx.completedSlices)
	progress := int(uploaded * 100 / uploadutil.Max64(ctx.fileSize, 1))
	if uploaded >= ctx.fileSize {
		progress = 100
	} else if progress > 99 {
		progress = 99
	}
	serverAny := make([]any, len(ctx.servers))
	for i, s := range ctx.servers {
		serverAny[i] = s
	}
	completedAny := make([]any, len(completed))
	for i, n := range completed {
		completedAny[i] = n
	}
	onState(map[string]any{
		"parent_id":        ctx.parentID,
		"target_name":      ctx.targetName,
		"file_size":        ctx.fileSize,
		"file_md5":         ctx.fileMD5,
		"preupload_id":     ctx.preuploadID,
		"slice_size":       ctx.sliceSize,
		"servers":          serverAny,
		"completed_slices": completedAny,
		"uploaded_bytes":   uploaded,
		"progress":         progress,
	})
}

const pathSha1Reuse = "/upload/v2/file/sha1_reuse"

type sha1ReuseData struct {
	Reuse  bool        `json:"reuse"`
	FileID json.Number `json:"fileID"`
	FileId json.Number `json:"fileId"`
}

func (d *Driver) RapidUploadByHash(ctx context.Context, req driver.RapidUploadRequest) (*driver.RapidUploadResult, error) {
	parentID := d.normalizeParent(req.ParentID)
	method := strings.ToLower(strings.TrimSpace(req.Method))
	fileName := strings.TrimSpace(req.FileName)
	if fileName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件名不能为空")
	}

	switch method {
	case "sha1":
		sha1 := driver.NormalizeTransferHash("sha1", req.Hash)
		if sha1 == "" {
			return &driver.RapidUploadResult{Reuse: false, Message: "无效的 SHA1 指纹"}, nil
		}
		var out sha1ReuseData
		body := map[string]any{
			"parentFileID": toAPIParentFileID(parentID),
			"filename":     fileName,
			"sha1":         sha1,
			"size":         req.Size,
			"duplicate":    req.Duplicate,
		}
		if err := d.apiCall(ctx, http.MethodPost, pathSha1Reuse, nil, body, &out); err != nil {
			body["parentFileId"] = body["parentFileID"]
			delete(body, "parentFileID")
			if err2 := d.apiCall(ctx, http.MethodPost, pathSha1Reuse, nil, body, &out); err2 != nil {
				return nil, err2
			}
		}
		fileID := firstNonEmptyNumber(out.FileID, out.FileId)
		msg := "未命中秒传"
		if out.Reuse {
			msg = "秒传命中"
		}
		return &driver.RapidUploadResult{
			Reuse:    out.Reuse,
			FileID:   fileID,
			ParentID: parentID,
			Message:  msg,
		}, nil
	case "md5":
		md5 := driver.NormalizeTransferHash("md5", req.Hash)
		if md5 == "" {
			return &driver.RapidUploadResult{Reuse: false, Message: "无效的 MD5 指纹"}, nil
		}
		policy := "rename"
		if req.Duplicate == 2 {
			policy = "overwrite"
		}
		createData, err := d.createUploadFile(ctx, parentID, fileName, req.Size, md5, policy)
		if err != nil {
			return nil, err
		}
		fileID := firstNonEmptyNumber(createData.FileID, createData.FileId)
		msg := "未命中秒传"
		if createData.Reuse {
			msg = "秒传命中"
		}
		return &driver.RapidUploadResult{
			Reuse:    createData.Reuse,
			FileID:   fileID,
			ParentID: parentID,
			Message:  msg,
		}, nil
	default:
		return nil, domain.Errf(domain.CodeNotImplement)
	}
}

var _ driver.RapidUploader = (*Driver)(nil)
