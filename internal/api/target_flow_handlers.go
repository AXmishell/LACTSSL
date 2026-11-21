package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"lactssl/internal/model"
)

type targetFlowPreviewBody struct {
	Sources   []model.TargetSource      `json:"sources"`
	Selection model.SiteSelection       `json:"selection"`
	Policy    model.CertPolicy          `json:"policy"`
	Overrides []model.CertGroupOverride `json:"group_overrides"`
	PublicCA  *bool                     `json:"public_ca"`
	// NoKeyStorage 与申请节点保持一致，预览时不再推荐复用已有证书。
	NoKeyStorage bool `json:"no_key_storage"`
}

// previewTargetFlow 读取目标站点并返回智能选取与证书分组预览。
// 预览只读，不会写入证书或修改目标系统。
func (s *Server) previewTargetFlow(c *gin.Context) {
	if !s.requireAPIKeyScope(c, apiScopeDeployExecute) {
		return
	}
	var body targetFlowPreviewBody
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	taskInput := &taskBody{
		FlowMode: model.FlowTargetBeta,
		TargetFlow: model.TargetFlowConfig{
			Sources:   body.Sources,
			Selection: body.Selection,
			Policy:    body.Policy,
			Overrides: body.Overrides,
		},
	}
	if err := s.normalizeTargetFlow(taskInput); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	publicCA := true
	if body.PublicCA != nil {
		publicCA = *body.PublicCA
	}
	preview, err := s.Manager.PreviewTargetFlow(c.Request.Context(), taskInput.TargetFlow, locale(c), publicCA, body.NoKeyStorage)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	ok(c, preview)
}
