package cloudshare

import (
	"context"
	"strings"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

type Service struct{ exec *driverexec.Executor }

func New(exec *driverexec.Executor) *Service { return &Service{exec: exec} }

func (s *Service) Capabilities(ctx context.Context, accountID int64) (driver.ShareCapabilities, error) {
	if accountID <= 0 {
		return driver.ShareCapabilities{}, domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	var capabilities driver.ShareCapabilities
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		provider, ok := drv.(driver.ShareCapabilityProvider)
		if !ok {
			return nil
		}
		capabilities = provider.ShareCapabilities()
		capabilities.Supported = capabilities.SupportsFree || capabilities.SupportsPaid
		return nil
	})
	return capabilities, err
}

func (s *Service) Create(ctx context.Context, accountID int64, req driver.CreateShareRequest) (*driver.ShareItem, error) {
	if accountID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	req.Kind = normalizeKind(req.Kind)
	req.Name = strings.TrimSpace(req.Name)
	req.Password = strings.TrimSpace(req.Password)
	req.ResourceDesc = strings.TrimSpace(req.ResourceDesc)
	req.FileIDs = cleanStrings(req.FileIDs)
	if req.Name == "" || len(req.FileIDs) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "分享名称与文件不能为空")
	}
	var result *driver.ShareItem
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		provider, err := driverexec.Require[driver.ShareCapabilityProvider](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持创建分享")
		}
		capabilities := provider.ShareCapabilities()
		if req.Kind == driver.ShareKindPaid && !capabilities.SupportsPaid {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持付费分享")
		}
		if req.Kind == driver.ShareKindFree && !capabilities.SupportsFree {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持免费分享")
		}
		if req.Password != "" && !capabilities.SupportsPassword {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持分享提取码")
		}
		if capabilities.MaxItems > 0 && len(req.FileIDs) > capabilities.MaxItems {
			return domain.Errorf(domain.CodeValidation, "一次最多分享 %d 个项目", capabilities.MaxItems)
		}
		creator, err := driverexec.Require[driver.ShareCreator](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持创建分享")
		}
		result, err = creator.CreateShare(ctx, req)
		return err
	})
	return result, err
}

func (s *Service) List(ctx context.Context, accountID int64, req driver.ListSharesRequest) (*driver.SharePage, error) {
	if accountID <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	req.Kind = normalizeKind(req.Kind)
	if req.Limit <= 0 || req.Limit > 100 {
		req.Limit = 50
	}
	var result *driver.SharePage
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		provider, err := driverexec.Require[driver.ShareCapabilityProvider](drv)
		if err != nil || !provider.ShareCapabilities().SupportsManage {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持分享管理")
		}
		lister, err := driverexec.Require[driver.ShareLister](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持分享管理")
		}
		result, err = lister.ListShares(ctx, req)
		return err
	})
	return result, err
}

func (s *Service) Update(ctx context.Context, accountID int64, req driver.UpdateShareRequest) error {
	if accountID <= 0 {
		return domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	req.Kind = normalizeKind(req.Kind)
	req.ShareIDs = cleanStrings(req.ShareIDs)
	if len(req.ShareIDs) == 0 {
		return domain.Errorf(domain.CodeValidation, "请选择要修改的分享")
	}
	return s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		provider, err := driverexec.Require[driver.ShareCapabilityProvider](drv)
		if err != nil || !provider.ShareCapabilities().SupportsTraffic {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持修改分享流量设置")
		}
		updater, err := driverexec.Require[driver.ShareUpdater](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持修改分享")
		}
		return updater.UpdateShares(ctx, req)
	})
}

func (s *Service) Cancel(ctx context.Context, accountID int64, shareIDs []string) error {
	if accountID <= 0 {
		return domain.Errorf(domain.CodeValidation, "非法 account_id")
	}
	shareIDs = cleanStrings(shareIDs)
	if len(shareIDs) == 0 {
		return domain.Errorf(domain.CodeValidation, "请选择要取消的分享")
	}
	return s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		provider, err := driverexec.Require[driver.ShareCapabilityProvider](drv)
		if err != nil || !provider.ShareCapabilities().SupportsCancel {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持取消分享")
		}
		canceller, err := driverexec.Require[driver.ShareCanceller](drv)
		if err != nil {
			return domain.Errorf(domain.CodeNotImplement, "当前网盘不支持取消分享")
		}
		return canceller.CancelShares(ctx, shareIDs)
	})
}

func normalizeKind(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), driver.ShareKindPaid) {
		return driver.ShareKindPaid
	}
	return driver.ShareKindFree
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
