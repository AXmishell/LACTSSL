// 本文件把官方部署目标的 Native 接口与注册表从 providers module 重新导出。
package deployplugin

import deployapi "lactssl/providers/api/deploy"

type NativeCertificate = deployapi.NativeCertificate
type NativeRequest = deployapi.NativeRequest
type NativeDeployer = deployapi.NativeDeployer
type NativeOptionsRequest = deployapi.NativeOptionsRequest
type NativeOptionLister = deployapi.NativeOptionLister
type NativeVerifier = deployapi.NativeVerifier
type NativePlanRequest = deployapi.NativePlanRequest
type NativePlanObject = deployapi.NativePlanObject
type NativePlanResponse = deployapi.NativePlanResponse
type NativePlanner = deployapi.NativePlanner

func MarkNativeVerifiable(ids ...string) { deployapi.MarkNativeVerifiable(ids...) }
func IsNativeVerifiable(id string) bool  { return deployapi.IsNativeVerifiable(id) }
func MarkNativePlannable(ids ...string)  { deployapi.MarkNativePlannable(ids...) }
func IsNativePlannable(id string) bool   { return deployapi.IsNativePlannable(id) }

func RegisterNative(p NativeDeployer)                 { deployapi.RegisterNative(p) }
func LookupNative(name string) (NativeDeployer, bool) { return deployapi.LookupNative(name) }
func HasNative(name string) bool                      { return deployapi.HasNative(name) }
func ListNative() []NativeDeployer                    { return deployapi.ListNative() }
func ResetNativeForTest()                             { deployapi.ResetNativeForTest() }
