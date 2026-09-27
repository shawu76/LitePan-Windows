package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// DatabaseGarbage 是可由垃圾清理工具安全识别的数据库残留。
type DatabaseGarbage struct {
	Key    string
	Name   string
	Detail string
	Count  int64
	Kind   string
}

type orphanRule struct {
	key, name, table, where string
}

var orphanRules = []orphanRule{
	{"auth_states", "无主认证状态", "account_auth_states", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"strm_tasks", "无主 STRM 任务", "strm_tasks", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"strm_branches", "无主 STRM 分支", "strm_branches", `task_id NOT IN (SELECT id FROM strm_tasks) OR account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"strm_dir_cache", "无主 STRM 路径映射", "strm_remote_dir_cache", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"upload_tasks", "无主上传任务", "upload_tasks", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"offline_tasks", "无主离线下载任务", "offline_download_tasks", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"organize_tasks", "无主目录整理任务", "media_organize_tasks", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"retention_tasks", "无主缓存任务", "cache_retention_tasks", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"fuse_mounts", "无主 FUSE 挂载", "fuse_mounts", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"quarktv_bindings", "无主夸克 TV 绑定", "quarktv_bindings", `account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"notifications", "无主账号通知", "notifications", `account_id > 0 AND account_id NOT IN (SELECT id FROM cloud_accounts)`},
	{"automation_runs", "无主联动运行记录", "automation_runs", `rule_id NOT IN (SELECT id FROM automation_rules)`},
}

// 只清理已经明确从 LitePan 数据模型中移除的表，绝不删除其他未知表。
var deprecatedTables = []string{
	"strm_sync_branches",
	"strm_sync_tasks",
	"strm_aggregates",
	"cache_retention_configs",
	"emby_proxy_configs",
}

func (db *DB) ScanGarbage(ctx context.Context) ([]DatabaseGarbage, error) {
	items := make([]DatabaseGarbage, 0)
	for _, rule := range orphanRules {
		exists, err := tableExists(ctx, db, rule.table)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		var count int64
		query := fmt.Sprintf("SELECT COUNT(1) FROM %s WHERE %s", rule.table, rule.where)
		if err := db.read.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return nil, fmt.Errorf("scan database garbage %s: %w", rule.key, err)
		}
		if count > 0 {
			items = append(items, DatabaseGarbage{
				Key: "orphan:" + rule.key, Name: rule.name, Detail: rule.table, Count: count, Kind: "orphan",
			})
		}
	}

	var tables []string
	var rows int64
	for _, table := range deprecatedTables {
		exists, err := tableExists(ctx, db, table)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		tables = append(tables, table)
		var count int64
		if err := db.read.QueryRowContext(ctx, "SELECT COUNT(1) FROM "+table).Scan(&count); err != nil {
			return nil, fmt.Errorf("scan deprecated table %s: %w", table, err)
		}
		rows += count
	}
	if len(tables) > 0 {
		items = append(items, DatabaseGarbage{
			Key: "deprecated_tables", Name: "历史废弃数据表", Detail: strings.Join(tables, "、"), Count: rows, Kind: "deprecated",
		})
	}
	return items, nil
}

// CleanupGarbage 会再次按固定规则复核目标；key 不能携带任意表名或 SQL。
func (db *DB) CleanupGarbage(ctx context.Context, key string) (int64, error) {
	if key == "deprecated_tables" {
		return db.dropDeprecatedTables(ctx)
	}
	name := strings.TrimPrefix(key, "orphan:")
	for _, rule := range orphanRules {
		if rule.key != name || name == key {
			continue
		}
		exists, err := tableExists(ctx, db, rule.table)
		if err != nil || !exists {
			return 0, err
		}
		res, err := db.write.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s", rule.table, rule.where))
		if err != nil {
			return 0, fmt.Errorf("cleanup database garbage %s: %w", rule.key, err)
		}
		return res.RowsAffected()
	}
	return 0, fmt.Errorf("unknown database garbage key %q", key)
}

func (db *DB) dropDeprecatedTables(ctx context.Context) (int64, error) {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var removed int64
	for _, table := range deprecatedTables {
		var count int64
		err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if count == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+table); err != nil {
			return 0, fmt.Errorf("drop deprecated table %s: %w", table, err)
		}
		removed++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return removed, nil
}
