package timeutil

import (
	"strconv"
	"strings"
	"time"
)

// UnixFloat 返回时间对应的 Unix 秒（浮点精度），与 JSON 数字时间戳互操作。
func UnixFloat(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}

// ParseClock 解析小时、分钟，允许两侧空白。
func ParseClock(text string) (hour, minute int, ok bool) {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return 0, 0, false
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// InClockWindow 包含起止分钟，支持跨午夜；无效配置不限制执行。
func InClockWindow(start, end string, now time.Time) bool {
	sh, sm, ok1 := ParseClock(start)
	eh, em, ok2 := ParseClock(end)
	if !ok1 || !ok2 {
		return true
	}
	lo, hi, current := sh*60+sm, eh*60+em, now.Hour()*60+now.Minute()
	if lo <= hi {
		return current >= lo && current <= hi
	}
	return current >= lo || current <= hi
}
