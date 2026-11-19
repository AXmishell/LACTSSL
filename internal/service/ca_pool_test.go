package service

import (
	"context"
	"errors"
	"testing"
	"time"

	legoacme "github.com/go-acme/lego/v4/acme"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

func createTestCAPool(t *testing.T, m *Manager, strategy string, items ...model.CAPoolItem) *model.CAPool {
	t.Helper()
	pool := &model.CAPool{
		PublicID: "pool-" + strategy,
		Name:     "test-" + strategy,
		Strategy: strategy,
		Enabled:  true,
		Items:    items,
	}
	if err := m.Store.CreateCAPool(pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func candidateValues(list []CACandidate) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.Value)
	}
	return out
}

func TestSelectCACandidatesRoundRobin(t *testing.T) {
	m := newTestManager(t)
	pool := createTestCAPool(t, m, model.CAPoolStrategyRoundRobin,
		model.CAPoolItem{CAValue: "letsencrypt", Priority: 30, Weight: 1, Enabled: true},
		model.CAPoolItem{CAValue: "zerossl", Priority: 10, Weight: 1, Enabled: true},
		model.CAPoolItem{CAValue: "google", Priority: 20, Weight: 1, Enabled: true},
	)

	want := [][]string{
		{"zerossl", "google", "letsencrypt"},
		{"google", "letsencrypt", "zerossl"},
		{"letsencrypt", "zerossl", "google"},
		{"zerossl", "google", "letsencrypt"},
	}
	for index, expected := range want {
		list, err := m.SelectCACandidates(pool.ID, true)
		if err != nil {
			t.Fatalf("第 %d 次选择失败: %v", index+1, err)
		}
		got := candidateValues(list)
		if len(got) != len(expected) {
			t.Fatalf("第 %d 次候选数量不正确: %v", index+1, got)
		}
		for i := range expected {
			if got[i] != expected[i] {
				t.Fatalf("第 %d 次候选顺序不正确: got %v want %v", index+1, got, expected)
			}
		}
	}
}

func TestSelectCACandidatesReuseDoesNotAdvanceCursor(t *testing.T) {
	m := newTestManager(t)
	pool := createTestCAPool(t, m, model.CAPoolStrategyRoundRobin,
		model.CAPoolItem{CAValue: "letsencrypt", Priority: 10, Weight: 1, Enabled: true},
		model.CAPoolItem{CAValue: "zerossl", Priority: 20, Weight: 1, Enabled: true},
	)
	for i := 0; i < 3; i++ {
		list, err := m.SelectCACandidates(pool.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := candidateValues(list); got[0] != "letsencrypt" {
			t.Fatalf("只读选择不应推进游标: %v", got)
		}
	}
	list, err := m.SelectCACandidates(pool.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateValues(list); got[0] != "letsencrypt" {
		t.Fatalf("首次签发应从第一个候选开始: %v", got)
	}
	list, err = m.SelectCACandidates(pool.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateValues(list); got[0] != "zerossl" {
		t.Fatalf("第二次签发应轮询到第二个候选: %v", got)
	}
}

func TestSelectCACandidatesSkipsDisabledAndInvalid(t *testing.T) {
	m := newTestManager(t)
	pool := createTestCAPool(t, m, model.CAPoolStrategyPriority,
		model.CAPoolItem{CAValue: "letsencrypt", Priority: 10, Weight: 1, Enabled: true},
		model.CAPoolItem{CAValue: "zerossl", Priority: 20, Weight: 1, Enabled: false},
		model.CAPoolItem{CAValue: "acme-ca:9999", Priority: 30, Weight: 1, Enabled: true},
	)
	list, err := m.SelectCACandidates(pool.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	got := candidateValues(list)
	if len(got) != 1 || got[0] != "letsencrypt" {
		t.Fatalf("应只保留有效候选: %v", got)
	}
}

func TestSelectCACandidatesEmptyPool(t *testing.T) {
	m := newTestManager(t)
	pool := createTestCAPool(t, m, model.CAPoolStrategyPriority,
		model.CAPoolItem{CAValue: "letsencrypt", Priority: 10, Weight: 1, Enabled: false},
	)
	if _, err := m.SelectCACandidates(pool.ID, true); err == nil {
		t.Fatal("没有启用候选时应报错")
	}
}

func TestSelectCACandidatesRandomKeepsAllCandidates(t *testing.T) {
	m := newTestManager(t)
	pool := createTestCAPool(t, m, model.CAPoolStrategyRandom,
		model.CAPoolItem{CAValue: "letsencrypt", Priority: 10, Weight: 1, Enabled: true},
		model.CAPoolItem{CAValue: "zerossl", Priority: 20, Weight: 1, Enabled: true},
		model.CAPoolItem{CAValue: "google", Priority: 30, Weight: 1, Enabled: true},
	)
	for i := 0; i < 10; i++ {
		list, err := m.SelectCACandidates(pool.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 3 {
			t.Fatalf("随机策略丢失候选: %v", list)
		}
		seen := map[string]bool{}
		for _, item := range list {
			if seen[item.Value] {
				t.Fatalf("随机策略出现重复候选: %v", list)
			}
			seen[item.Value] = true
		}
	}
}

func TestCAPoolIDForTaskPrecedence(t *testing.T) {
	m := newTestManager(t)
	task := &model.Task{CAPoolID: 2}
	if got := m.CAPoolIDForTask(task, model.PipelineConfig{CAPoolID: 1}); got != 1 {
		t.Fatalf("申请节点应优先: %d", got)
	}
	if got := m.CAPoolIDForTask(task, model.PipelineConfig{}); got != 2 {
		t.Fatalf("任务配置应次之: %d", got)
	}
	if err := m.Store.SetSetting(CAPoolSettingKey, "3"); err != nil {
		t.Fatal(err)
	}
	if got := m.CAPoolIDForTask(&model.Task{}, model.PipelineConfig{}); got != 3 {
		t.Fatalf("全局默认应兜底: %d", got)
	}
	if got := m.CAPoolIDForTask(&model.Task{CA: "letsencrypt"}, model.PipelineConfig{}); got != 0 {
		t.Fatalf("显式单一 CA 不应套用全局默认调度组: %d", got)
	}
}

func TestShouldFailoverCA(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "rate limit", err: &RateLimitError{RetryAt: time.Now()}, want: true},
		{name: "local dns account", err: i18n.E("service.acme.dns_account_required"), want: false},
		{name: "dns validation", err: &legoacme.ProblemDetails{Type: "urn:ietf:params:acme:error:dns"}, want: false},
		{name: "caa", err: &legoacme.ProblemDetails{Type: "urn:ietf:params:acme:error:caa"}, want: true},
		{name: "server error", err: &legoacme.ProblemDetails{HTTPStatus: 500}, want: true},
		{name: "unknown", err: errors.New("boom"), want: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldFailoverCA(test.err); got != test.want {
				t.Fatalf("shouldFailoverCA = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCAPoolErrorPrefersEarliestRateLimit(t *testing.T) {
	early := time.Now().Add(time.Hour)
	late := time.Now().Add(3 * time.Hour)
	err := newCAPoolError(1, "pool", []CAFailure{
		{CA: "letsencrypt", Label: "LE", Err: &RateLimitError{RetryAt: late}},
		{CA: "zerossl", Label: "ZeroSSL", Err: &RateLimitError{RetryAt: early}},
	})
	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("全部限流时应能解析 RateLimitError: %T %v", err, err)
	}
	if !limited.RetryAt.Equal(early) {
		t.Fatalf("应返回最早的重试时间: %v", limited.RetryAt)
	}
}

func TestCAPoolErrorMixedFailures(t *testing.T) {
	for _, attempts := range [][]CAFailure{
		{
			{CA: "letsencrypt", Label: "LE", Err: &RateLimitError{RetryAt: time.Now().Add(time.Hour)}},
			{CA: "zerossl", Label: "ZeroSSL", Err: errors.New("connection refused")},
		},
		{
			{CA: "letsencrypt", Label: "LE", Err: errors.New("connection refused")},
			{CA: "zerossl", Label: "ZeroSSL", Err: &RateLimitError{RetryAt: time.Now().Add(time.Hour)}},
		},
	} {
		err := newCAPoolError(1, "pool", attempts)
		var poolErr *CAPoolError
		if !errors.As(err, &poolErr) {
			t.Fatalf("应返回 CAPoolError: %T", err)
		}
		if poolErr.RateLimit != nil {
			t.Fatal("混合失败不应伪装成全部限流")
		}
		var limited *RateLimitError
		if errors.As(err, &limited) {
			t.Fatal("混合失败不应让 errors.As 误判为限流错误")
		}
		if poolErr.Error() == "" {
			t.Fatal("聚合错误消息不应为空")
		}
	}
}
