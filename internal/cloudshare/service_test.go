package cloudshare

import (
	"context"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

type shareTestProvider struct{ drv driver.Driver }

func (p shareTestProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

type shareTestDriver struct {
	created   driver.CreateShareRequest
	updated   driver.UpdateShareRequest
	cancelled []string
}

func (*shareTestDriver) Config() driver.Config      { return driver.Config{Name: "share-test"} }
func (*shareTestDriver) GetAddition() any           { return &struct{}{} }
func (*shareTestDriver) Init(context.Context) error { return nil }
func (*shareTestDriver) Drop(context.Context) error { return nil }
func (*shareTestDriver) Ping(context.Context) error { return nil }
func (*shareTestDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (*shareTestDriver) ShareCapabilities() driver.ShareCapabilities {
	return driver.ShareCapabilities{
		SupportsFree: true, SupportsPaid: true, SupportsManage: true, SupportsPassword: true,
		SupportsTraffic: true, SupportsCancel: true, MaxItems: 2, ExpireDays: []int{1, 7, 30, 0},
	}
}
func (d *shareTestDriver) CreateShare(_ context.Context, req driver.CreateShareRequest) (*driver.ShareItem, error) {
	d.created = req
	return &driver.ShareItem{ID: "9", Kind: req.Kind, Name: req.Name}, nil
}
func (*shareTestDriver) ListShares(_ context.Context, req driver.ListSharesRequest) (*driver.SharePage, error) {
	return &driver.SharePage{Items: []driver.ShareItem{{ID: "9", Kind: req.Kind}}, NextCursor: "-1"}, nil
}
func (d *shareTestDriver) UpdateShares(_ context.Context, req driver.UpdateShareRequest) error {
	d.updated = req
	return nil
}
func (d *shareTestDriver) CancelShares(_ context.Context, shareIDs []string) error {
	d.cancelled = append([]string(nil), shareIDs...)
	return nil
}

func newShareTestService(drv driver.Driver) *Service {
	return New(driverexec.New(shareTestProvider{drv: drv}, nil))
}

func TestCreateNormalizesAndDispatches(t *testing.T) {
	drv := &shareTestDriver{}
	svc := newShareTestService(drv)
	got, err := svc.Create(context.Background(), 1, driver.CreateShareRequest{
		Kind: "PAID", Name: " 测试分享 ", FileIDs: []string{"11", "11", " 12 "}, PayAmount: 8,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID != "9" || drv.created.Kind != driver.ShareKindPaid || drv.created.Name != "测试分享" {
		t.Fatalf("unexpected result/request: got=%+v req=%+v", got, drv.created)
	}
	if len(drv.created.FileIDs) != 2 || drv.created.FileIDs[1] != "12" {
		t.Fatalf("file ids not normalized: %#v", drv.created.FileIDs)
	}
}

func TestCreateHonorsDriverItemLimit(t *testing.T) {
	svc := newShareTestService(&shareTestDriver{})
	_, err := svc.Create(context.Background(), 1, driver.CreateShareRequest{
		Name: "too many", FileIDs: []string{"1", "2", "3"},
	})
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeValidation {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestListAndUpdate(t *testing.T) {
	drv := &shareTestDriver{}
	svc := newShareTestService(drv)
	page, err := svc.List(context.Background(), 1, driver.ListSharesRequest{Kind: "paid", Limit: 500})
	if err != nil || len(page.Items) != 1 || page.Items[0].Kind != driver.ShareKindPaid {
		t.Fatalf("list: page=%+v err=%v", page, err)
	}
	err = svc.Update(context.Background(), 1, driver.UpdateShareRequest{
		Kind: "paid", ShareIDs: []string{"9", "9"}, TrafficSwitch: 2,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(drv.updated.ShareIDs) != 1 || drv.updated.TrafficSwitch != 2 {
		t.Fatalf("update request not normalized: %+v", drv.updated)
	}
	if err := svc.Cancel(context.Background(), 1, []string{"9", " 9 ", "10"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if len(drv.cancelled) != 2 || drv.cancelled[1] != "10" {
		t.Fatalf("cancel ids not normalized: %#v", drv.cancelled)
	}
}
