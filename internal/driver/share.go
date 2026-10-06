package driver

import "context"

const (
	ShareKindFree = "free"
	ShareKindPaid = "paid"
)

// ShareCapabilities 描述驱动可用的网盘分享能力。
type ShareCapabilities struct {
	Supported        bool  `json:"supported"`
	SupportsFree     bool  `json:"supports_free"`
	SupportsPaid     bool  `json:"supports_paid"`
	SupportsManage   bool  `json:"supports_manage"`
	SupportsPassword bool  `json:"supports_password"`
	SupportsTraffic  bool  `json:"supports_traffic"`
	SupportsCancel   bool  `json:"supports_cancel"`
	MaxItems         int   `json:"max_items"`
	ExpireDays       []int `json:"expire_days"`
}

type CreateShareRequest struct {
	Kind               string
	Name               string
	FileIDs            []string
	ExpireDays         int
	Password           string
	PayAmount          int64
	RewardEnabled      bool
	ResourceDesc       string
	TrafficSwitch      int
	TrafficLimitSwitch int
	TrafficLimit       int64
}

type ListSharesRequest struct {
	Kind   string
	Cursor string
	Limit  int
}

type UpdateShareRequest struct {
	Kind               string
	ShareIDs           []string
	TrafficSwitch      int
	TrafficLimitSwitch int
	TrafficLimit       int64
}

type ShareItem struct {
	ID                 string  `json:"id"`
	Kind               string  `json:"kind"`
	Key                string  `json:"key"`
	URL                string  `json:"url"`
	Name               string  `json:"name"`
	Expiration         string  `json:"expiration"`
	Expired            bool    `json:"expired"`
	Password           string  `json:"password,omitempty"`
	PayAmount          float64 `json:"pay_amount,omitempty"`
	Income             float64 `json:"income,omitempty"`
	OrderCount         int64   `json:"order_count,omitempty"`
	TrafficSwitch      int     `json:"traffic_switch"`
	TrafficLimitSwitch int     `json:"traffic_limit_switch"`
	TrafficLimit       int64   `json:"traffic_limit"`
	UsedBytes          int64   `json:"used_bytes"`
	PreviewCount       int64   `json:"preview_count"`
	SaveCount          int64   `json:"save_count"`
	DownloadCount      int64   `json:"download_count"`
}

type SharePage struct {
	Items      []ShareItem `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

type ShareCapabilityProvider interface {
	ShareCapabilities() ShareCapabilities
}

type ShareCreator interface {
	CreateShare(ctx context.Context, req CreateShareRequest) (*ShareItem, error)
}

type ShareLister interface {
	ListShares(ctx context.Context, req ListSharesRequest) (*SharePage, error)
}

type ShareUpdater interface {
	UpdateShares(ctx context.Context, req UpdateShareRequest) error
}

type ShareCanceller interface {
	CancelShares(ctx context.Context, shareIDs []string) error
}
