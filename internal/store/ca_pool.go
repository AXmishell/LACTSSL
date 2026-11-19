package store

import (
	"database/sql"

	"lactssl/internal/model"
)

// ---------------------------- CA 调度组 ----------------------------

// CreateCAPool 创建调度组及其候选 CA。
func (s *Store) CreateCAPool(pool *model.CAPool) error {
	now := nowUTC()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`INSERT INTO ca_pools(public_id,name,strategy,cursor,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)`,
		pool.PublicID, pool.Name, pool.Strategy, pool.Cursor, boolInt(pool.Enabled), now, now)
	if err != nil {
		return err
	}
	pool.ID, err = res.LastInsertId()
	if err != nil {
		return err
	}
	for index := range pool.Items {
		item := &pool.Items[index]
		item.PoolID = pool.ID
		if item.Weight <= 0 {
			item.Weight = 1
		}
		itemRes, err := tx.Exec(`INSERT INTO ca_pool_items(pool_id,ca_value,priority,weight,enabled,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?)`,
			item.PoolID, item.CAValue, item.Priority, item.Weight, boolInt(item.Enabled), now, now)
		if err != nil {
			return err
		}
		item.ID, err = itemRes.LastInsertId()
		if err != nil {
			return err
		}
		item.CreatedAt, item.UpdatedAt = now, now
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	pool.CreatedAt, pool.UpdatedAt = now, now
	return nil
}

// UpdateCAPool 更新调度组基础信息和候选列表。
// 游标不随配置更新重置，避免编辑名称或策略后轮询位置回退。
func (s *Store) UpdateCAPool(pool *model.CAPool) error {
	now := nowUTC()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`UPDATE ca_pools SET name=?,strategy=?,enabled=?,updated_at=? WHERE id=?`,
		pool.Name, pool.Strategy, boolInt(pool.Enabled), now, pool.ID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`DELETE FROM ca_pool_items WHERE pool_id=?`, pool.ID); err != nil {
		return err
	}
	for index := range pool.Items {
		item := &pool.Items[index]
		item.PoolID = pool.ID
		if item.Weight <= 0 {
			item.Weight = 1
		}
		itemRes, err := tx.Exec(`INSERT INTO ca_pool_items(pool_id,ca_value,priority,weight,enabled,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?)`,
			item.PoolID, item.CAValue, item.Priority, item.Weight, boolInt(item.Enabled), now, now)
		if err != nil {
			return err
		}
		item.ID, err = itemRes.LastInsertId()
		if err != nil {
			return err
		}
		item.CreatedAt, item.UpdatedAt = now, now
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	pool.UpdatedAt = now
	return nil
}

// GetCAPool 查询调度组及其候选。
func (s *Store) GetCAPool(id int64) (*model.CAPool, error) {
	var pool model.CAPool
	var enabled int
	err := s.DB.QueryRow(`SELECT id,public_id,name,strategy,cursor,enabled,created_at,updated_at
		FROM ca_pools WHERE id=?`, id).
		Scan(&pool.ID, &pool.PublicID, &pool.Name, &pool.Strategy, &pool.Cursor, &enabled, &pool.CreatedAt, &pool.UpdatedAt)
	if err != nil {
		return nil, err
	}
	pool.Enabled = enabled == 1
	items, err := s.listCAPoolItems(id)
	if err != nil {
		return nil, err
	}
	pool.Items = items
	return &pool, nil
}

// ListCAPools 返回全部调度组及其候选。
func (s *Store) ListCAPools() ([]model.CAPool, error) {
	rows, err := s.DB.Query(`SELECT id,public_id,name,strategy,cursor,enabled,created_at,updated_at
		FROM ca_pools ORDER BY id`)
	if err != nil {
		return nil, err
	}
	list := make([]model.CAPool, 0)
	for rows.Next() {
		var pool model.CAPool
		var enabled int
		if err := rows.Scan(&pool.ID, &pool.PublicID, &pool.Name, &pool.Strategy, &pool.Cursor, &enabled, &pool.CreatedAt, &pool.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		pool.Enabled = enabled == 1
		list = append(list, pool)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// 连接池只有一个连接，必须先关闭结果集再查询候选。
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range list {
		items, err := s.listCAPoolItems(list[index].ID)
		if err != nil {
			return nil, err
		}
		list[index].Items = items
	}
	return list, nil
}

func (s *Store) listCAPoolItems(poolID int64) ([]model.CAPoolItem, error) {
	rows, err := s.DB.Query(`SELECT id,pool_id,ca_value,priority,weight,enabled,created_at,updated_at
		FROM ca_pool_items WHERE pool_id=? ORDER BY priority,id`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.CAPoolItem, 0)
	for rows.Next() {
		var item model.CAPoolItem
		var enabled int
		if err := rows.Scan(&item.ID, &item.PoolID, &item.CAValue, &item.Priority, &item.Weight, &enabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Enabled = enabled == 1
		items = append(items, item)
	}
	return items, rows.Err()
}

// DeleteCAPool 删除调度组；候选由外键级联删除，同时显式清理以兼容旧数据库。
func (s *Store) DeleteCAPool(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM ca_pool_items WHERE pool_id=?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM ca_pools WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// NextCAPoolCursor 原子地返回当前游标并递增，供轮询策略分配起始位置。
func (s *Store) NextCAPoolCursor(id int64) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var cursor int64
	if err := tx.QueryRow(`SELECT cursor FROM ca_pools WHERE id=?`, id).Scan(&cursor); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE ca_pools SET cursor=cursor+1 WHERE id=?`, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return cursor, nil
}

// CAPoolNameExists 判断调度组名称是否已被占用。
func (s *Store) CAPoolNameExists(name string, excludeID int64) (bool, error) {
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM ca_pools WHERE name=? AND id<>?`, name, excludeID).Scan(&count)
	return count > 0, err
}

// CountTasksUsingCAPool 统计仍然引用指定调度组的任务数量。
func (s *Store) CountTasksUsingCAPool(poolID int64) (int64, error) {
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM tasks WHERE ca_pool_id=?`, poolID).Scan(&count)
	return count, err
}

// CountCAPoolsUsingCA 统计引用指定 CA 的调度组数量，用于删除保护。
func (s *Store) CountCAPoolsUsingCA(caValue string) (int64, error) {
	var count int64
	err := s.DB.QueryRow(`SELECT COUNT(DISTINCT pool_id) FROM ca_pool_items WHERE ca_value=?`, caValue).Scan(&count)
	return count, err
}

// CAPoolSummary 用于设置页和任务页展示的轻量信息。
type CAPoolSummary struct {
	ID       int64  `json:"id"`
	PublicID string `json:"public_id"`
	Name     string `json:"name"`
	Strategy string `json:"strategy"`
	Enabled  bool   `json:"enabled"`
	Count    int    `json:"count"`
}

// ListCAPoolSummaries 返回调度组摘要，避免前端拉取完整候选列表。
func (s *Store) ListCAPoolSummaries() ([]CAPoolSummary, error) {
	pools, err := s.ListCAPools()
	if err != nil {
		return nil, err
	}
	out := make([]CAPoolSummary, 0, len(pools))
	for _, pool := range pools {
		out = append(out, CAPoolSummary{
			ID:       pool.ID,
			PublicID: pool.PublicID,
			Name:     pool.Name,
			Strategy: pool.Strategy,
			Enabled:  pool.Enabled,
			Count:    len(pool.EnabledItems()),
		})
	}
	return out, nil
}
