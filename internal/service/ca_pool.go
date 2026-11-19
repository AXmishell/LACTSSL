package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	legoacme "github.com/go-acme/lego/v4/acme"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// CAPoolSettingKey 是全局默认 CA 调度组设置项。
const CAPoolSettingKey = "acme.ca_pool_id"

// CACandidate 是调度组中一个可尝试的 CA。
type CACandidate struct {
	Value    string
	Label    string
	Priority int
	Weight   int
}

// CAFailure 记录一次失败转移尝试。
type CAFailure struct {
	CA    string
	Label string
	Err   error
}

// CAPoolError 表示调度组内所有候选 CA 均失败。
// 若所有失败都是限流，Unwrap 会返回最早可重试的 RateLimitError，
// 从而复用任务层现有的限流延迟逻辑。
type CAPoolError struct {
	PoolID    int64
	PoolName  string
	Attempts  []CAFailure
	RateLimit *RateLimitError
}

func (e *CAPoolError) Error() string {
	if e == nil {
		return ""
	}
	key, params := e.LocalizedMessage()
	return i18n.T(i18n.Default, key, params)
}

// LocalizedMessage 让任务层持久化可翻译消息，同时保留每个候选的错误摘要。
func (e *CAPoolError) LocalizedMessage() (string, map[string]any) {
	details := make([]string, 0, len(e.Attempts))
	for _, attempt := range e.Attempts {
		label := attempt.Label
		if label == "" {
			label = attempt.CA
		}
		details = append(details, fmt.Sprintf("%s: %s", label, i18n.ErrorText(i18n.Default, attempt.Err)))
	}
	return "service.acme.ca_pool_all_failed", map[string]any{
		"count":  len(e.Attempts),
		"detail": strings.Join(details, "; "),
	}
}

func (e *CAPoolError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.RateLimit != nil {
		return e.RateLimit
	}
	// 混合失败时不能把最后一个限流错误暴露给任务层，否则会被误判为
	// “全部候选都限流”并推迟整个任务。优先返回最近的非限流错误。
	for index := len(e.Attempts) - 1; index >= 0; index-- {
		var limited *RateLimitError
		if !errors.As(e.Attempts[index].Err, &limited) {
			return e.Attempts[index].Err
		}
	}
	return nil
}

// SelectCACandidates 读取调度组并生成本次签发的尝试顺序。
// advance 为 true 时轮询策略会推进游标；仅用于复用判断等只读场景时应传 false，
// 避免复用有效证书也消耗轮询位置。
// 失效、禁用或无法解析的候选会被跳过并记录告警。
func (m *Manager) SelectCACandidates(poolID int64, advance bool) ([]CACandidate, error) {
	if poolID <= 0 {
		return nil, i18n.E("service.acme.ca_pool_not_found")
	}
	pool, err := m.Store.GetCAPool(poolID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, i18n.E("service.acme.ca_pool_not_found")
		}
		return nil, err
	}
	if !pool.Enabled {
		return nil, i18n.E("service.acme.ca_pool_disabled", "name", pool.Name)
	}

	candidates := make([]CACandidate, 0, len(pool.Items))
	for _, item := range pool.Items {
		if !item.Enabled {
			continue
		}
		ca, err := m.ResolveCA(item.CAValue)
		if err != nil {
			logger().Warn("跳过失效的 CA 调度项", "pool_id", pool.ID, "ca", item.CAValue, "error", SafeError(err))
			continue
		}
		label := ca.Localized(m.Locale()).Label
		if strings.TrimSpace(label) == "" {
			label = item.CAValue
		}
		weight := item.Weight
		if weight <= 0 {
			weight = 1
		}
		candidates = append(candidates, CACandidate{
			Value:    item.CAValue,
			Label:    label,
			Priority: item.Priority,
			Weight:   weight,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].Value < candidates[j].Value
	})
	if len(candidates) == 0 {
		return nil, i18n.E("service.acme.ca_pool_empty", "name", pool.Name)
	}

	switch strings.ToLower(strings.TrimSpace(pool.Strategy)) {
	case model.CAPoolStrategyRandom:
		rand.Shuffle(len(candidates), func(i, j int) {
			candidates[i], candidates[j] = candidates[j], candidates[i]
		})
	case model.CAPoolStrategyRoundRobin, "sequential", "round-robin", "roundrobin":
		cursor := pool.Cursor
		if advance {
			next, err := m.Store.NextCAPoolCursor(pool.ID)
			if err != nil {
				return nil, err
			}
			cursor = next
		}
		start := int(cursor % int64(len(candidates)))
		if start > 0 {
			candidates = append(candidates[start:], candidates[:start]...)
		}
	}
	return candidates, nil
}

// CAPoolIDForTask 按申请节点、任务、全局默认的顺序解析调度组。
func (m *Manager) CAPoolIDForTask(task *model.Task, cfg model.PipelineConfig) int64 {
	if cfg.CAPoolID > 0 {
		return cfg.CAPoolID
	}
	if task != nil && task.CAPoolID > 0 {
		return task.CAPoolID
	}
	// 任务显式选择了单一 CA 时，不再套用全局默认调度组。
	// 否则老任务会悄悄从固定 CA 切换到调度组，改变签发来源。
	if cfg.CA != "" || (task != nil && task.CA != "") {
		return 0
	}
	raw, _ := m.Store.GetSetting(CAPoolSettingKey)
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// CAPoolName 返回调度组名称，用于日志与错误消息。
func (m *Manager) CAPoolName(id int64) string {
	if id <= 0 {
		return ""
	}
	pool, err := m.Store.GetCAPool(id)
	if err != nil {
		return ""
	}
	return pool.Name
}

// newCAPoolError 汇总失败尝试；全部限流时保留最早的重试时间。
func newCAPoolError(poolID int64, poolName string, attempts []CAFailure) error {
	if len(attempts) == 0 {
		return i18n.E("service.acme.ca_pool_empty", "name", poolName)
	}
	allLimited := true
	var earliest *RateLimitError
	for _, attempt := range attempts {
		var limited *RateLimitError
		if errors.As(attempt.Err, &limited) {
			if earliest == nil || limited.RetryAt.Before(earliest.RetryAt) {
				earliest = limited
			}
			continue
		}
		allLimited = false
	}
	poolErr := &CAPoolError{
		PoolID:   poolID,
		PoolName: poolName,
		Attempts: attempts,
	}
	if allLimited {
		poolErr.RateLimit = earliest
	}
	return poolErr
}

// shouldFailoverCA 判断某个错误是否值得切换到下一个 CA。
// DNS 验证、DNS 账户配置、密钥存储等本地问题默认不切换，
// 避免在必然失败的情况下继续消耗其他 CA 的配额。
func shouldFailoverCA(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	var limited *RateLimitError
	if errors.As(err, &limited) {
		return true
	}

	var problem *legoacme.ProblemDetails
	if errors.As(err, &problem) {
		return failoverForProblem(problem)
	}

	var keyed *i18n.Error
	if errors.As(err, &keyed) && keyed.Key != "" {
		switch keyed.Key {
		case "service.acme.domains_empty",
			"service.acme.dns_account_required",
			"service.acme.dns_provider_unavailable",
			"service.acme.dns_provider_unsupported",
			"service.acme.dns_provider_init_failed",
			"service.acme.dns01_config_failed",
			"service.acme.account_key_parse_failed",
			"service.acme.account_key_generate_failed",
			"service.acme.account_key_save_failed",
			"service.acme.v1_account_key_invalid",
			"service.acme.v1_challenge_failed",
			"service.acme.v1_challenge_timeout",
			"service.acme.ca_unsupported":
			return false
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	// 未知错误默认尝试下一个 CA，符合“一个 CA 失败用下一个”的目标。
	return true
}

func failoverForProblem(problem *legoacme.ProblemDetails) bool {
	if problem == nil {
		return true
	}
	if problem.HTTPStatus == 429 || problem.HTTPStatus >= 500 {
		return true
	}
	problemType := strings.ToLower(problem.Type)
	switch {
	case strings.Contains(problemType, ":dns"),
		strings.Contains(problemType, ":connection"),
		strings.Contains(problemType, ":tls"):
		// 这些错误通常表示验证目标不可达或 DNS 未生效，换 CA 也不会改变结果。
		return false
	case strings.Contains(problemType, ":rateLimited"),
		strings.Contains(problemType, ":caa"),
		strings.Contains(problemType, ":rejectedIdentifier"),
		strings.Contains(problemType, ":unsupportedIdentifier"),
		strings.Contains(problemType, ":unauthorized"),
		strings.Contains(problemType, ":serverInternal"),
		strings.Contains(problemType, ":badNonce"),
		strings.Contains(problemType, ":malformed"),
		strings.Contains(problemType, ":accountDoesNotExist"),
		strings.Contains(problemType, ":externalAccountRequired"),
		strings.Contains(problemType, ":userActionRequired"):
		return true
	}
	return true
}
