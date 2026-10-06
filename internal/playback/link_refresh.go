package playback

import "net/url"

// logRefresh 记录换链情况。直链里带签名/token，所以只记目标 host。
func (lh *linkHolder) logRefresh(message string, rawURL string, status int, refreshed bool) {
	if lh == nil || lh.svc == nil || lh.svc.log == nil {
		return
	}
	host := ""
	if u, err := url.Parse(rawURL); err == nil {
		host = u.Host
	}
	if refreshed {
		lh.svc.log.Debug(message, "account_id", lh.accountID, "file_id", lh.fileID,
			"target_host", host, "upstream_status", status, "refresh_left", lh.refreshLeft)
		return
	}
	lh.svc.log.Warn(message, "account_id", lh.accountID, "file_id", lh.fileID,
		"target_host", host, "upstream_status", status, "refresh_left", lh.refreshLeft)
}
