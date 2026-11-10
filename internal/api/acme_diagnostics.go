package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

type acmeAccountView struct {
	model.ACMEAccount
	EndpointName string `json:"endpoint_name"`
}

type acmeOrderView struct {
	model.ACMEOrder
	EndpointName     string `json:"endpoint_name"`
	AccountKid       string `json:"account_kid"`
	CertificateState string `json:"certificate_state,omitempty"`
}

func queryInt(c *gin.Context, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(c.Query(key)))
	if err != nil {
		return fallback
	}
	return value
}

func (s *Server) listACMEAccounts(c *gin.Context) {
	endpointID := int64(queryInt(c, "endpoint_id", 0))
	limit := queryInt(c, "limit", 50)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	list, total, err := s.Store.ListACMEAccounts(endpointID, limit, offset)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	endpoints := s.acmeEndpointNames()
	out := make([]acmeAccountView, 0, len(list))
	for _, account := range list {
		out = append(out, acmeAccountView{ACMEAccount: account, EndpointName: endpoints[account.EndpointID]})
	}
	ok(c, gin.H{"items": out, "total": total})
}

func (s *Server) listACMEOrders(c *gin.Context) {
	endpointID := int64(queryInt(c, "endpoint_id", 0))
	limit := queryInt(c, "limit", 50)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	list, total, err := s.Store.ListACMEOrders(endpointID, c.Query("status"), limit, offset)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	endpoints := s.acmeEndpointNames()
	out := make([]acmeOrderView, 0, len(list))
	for _, order := range list {
		view := acmeOrderView{ACMEOrder: order, EndpointName: endpoints[order.EndpointID]}
		if account, err := s.Store.GetACMEAccountByID(order.AccountID); err == nil {
			view.AccountKid = account.Kid
		}
		if order.CertificateID > 0 {
			if certificate, err := s.Store.GetCertificate(order.CertificateID); err == nil {
				view.CertificateState = certificate.Status
			}
		}
		out = append(out, view)
	}
	ok(c, gin.H{"items": out, "total": total})
}

func (s *Server) getACMEOrder(c *gin.Context) {
	id, okID := parseID(c)
	if !okID {
		return
	}
	order, err := s.Store.GetACMEOrderByID(id)
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.acme_order_not_found")
		return
	}
	endpoints := s.acmeEndpointNames()
	view := acmeOrderView{ACMEOrder: *order, EndpointName: endpoints[order.EndpointID]}
	if account, err := s.Store.GetACMEAccountByID(order.AccountID); err == nil {
		view.AccountKid = account.Kid
	}
	authzs, _ := s.Store.ListACMEAuthorizations(order.ID)
	type authzView struct {
		model.ACMEAuthorization
		Challenges []model.ACMEChallenge `json:"challenges"`
	}
	authzViews := make([]authzView, 0, len(authzs))
	for _, authz := range authzs {
		challenges, _ := s.Store.ListACMEChallenges(authz.ID)
		authzViews = append(authzViews, authzView{ACMEAuthorization: authz, Challenges: challenges})
	}
	var certificate *model.Certificate
	if order.CertificateID > 0 {
		certificate, _ = s.Store.GetCertificate(order.CertificateID)
		if certificate != nil {
			view.CertificateState = certificate.Status
		}
	}
	ok(c, gin.H{
		"order":          view,
		"authorizations": authzViews,
		"certificate":    certificate,
	})
}

// acmeCleanupBody 描述清理 ACME 诊断记录的请求条件。
type acmeCleanupBody struct {
	Target        string `json:"target"`
	Scope         string `json:"scope"`
	EndpointID    int64  `json:"endpoint_id"`
	OlderThanDays int    `json:"older_than_days"`
	InactiveDays  int    `json:"inactive_days"`
}

// filter 校验请求条件并转换为数据层筛选条件。
func (b acmeCleanupBody) filter() (store.ACMECleanupFilter, bool) {
	target := strings.ToLower(strings.TrimSpace(b.Target))
	if target == "" {
		target = "orders"
	}
	scope := strings.ToLower(strings.TrimSpace(b.Scope))
	if scope == "" {
		scope = "stale"
	}
	switch target {
	case "orders", "accounts", "all":
	default:
		return store.ACMECleanupFilter{}, false
	}
	switch scope {
	case "stale", "all":
	default:
		return store.ACMECleanupFilter{}, false
	}
	return store.ACMECleanupFilter{
		Orders:        target != "accounts",
		Accounts:      target != "orders",
		Scope:         scope,
		EndpointID:    max(b.EndpointID, 0),
		OlderThanDays: max(b.OlderThanDays, 0),
		InactiveDays:  max(b.InactiveDays, 0),
	}, true
}

// parseACMECleanupBody 解析清理请求，空请求体按默认条件处理。
func parseACMECleanupBody(c *gin.Context) (acmeCleanupBody, bool) {
	var body acmeCleanupBody
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return body, false
	}
	return body, true
}

// cleanupACMEDiagnostics 清理 ACME 诊断记录。默认仅清理失效订单，可叠加端点、时间等条件。
func (s *Server) cleanupACMEDiagnostics(c *gin.Context) {
	body, okBody := parseACMECleanupBody(c)
	if !okBody {
		return
	}
	filter, okFilter := body.filter()
	if !okFilter {
		failKey(c, http.StatusBadRequest, "api.error.invalid_cleanup_scope")
		return
	}

	now := time.Now()
	result, err := s.Store.CleanupACME(filter, now)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if result.Orders > 0 || result.Accounts > 0 {
		s.Store.AddLogKey("acme", 0, "warning", "log.acme.cleaned", map[string]any{"orders": result.Orders, "accounts": result.Accounts})
	}
	ok(c, gin.H{
		"orders":   result.Orders,
		"accounts": result.Accounts,
		"count":    result.Orders + result.Accounts,
		"message":  i18n.T(locale(c), "api.ok.acme_cleaned", map[string]any{"orders": result.Orders, "accounts": result.Accounts}),
	})
}

// previewCleanupACMEDiagnostics 返回符合条件的订单与账户数量，不实际删除。
func (s *Server) previewCleanupACMEDiagnostics(c *gin.Context) {
	body, okBody := parseACMECleanupBody(c)
	if !okBody {
		return
	}
	filter, okFilter := body.filter()
	if !okFilter {
		failKey(c, http.StatusBadRequest, "api.error.invalid_cleanup_scope")
		return
	}
	result, err := s.Store.CountACMECleanup(filter, time.Now())
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, gin.H{"orders": result.Orders, "accounts": result.Accounts, "count": result.Orders + result.Accounts})
}

func (s *Server) acmeEndpointNames() map[int64]string {
	list, err := s.Store.ListACMEEndpoints()
	if err != nil {
		return map[int64]string{}
	}
	out := make(map[int64]string, len(list))
	for _, endpoint := range list {
		out[endpoint.ID] = endpoint.Name
	}
	return out
}
