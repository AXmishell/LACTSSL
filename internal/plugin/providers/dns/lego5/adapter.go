// Package lego5 提供 lego v5 DNS 服务商到插件协议 v1 的适配层。
//
// lego v5 的 challenge.Provider 使用 context，而插件协议 v1 的
// present/cleanup 是一次性进程调用，没有跨进程 context。这里用
// context.Background 适配；超时与进程终止仍由宿主控制。
package lego5

import (
	"context"

	"github.com/go-acme/lego/v5/challenge"
	dns "lactssl/providers/api/dns"
)

// Wrap 把 lego v5 的 challenge.Provider 适配为插件的 ChallengeProvider。
func Wrap(provider challenge.Provider) dns.NativeChallengeProvider {
	return adapter{provider: provider}
}

type adapter struct {
	provider challenge.Provider
}

// Unwrap 返回底层 lego v5 provider，供 dns/state 反射 recordIDs。
func (a adapter) Unwrap() any { return a.provider }

func (a adapter) Present(domain, token, keyAuth string) error {
	return a.provider.Present(context.Background(), domain, token, keyAuth)
}

func (a adapter) CleanUp(domain, token, keyAuth string) error {
	return a.provider.CleanUp(context.Background(), domain, token, keyAuth)
}
