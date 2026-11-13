package service

import (
	"strconv"
	"strings"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/store"
)

const acmeCAPrefix = "acme-ca:"

// ACMECAValue 返回通用 ACME 上游在任务与设置中的引用值。
func ACMECAValue(id int64) string {
	return acmeCAPrefix + strconv.FormatInt(id, 10)
}

// ParseACMECAValue 解析通用 ACME 上游引用。
func ParseACMECAValue(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, acmeCAPrefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, acmeCAPrefix), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// CADefFromACMECA 把数据库中的通用 ACME 上游转换为统一的 CA 定义。
func CADefFromACMECA(ca model.ACMECA) CADef {
	protocol := strings.ToLower(strings.TrimSpace(ca.Protocol))
	if protocol != "v1" {
		protocol = "v2"
	}
	return CADef{
		Value:    ACMECAValue(ca.ID),
		Label:    ca.Name,
		URL:      ca.DirectoryURL,
		Protocol: protocol,
		Custom:   true,
		EAB:      ca.EABRequired,
	}
}

// ResolveCA 查找内置或用户配置的通用 ACME 上游。
func (m *Manager) ResolveCA(value string) (CADef, error) {
	if ca, found := CAInfo(value); found {
		return ca, nil
	}
	id, found := ParseACMECAValue(value)
	if !found {
		return CADef{}, i18n.E("service.acme.ca_unsupported", "ca", value)
	}
	ca, err := m.Store.GetACMECA(id)
	if err != nil {
		return CADef{}, i18n.E("service.acme.ca_unsupported", "ca", value)
	}
	return CADefFromACMECA(*ca), nil
}

// LocalizedCADefs 返回内置 CA 与通用 ACME 上游的合并列表。
func (m *Manager) LocalizedCADefs(locale i18n.Locale) []CADef {
	list := LocalizeCADefs(locale)
	custom, err := m.Store.ListACMECAs()
	if err != nil {
		return list
	}
	for _, ca := range custom {
		list = append(list, CADefFromACMECA(ca))
	}
	return list
}

// LocalizedCADefs 返回内置 CA 与通用 ACME 上游的合并列表。
func LocalizedCADefs(st *store.Store, locale i18n.Locale) []CADef {
	list := LocalizeCADefs(locale)
	custom, err := st.ListACMECAs()
	if err != nil {
		return list
	}
	for _, ca := range custom {
		list = append(list, CADefFromACMECA(ca))
	}
	return list
}
