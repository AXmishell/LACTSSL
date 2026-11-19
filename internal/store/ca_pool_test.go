package store

import (
	"testing"

	"lactssl/internal/model"
)

func TestCAPoolLifecycleAndCursor(t *testing.T) {
	st := newTestStore(t)
	pool := &model.CAPool{
		PublicID: "pool-public",
		Name:     "production",
		Strategy: model.CAPoolStrategyRoundRobin,
		Enabled:  true,
		Items: []model.CAPoolItem{
			{CAValue: "letsencrypt", Priority: 10, Weight: 1, Enabled: true},
			{CAValue: "zerossl", Priority: 20, Weight: 1, Enabled: true},
		},
	}
	if err := st.CreateCAPool(pool); err != nil {
		t.Fatal(err)
	}
	if pool.ID == 0 || len(pool.Items) != 2 || pool.Items[0].ID == 0 {
		t.Fatalf("创建调度组后 ID 未回填: %+v", pool)
	}

	loaded, err := st.GetCAPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "production" || len(loaded.Items) != 2 || !loaded.Enabled {
		t.Fatalf("读取调度组结果不正确: %+v", loaded)
	}

	first, err := st.NextCAPoolCursor(pool.ID)
	if err != nil || first != 0 {
		t.Fatalf("第一次游标应为 0: %d %v", first, err)
	}
	second, err := st.NextCAPoolCursor(pool.ID)
	if err != nil || second != 1 {
		t.Fatalf("第二次游标应为 1: %d %v", second, err)
	}

	pool.Name = "production-v2"
	pool.Strategy = model.CAPoolStrategyRandom
	pool.Items = []model.CAPoolItem{{CAValue: "google", Priority: 5, Weight: 2, Enabled: true}}
	if err := st.UpdateCAPool(pool); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.GetCAPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "production-v2" || loaded.Strategy != model.CAPoolStrategyRandom || len(loaded.Items) != 1 {
		t.Fatalf("更新调度组结果不正确: %+v", loaded)
	}
	// 更新候选不应重置轮询游标。
	if loaded.Cursor != 2 {
		t.Fatalf("更新后游标应保持为 2，实际 %d", loaded.Cursor)
	}

	exists, err := st.CAPoolNameExists("production-v2", 0)
	if err != nil || !exists {
		t.Fatalf("名称应已存在: %v %v", exists, err)
	}
	exists, err = st.CAPoolNameExists("production-v2", pool.ID)
	if err != nil || exists {
		t.Fatalf("排除自身后名称不应冲突: %v %v", exists, err)
	}

	count, err := st.CountCAPoolsUsingCA("google")
	if err != nil || count != 1 {
		t.Fatalf("CA 引用统计不正确: %d %v", count, err)
	}

	if err := st.DeleteCAPool(pool.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetCAPool(pool.ID); err == nil {
		t.Fatal("删除后不应再读取到调度组")
	}
}

func TestTaskCAPoolIDRoundTrip(t *testing.T) {
	st := newTestStore(t)
	task := &model.Task{
		Name:      "pool task",
		Domains:   []string{"example.com"},
		Challenge: "dns01",
		CA:        "",
		CAPoolID:  42,
		KeyType:   "ecdsa",
		Status:    "pending",
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CAPoolID != 42 {
		t.Fatalf("任务 CAPoolID 未保存: %+v", loaded)
	}
	task.CAPoolID = 43
	if err := st.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CAPoolID != 43 {
		t.Fatalf("任务 CAPoolID 未更新: %+v", loaded)
	}
	count, err := st.CountTasksUsingCAPool(43)
	if err != nil || count != 1 {
		t.Fatalf("任务引用统计不正确: %d %v", count, err)
	}
}
