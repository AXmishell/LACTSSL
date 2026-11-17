package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/pluginrepo"
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

// savePluginIcons 把安装包内的图标原子写入插件专属目录。
// refs 为空时清理该插件的目录，避免升级后残留旧图。
func (s *Server) savePluginIcons(kind, owner string, refs map[string]pluginrepo.IconFile, srcDir string) error {
	root, ok := s.pluginIconRoot(kind)
	if !ok || !validPluginIconID(owner) {
		return nil
	}
	if len(refs) == 0 {
		return s.removePluginIcons(kind, owner)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(root, ".icons-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for id, ref := range refs {
		if !validPluginIconID(id) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(ref.File))
		mime := pluginrepo.IconMIME(ext)
		if mime == "" || ref.File != "icons/"+id+ext {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(srcDir, id+ext))
		if err != nil {
			return err
		}
		if len(data) == 0 || len(data) > pluginrepo.MaxIconBytes {
			return nil
		}
		sum := sha256.Sum256(data)
		if !strings.EqualFold(ref.SHA256, hex.EncodeToString(sum[:])) {
			return nil
		}
		if err := os.WriteFile(filepath.Join(tmp, id+ext), data, 0o644); err != nil {
			return err
		}
	}
	dest := filepath.Join(root, owner)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
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
				mime := pluginrepo.IconMIME(ext)
				id := strings.TrimSuffix(name, filepath.Ext(name))
				if mime == "" || !validPluginIconID(id) {
					continue
				}
				info, err := file.Info()
				if err != nil || info.Size() == 0 || info.Size() > pluginrepo.MaxIconBytes {
					continue
				}
				data, err := os.ReadFile(filepath.Join(root, owner.Name(), name))
				if err != nil {
					continue
				}
				if out[kind] == nil {
					out[kind] = map[string]string{}
				}
				out[kind][id] = pluginrepo.EncodeIconDataURI(mime, data)
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

// cachedRepoIconMap 只读取已签名的本地索引缓存，不触发网络请求。
func (s *Server) cachedRepoIconMap() map[string]map[string]string {
	s.pluginRepoMu.Lock()
	index := s.pluginRepoIndex
	s.pluginRepoMu.Unlock()
	if len(index.Icons) == 0 && s.Store != nil {
		if raw, ok := s.cachedRepoIndexRaw(); ok {
			if err := json.Unmarshal([]byte(raw), &index); err != nil {
				return nil
			}
			index.SanitizeIcons()
		}
	}
	return index.IconMap()
}

// pluginIconMap 合并索引图标与已安装包内图标，后者覆盖前者。
func (s *Server) pluginIconMap() map[string]map[string]string {
	out := map[string]map[string]string{pluginKindDNS: {}, pluginKindDeploy: {}}
	for kind, byID := range s.cachedRepoIconMap() {
		if kind != pluginKindDNS && kind != pluginKindDeploy {
			continue
		}
		if out[kind] == nil {
			out[kind] = map[string]string{}
		}
		for id, icon := range byID {
			out[kind][id] = icon
		}
	}
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

// pluginMarketIcon 为仓库条目挑选展示图标：只使用条目自身 ID 对应的图标。
// 合并包不借用首个 provider 的 logo，避免把某一家的品牌当作整个包的门面；
// 需要包级图标时，可在索引中按包 ID 提供，或用 hide_icon 强制隐藏。
func pluginMarketIcon(plugin pluginrepo.Plugin, icons map[string]map[string]string) string {
	if plugin.HideIcon {
		return ""
	}
	return icons[plugin.Kind][plugin.ID]
}
