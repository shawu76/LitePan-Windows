package store

import (
	"context"
	"strings"
	"testing"
)

func TestDatabaseGarbageOnlyRemovesKnownTargets(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.ExecContext(ctx, `
		CREATE TABLE strm_aggregates(id INTEGER PRIMARY KEY, name TEXT);
		INSERT INTO strm_aggregates(name) VALUES ('旧聚合');
		CREATE TABLE user_private_table(id INTEGER PRIMARY KEY);
		INSERT INTO user_private_table(id) VALUES (1);
		INSERT INTO strm_remote_dir_cache(account_id,dir_id,dir_path,last_seen_at)
		VALUES (999,'old','/old',0);
		INSERT INTO notifications(title,message,is_read) VALUES
		('已读通知','old',1),
		('未读通知','keep',0);
		INSERT INTO notifications(title,message,ref_id,is_read)
		VALUES ('待确认通知','keep',88,1);
	`); err != nil {
		t.Fatal(err)
	}

	items, err := db.ScanGarbage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	readNotificationsKey := garbageKeyWithPrefix(items, readNotificationsKeyPrefix)
	if !hasGarbage(items, "orphan:strm_dir_cache") || !hasGarbage(items, "deprecated_tables") || readNotificationsKey == "" {
		t.Fatalf("未扫描到预期残留：%+v", items)
	}
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO notifications(title,message,is_read) VALUES ('扫描后已读通知','new',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CleanupGarbage(ctx, readNotificationsKey); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CleanupGarbage(ctx, "orphan:strm_dir_cache"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CleanupGarbage(ctx, "deprecated_tables"); err != nil {
		t.Fatal(err)
	}
	if exists, err := tableExists(ctx, db, "strm_aggregates"); err != nil || exists {
		t.Fatalf("废弃表未删除：exists=%v err=%v", exists, err)
	}
	if exists, err := tableExists(ctx, db, "user_private_table"); err != nil || !exists {
		t.Fatalf("未知表不应删除：exists=%v err=%v", exists, err)
	}
	var readCount, unreadCount, linkedCount int
	if err := db.read.QueryRowContext(ctx, `SELECT COUNT(1) FROM notifications WHERE is_read=1 AND ref_id=0`).Scan(&readCount); err != nil {
		t.Fatal(err)
	}
	if err := db.read.QueryRowContext(ctx, `SELECT COUNT(1) FROM notifications WHERE is_read=0`).Scan(&unreadCount); err != nil {
		t.Fatal(err)
	}
	if err := db.read.QueryRowContext(ctx, `SELECT COUNT(1) FROM notifications WHERE ref_id>0`).Scan(&linkedCount); err != nil {
		t.Fatal(err)
	}
	if readCount != 1 || unreadCount != 1 || linkedCount != 1 {
		t.Fatalf("通知清理范围不正确：read=%d unread=%d linked=%d", readCount, unreadCount, linkedCount)
	}
}

func hasGarbage(items []DatabaseGarbage, key string) bool {
	for _, item := range items {
		if item.Key == key {
			return true
		}
	}
	return false
}

func garbageKeyWithPrefix(items []DatabaseGarbage, prefix string) string {
	for _, item := range items {
		if strings.HasPrefix(item.Key, prefix) {
			return item.Key
		}
	}
	return ""
}
