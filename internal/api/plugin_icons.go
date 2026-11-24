package api

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/plugin/runtime/icon"
	"lactssl/internal/service"
)

// 服务商图标由插件仓库提供，缓存到 <data_dir>/plugin-icons/<kind>/<owner>/。
// owner 是插件 ID，卸载时可以整目录删除；API 只读取已安装包内的文件，
// 索引图标作为未安装插件与包内缺图时的兜底。
const pluginIconDir = "plugin-icons"

var pluginIconIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func validPluginIconID(value string) bool {
	return pluginIconIDPattern.MatchString(value)
}

func (s *Server) pluginIconRoot(kind string) (string, bool) {
	if strings.TrimSpace(s.DataDir) == "" {
		return "", false
	}
	if kind != pluginKindDNS && kind != pluginKindDeploy {
		return "", false
	}
	return filepath.Join(s.DataDir, pluginIconDir, kind), true
}

func (s *Server) removePluginIcons(kind, owner string) error {
	root, ok := s.pluginIconRoot(kind)
	if !ok || !validPluginIconID(owner) {
		return nil
	}
	if err := os.RemoveAll(filepath.Join(root, owner)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// packageIconMap 读取已安装包内的图标，目录按名称排序保证覆盖顺序稳定。
func (s *Server) packageIconMap() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, kind := range []string{pluginKindDNS, pluginKindDeploy} {
		root, ok := s.pluginIconRoot(kind)
		if !ok {
			continue
		}
		owners, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		sort.Slice(owners, func(i, j int) bool { return owners[i].Name() < owners[j].Name() })
		for _, owner := range owners {
			if !owner.IsDir() || strings.HasPrefix(owner.Name(), ".") {
				continue
			}
			files, err := os.ReadDir(filepath.Join(root, owner.Name()))
			if err != nil {
				continue
			}
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				name := file.Name()
				ext := strings.ToLower(filepath.Ext(name))
				mime := icons.MIME(ext)
				id := strings.TrimSuffix(name, filepath.Ext(name))
				if mime == "" || !validPluginIconID(id) {
					continue
				}
				info, err := file.Info()
				if err != nil || info.Size() == 0 || info.Size() > icons.MaxBytes {
					continue
				}
				data, err := os.ReadFile(filepath.Join(root, owner.Name(), name))
				if err != nil {
					continue
				}
				if out[kind] == nil {
					out[kind] = map[string]string{}
				}
				out[kind][id] = icons.EncodeDataURI(mime, data)
			}
		}
	}
	// 部署历史 ID（如 baota_site）即使索引不可用，也能复用规范图标。
	if icons := out[pluginKindDeploy]; len(icons) > 0 {
		for alias, canonical := range service.DeprecatedDeployTypeCanonical {
			if icon := icons[canonical]; icon != "" {
				icons[alias] = icon
			}
		}
	}
	return out
}

// pluginIconMap 返回已安装包内图标；内置插件未提供图标时由前端回退首字母头像。
func (s *Server) pluginIconMap() map[string]map[string]string {
	out := map[string]map[string]string{pluginKindDNS: {}, pluginKindDeploy: {}}
	for kind, byID := range s.packageIconMap() {
		if out[kind] == nil {
			out[kind] = map[string]string{}
		}
		for id, icon := range byID {
			out[kind][id] = icon
		}
	}
	return out
}

// listPluginIcons 返回当前可用的服务商图标，供凭据页与插件市场使用。
func (s *Server) listPluginIcons(c *gin.Context) {
	c.Header("Cache-Control", "private, max-age=300")
	ok(c, s.pluginIconMap())
}
