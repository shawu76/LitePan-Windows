package upload

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const transferCheckpointInterval = 10 * time.Second

// 并发 WriteAt 会产生空洞，恢复时只信任已落盘的连续断点。
func crossTransferResumeOffset(path string) (int64, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path + ".download")
	if os.IsNotExist(err) {
		return info.Size(), nil
	}
	if err != nil {
		return 0, err
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || offset < 0 || offset > info.Size() {
		// 损坏的检查点只能从头重下，不能信任可能带空洞的文件长度。
		return 0, nil
	}
	return offset, nil
}

func saveCrossTransferCheckpoint(file *os.File, offset int64) error {
	if err := file.Sync(); err != nil {
		return err
	}
	path := file.Name() + ".download"
	tmp, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = fmt.Fprintln(tmp, offset); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), path)
}

// transferCheckpoint 异步落盘连续断点，避免 fsync 阻塞下载；失败时沿用旧断点。
type transferCheckpoint struct {
	file    *os.File
	stopCh  chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	offset  int64
	savedAt int64
}

func newTransferCheckpoint(file *os.File, offset int64) *transferCheckpoint {
	return newTransferCheckpointEvery(file, offset, transferCheckpointInterval)
}

func newTransferCheckpointEvery(file *os.File, offset int64, interval time.Duration) *transferCheckpoint {
	c := &transferCheckpoint{
		file:    file,
		offset:  offset,
		savedAt: offset,
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.run(interval)
	return c
}

func (c *transferCheckpoint) update(offset int64) {
	c.mu.Lock()
	if offset > c.offset {
		c.offset = offset
	}
	c.mu.Unlock()
}

func (c *transferCheckpoint) run(interval time.Duration) {
	defer close(c.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.flush()
		}
	}
}

// stop 停止后台落盘并等待退出；调用方必须在此之后再截断或关闭文件。
func (c *transferCheckpoint) stop() {
	close(c.stopCh)
	<-c.done
}

func (c *transferCheckpoint) flush() {
	c.mu.Lock()
	offset, savedAt := c.offset, c.savedAt
	c.mu.Unlock()
	if offset <= savedAt {
		return
	}
	if err := saveCrossTransferCheckpoint(c.file, offset); err != nil {
		return
	}
	c.mu.Lock()
	if offset > c.savedAt {
		c.savedAt = offset
	}
	c.mu.Unlock()
}
