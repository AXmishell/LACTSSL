package api

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
)

// notFoundBody 复用控制台的全局样式类，保证 404 页面与前端界面一致。
const notFoundBody = `<div class="shell" style="display:block;min-height:100vh">
  <main class="main-content">
    <div class="page notfound-page" style="min-height:calc(100vh - 72px)">
      <section class="k-card notfound-card">
        <div class="notfound-code" aria-hidden="true">404</div>
        <h2>{{title}}</h2>
        <div class="muted" style="margin-top:4px;font-size:14px">{{titleAlt}}</div>
        <p class="notfound-desc" style="margin-top:12px">{{desc}}<br>{{descAlt}}</p>
        <div class="notfound-path">
          <span>URL</span>
          <code class="mono break-all">{{path}}</code>
        </div>
        <div class="auth-hint">{{hint}}<br>{{hintAlt}}</div>
      </section>
    </div>
  </main>
</div>`

var (
	moduleScriptPattern = regexp.MustCompile(`(?is)<script[^>]*\btype=["']module["'][^>]*>\s*</script>\s*`)
	htmlTagPattern      = regexp.MustCompile(`(?is)<html[^>]*>`)
	titlePattern        = regexp.MustCompile(`(?is)<title>.*?</title>`)
	appDivPattern       = regexp.MustCompile(`(?is)<div\s+id="app"\s*>\s*</div>`)
)

// notFoundPage 输出控制台样式的 404 页面，用于未通过安全入口门禁的页面请求。
// 这里不加载应用脚本，只复用构建产物中的全局样式表，避免触发前端路由与会话逻辑。
func (s *Server) notFoundPage(c *gin.Context) {
	loc := locale(c)
	// 同时展示中英文，主语言跟随请求，另一种语言作为补充。
	alt := i18n.ZhCN
	if loc != i18n.EnUS {
		alt = i18n.EnUS
	}
	body := strings.NewReplacer(
		"{{title}}", html.EscapeString(i18n.T(loc, "api.error.page_not_found", nil)),
		"{{titleAlt}}", html.EscapeString(i18n.T(alt, "api.error.page_not_found", nil)),
		"{{desc}}", html.EscapeString(i18n.T(loc, "api.error.page_not_found_desc", nil)),
		"{{descAlt}}", html.EscapeString(i18n.T(alt, "api.error.page_not_found_desc", nil)),
		"{{hint}}", html.EscapeString(i18n.T(loc, "api.error.page_not_found_hint", nil)),
		"{{hintAlt}}", html.EscapeString(i18n.T(alt, "api.error.page_not_found_hint", nil)),
		"{{path}}", html.EscapeString(c.Request.URL.Path),
	).Replace(notFoundBody)

	page, err := s.notFoundDocument(body, loc)
	if err != nil {
		// 前端资源缺失时退回最小页面，至少保证 404 可读。
		page = []byte(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>404 · LACTSSL</title></head><body>` + body + `</body></html>`)
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusNotFound, "text/html; charset=utf-8", page)
}

// notFoundDocument 以构建后的 index.html 为模板，移除应用脚本并注入 404 内容。
func (s *Server) notFoundDocument(body string, loc i18n.Locale) ([]byte, error) {
	data, err := fs.ReadFile(s.WebFS, "index.html")
	if err != nil {
		return nil, err
	}
	doc := moduleScriptPattern.ReplaceAll(data, nil)
	doc = htmlTagPattern.ReplaceAllFunc(doc, func([]byte) []byte {
		return []byte(`<html lang="` + html.EscapeString(string(loc)) + `">`)
	})
	doc = titlePattern.ReplaceAllFunc(doc, func([]byte) []byte {
		return []byte(`<title>404 · LACTSSL</title>`)
	})
	injected := false
	doc = appDivPattern.ReplaceAllFunc(doc, func([]byte) []byte {
		injected = true
		return []byte(`<div id="app">` + body + `</div>`)
	})
	if !injected {
		// 模板结构不符合预期时，至少把内容放进 body，避免 404 页面空白。
		doc = bytes.Replace(doc, []byte("</body>"), []byte(body+"</body>"), 1)
	}
	return doc, nil
}
