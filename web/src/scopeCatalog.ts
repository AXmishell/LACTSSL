// 权限展示目录：把后端返回的细粒度分组（17 个）收敛成 6 个可浏览的模块。
// 这里只影响展示顺序与聚合，scope 的取值、校验和鉴权仍以后端 api_keys.go 为准。

export interface ScopeModuleDef {
  value: string
  labelKey: string
  // 后端 api.scope.group.* 的取值。
  groups: string[]
}

export const SCOPE_ALL_GROUP = 'all'

export const SCOPE_MODULES: ScopeModuleDef[] = [
  {
    value: 'certificates',
    labelKey: 'apiKeys.moduleCertificates',
    groups: ['certificates', 'ca', 'private_ca']
  },
  {
    value: 'automation',
    labelKey: 'apiKeys.moduleAutomation',
    groups: ['tasks', 'deploy', 'monitors']
  },
  {
    value: 'credentials',
    labelKey: 'apiKeys.moduleCredentials',
    groups: ['credentials']
  },
  {
    value: 'operations',
    labelKey: 'apiKeys.moduleOperations',
    groups: ['system', 'logs', 'settings', 'security']
  },
  {
    value: 'data',
    labelKey: 'apiKeys.moduleData',
    groups: ['backup', 'upgrade']
  },
  {
    value: 'access',
    labelKey: 'apiKeys.moduleAccess',
    groups: ['api_keys', 'plugins', 'account']
  }
]

const moduleByGroup = new Map<string, string>()
for (const module of SCOPE_MODULES) {
  for (const group of module.groups) moduleByGroup.set(group, module.value)
}

// 后端新增分组时不会从界面上消失，而是落到“其他”模块，避免权限被静默隐藏。
export function moduleOfGroup(group: string): string {
  return moduleByGroup.get(group) || 'other'
}
