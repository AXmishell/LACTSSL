//go:build nobuiltin_deploy

package builtin

import deployplugin "lactssl/internal/plugin/deploy"

// SyncDeploy 在裁剪部署内置实现的构建中清空内置注册。
func SyncDeploy(available, enabled bool, ids map[string]bool) error {
	if err := deployplugin.ReplaceBuiltin(nil); err != nil {
		return err
	}
	deployplugin.SetBuiltinDeployers(nil)
	return nil
}
