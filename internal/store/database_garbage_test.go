package store

import (
	"context"
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
	`); err != nil {
		t.Fatal(err)
	}

	items, err := db.ScanGarbage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasGarbage(items, "orphan:strm_dir_cache") || !hasGarbage(items, "deprecated_tables") {
		t.Fatalf("未扫描到预期残留：%+v", items)
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
}

func hasGarbage(items []DatabaseGarbage, key string) bool {
	for _, item := range items {
		if item.Key == key {
			return true
		}
	}
	return false
}
