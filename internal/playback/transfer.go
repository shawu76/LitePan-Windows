package playback

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"litepan/internal/domain"
)

const (
	transferRefreshes = 8
	transferRetries   = 5
)

func transferSettings(link domain.DownloadInfo) (int64, int) {
	chunk, workers := link.TransferChunkSize, link.TransferConcurrency
	if chunk <= 0 {
		chunk = link.ChunkSize
	}
	if chunk <= 0 {
		chunk = defaultPartSize
	}
	if workers <= 0 {
		workers = max(1, link.Concurrency)
	}
	if workers > maximumRangeConcurrency {
		workers = maximumRangeConcurrency
	}
	return chunk, workers
}

// DownloadOriginal 并发直写文件区间，不缓存整片。progress 串行报告本次写入量和
// 连续已写入的文件偏移；返回偏移可用于截断未完成的乱序尾部，安全恢复任务。
func (s *Service) DownloadOriginal(ctx context.Context, w io.WriterAt, accountID int64, fileID string, res Resolved, start, end int64, progress func(int64, int64) error) (int64, error) {
	if s == nil || w == nil || accountID <= 0 || fileID == "" || start < 0 || end < start || res.Link.URL == "" {
		return start, domain.Errorf(domain.CodeValidation, "源文件下载参数无效")
	}
	size := res.File.Size
	if size <= 0 {
		size = res.Link.Size
	}
	if size <= 0 {
		size = end + 1
	}
	if end >= size {
		return start, domain.Errorf(domain.CodeValidation, "下载区间超出源文件大小")
	}
	chunk, workers := transferSettings(res.Link)
	accountLimit := workers
	count := (end-start)/chunk + 1
	if int64(workers) > count {
		workers = int(count)
	}
	lh := s.originalLinkHolder(accountID, fileID, res)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	next, prefix := int64(0), start
	// 只保存连续断点之后的进度；已归并的分片即时移除。
	written := make(map[int64]int64)
	report := func(index, n int64) error {
		mu.Lock()
		defer mu.Unlock()
		written[index] += n
		for prefix <= end {
			i := (prefix - start) / chunk
			partStart := start + i*chunk
			partLength := min(chunk, end-partStart+1)
			prefix = partStart + written[i]
			if written[i] < partLength {
				break
			}
			delete(written, i)
		}
		if progress != nil {
			return progress(n, prefix)
		}
		return nil
	}
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				index := next
				next++
				mu.Unlock()
				if index >= count || workerCtx.Err() != nil {
					return
				}
				partStart := start + index*chunk
				partEnd := partStart + min(chunk, end-partStart+1) - 1
				writer := &transferWriter{dst: w, offset: partStart, report: func(n int64) error { return report(index, n) }}
				if err := s.downloadTransferPart(workerCtx, writer, lh, partEnd, size, accountLimit); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return prefix, ctx.Err()
	}
	return prefix, firstErr
}

type transferWriter struct {
	dst    io.WriterAt
	offset int64
	report func(int64) error
	err    error
}

func (w *transferWriter) Write(p []byte) (int, error) {
	n, err := w.dst.WriteAt(p, w.offset)
	w.offset += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if n > 0 {
		if progressErr := w.report(int64(n)); err == nil {
			err = progressErr
		}
	}
	w.err = err
	return n, err
}

func (s *Service) downloadTransferPart(ctx context.Context, w *transferWriter, lh *linkHolder, end, size int64, workers int) error {
	var lastErr error
	for attempt := 0; attempt < transferRetries; attempt++ {
		if err := s.transferLimits.waitCoolDown(ctx, lh.accountID); err != nil {
			return err
		}
		if attempt > 0 {
			if err := waitTransferRetry(ctx, (1<<min(attempt-1, 3))*500*time.Millisecond); err != nil {
				return err
			}
		}
		release, err := s.transferLimits.acquireBlocking(ctx, lh.accountID, workers)
		if err != nil {
			return err
		}
		// 等待并发名额期间也可能被其他任务登记冷却。
		if err := s.transferLimits.waitCoolDown(ctx, lh.accountID); err != nil {
			release()
			return err
		}
		lh.mu.Lock()
		link, generation := lh.link, lh.generation
		lh.mu.Unlock()
		start := w.offset
		resp, err := s.doRangeRequest(ctx, lh.accountID, link, start, end, true, false)
		if err != nil {
			release()
			lastErr = err
			continue
		}
		status := resp.StatusCode
		if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound {
			_ = resp.Body.Close()
			release()
			_, refreshed, err := lh.refreshVersion(ctx, link, status, generation)
			if err != nil {
				return err
			}
			if !refreshed {
				return domain.Errorf(domain.CodeDriverError, "源盘下载 HTTP %d，换链次数已用尽", status)
			}
			// 换链不占用分片的网络重试额度，但受整任务固定额度约束。
			attempt--
			continue
		}
		if status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= http.StatusInternalServerError {
			if status == http.StatusTooManyRequests {
				s.transferLimits.coolDown(lh.accountID, transferRetryAfter(resp.Header.Get("Retry-After")))
			}
			_ = resp.Body.Close()
			release()
			lastErr = domain.Errorf(domain.CodeDriverError, "源盘下载 HTTP %d", status)
			continue
		}
		if err := validateTransferRange(resp, start, end, size); err != nil {
			_ = resp.Body.Close()
			release()
			return err
		}
		buf := copyBufPool.Get().(*[]byte)
		_, err = io.CopyBuffer(w, io.LimitReader(resp.Body, end-start+1), *buf)
		copyBufPool.Put(buf)
		_ = resp.Body.Close()
		release()
		if w.err != nil {
			return w.err
		}
		if w.offset == end+1 {
			return nil
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		lastErr = err
	}
	return fmt.Errorf("源盘分片下载失败（断点 %d）：%w", w.offset, lastErr)
}

func validateTransferRange(resp *http.Response, start, end, size int64) error {
	want := end - start + 1
	if resp.StatusCode == http.StatusOK && start == 0 && end == size-1 && resp.ContentLength == want {
		return nil
	}
	if resp.StatusCode != http.StatusPartialContent {
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return fmt.Errorf("%w：HTTP %d", ErrInvalidRangeResponse, resp.StatusCode)
		}
		return domain.Errorf(domain.CodeDriverError, "源盘下载 HTTP %d", resp.StatusCode)
	}
	var gotStart, gotEnd, gotSize int64
	if n, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &gotStart, &gotEnd, &gotSize); err != nil || n != 3 || gotStart != start || gotEnd != end || gotSize != size || (resp.ContentLength >= 0 && resp.ContentLength != want) {
		return fmt.Errorf("%w：Content-Range 或长度与请求不符", ErrInvalidRangeResponse)
	}
	return nil
}

func transferRetryAfter(value string) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(min(seconds, 300)) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil {
		return max(time.Duration(0), min(time.Until(until), 5*time.Minute))
	}
	return 30 * time.Second
}

func waitTransferRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
