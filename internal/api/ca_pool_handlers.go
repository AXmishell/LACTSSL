package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/acme"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/service"
)

type caPoolItemBody struct {
	CAValue  string `json:"ca_value"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
	Enabled  *bool  `json:"enabled"`
}

type caPoolBody struct {
	Name     string           `json:"name"`
	Strategy string           `json:"strategy"`
	Enabled  *bool            `json:"enabled"`
	Items    []caPoolItemBody `json:"items"`
}

func (s *Server) listCAPools(c *gin.Context) {
	list, err := s.Store.ListCAPools()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, list)
}

func (s *Server) createCAPool(c *gin.Context) {
	var body caPoolBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	pool, err := s.buildCAPool(body, 0)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	publicID, err := acme.NewPublicID()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	pool.PublicID = publicID
	if err := s.Store.CreateCAPool(pool); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("ca", pool.ID, "info", "log.ca.pool_created", map[string]any{"name": pool.Name})
	ok(c, pool)
}

func (s *Server) updateCAPool(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	existing, err := s.Store.GetCAPool(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.ca_pool_not_found")
		return
	}
	var body caPoolBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	pool, err := s.buildCAPool(body, existing.ID)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if !pool.Enabled && s.setting(service.CAPoolSettingKey, "0") == strconv.FormatInt(existing.ID, 10) {
		failKey(c, http.StatusConflict, "api.error.ca_pool_is_default")
		return
	}
	pool.ID = existing.ID
	pool.PublicID = existing.PublicID
	pool.Cursor = existing.Cursor
	if err := s.Store.UpdateCAPool(pool); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("ca", pool.ID, "info", "log.ca.pool_updated", map[string]any{"name": pool.Name})
	updated, err := s.Store.GetCAPool(pool.ID)
	if err != nil {
		ok(c, pool)
		return
	}
	ok(c, updated)
}

func (s *Server) deleteCAPool(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	pool, err := s.Store.GetCAPool(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.ca_pool_not_found")
		return
	}
	if s.setting(service.CAPoolSettingKey, "0") == strconv.FormatInt(id, 10) {
		failKey(c, http.StatusConflict, "api.error.ca_pool_is_default")
		return
	}
	tasks, err := s.Store.CountTasksUsingCAPool(id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if tasks > 0 {
		failKey(c, http.StatusConflict, "api.error.ca_pool_has_tasks", "count", tasks)
		return
	}
	if err := s.Store.DeleteCAPool(id); err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	s.Store.AddLogKey("ca", id, "warning", "log.ca.pool_deleted", map[string]any{"name": pool.Name})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.deleted", nil)})
}

func (s *Server) buildCAPool(body caPoolBody, excludeID int64) (*model.CAPool, error) {
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return nil, i18n.E("api.error.ca_pool_name_required")
	}
	if len([]rune(name)) > 80 {
		return nil, i18n.E("api.error.ca_pool_name_too_long")
	}
	exists, err := s.Store.CAPoolNameExists(name, excludeID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, i18n.E("api.error.ca_pool_name_exists")
	}
	strategy := strings.ToLower(strings.TrimSpace(body.Strategy))
	switch strategy {
	case "", model.CAPoolStrategyRoundRobin, "sequential", "round-robin", "roundrobin":
		strategy = model.CAPoolStrategyRoundRobin
	case model.CAPoolStrategyRandom, model.CAPoolStrategyPriority:
	default:
		return nil, i18n.E("api.error.ca_pool_strategy_invalid")
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	pool := &model.CAPool{Name: name, Strategy: strategy, Enabled: enabled}
	seen := map[string]bool{}
	for _, item := range body.Items {
		value := strings.TrimSpace(item.CAValue)
		if value == "" {
			return nil, i18n.E("api.error.ca_pool_item_required")
		}
		if seen[value] {
			return nil, i18n.E("api.error.ca_pool_item_duplicate", "ca", value)
		}
		seen[value] = true
		if _, private := service.ParsePrivateCAValue(value); private {
			return nil, i18n.E("api.error.ca_pool_private_unsupported", "ca", value)
		}
		if _, err := s.Manager.ResolveCA(value); err != nil {
			return nil, i18n.E("api.error.ca_pool_item_invalid", "ca", value)
		}
		itemEnabled := true
		if item.Enabled != nil {
			itemEnabled = *item.Enabled
		}
		weight := item.Weight
		if weight <= 0 {
			weight = 1
		}
		pool.Items = append(pool.Items, model.CAPoolItem{
			CAValue:  value,
			Priority: item.Priority,
			Weight:   weight,
			Enabled:  itemEnabled,
		})
	}
	if len(pool.EnabledItems()) == 0 {
		return nil, i18n.E("api.error.ca_pool_empty")
	}
	return pool, nil
}
