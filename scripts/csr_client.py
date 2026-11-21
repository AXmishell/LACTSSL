#!/usr/bin/env python3
"""LACTSSL 客户端 CSR 申请脚本。

客户端在本地生成私钥和 CSR，只把 CSR 提交给 LACTSSL；服务端完成公共 CA
的 DNS-01 验证，或使用管理员指定的私有 CA 直接签名。私钥始终留在客户端。

依赖：Python 3.9+、OpenSSL 1.1.1+（命令行工具）。

交互式向导（推荐首次使用）：
    python3 scripts/csr_client.py --interactive
    # 或直接不带参数在终端运行，也会自动进入交互模式
    python3 scripts/csr_client.py

示例（公共 CA 首次申请）：
    export LACTSSL_URL="https://ssl.example.com"
    export LACTSSL_TOKEN="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    python3 scripts/csr_client.py \
        --domains example.com,www.example.com \
        --dns-account-id 3 \
        --ca letsencrypt \
        --out-dir ./certs

示例（私有 CA 申请，私钥仍在本地生成）：
    python3 scripts/csr_client.py \
        --domains app.internal,api.internal \
        --private-ca-id 2 \
        --out-dir ./certs

续传已有申请（会优先复用上次失败时留下的 <name>.key.new）：
    python3 scripts/csr_client.py \
        --request-id csr_xxxxxxxx \
        --out-dir ./certs

使用已有 CSR 和私钥（例如证书续期）：
    python3 scripts/csr_client.py \
        --csr ./certs/example.com.csr \
        --key ./certs/example.com.key \
        --dns-account-id 3 \
        --out-dir ./certs

用现有私钥重新生成 CSR 并续期（私钥不复制、不修改）：
    python3 scripts/csr_client.py \
        --key ./certs/example.com.key \
        --domains example.com,www.example.com \
        --out-dir ./certs \
        --force

从上次的 .request.json 一键续期：
    python3 scripts/csr_client.py --renew-from ./certs/example.com.request.json

查询/取消申请（可配合 --json 用于自动化）：
    python3 scripts/csr_client.py --list --json
    python3 scripts/csr_client.py --status csr_xxxxxxxx --json
    python3 scripts/csr_client.py --cancel csr_xxxxxxxx --yes

批量申请（JSON 清单，顺序执行；失败时 --on-error stop|continue）：
    python3 scripts/csr_client.py --manifest ./certs.json --json

自动化要点：
    - 进度日志写入 stderr，--json 时 stdout 只有最终 JSON；
    - 输出文件先在 out-dir 下以 .new 暂存，成功后原子替换，失败不会破坏旧证书；
    - 创建申请后立即写 <name>.request.json，断网后可用 --request-id 恢复；
    - 网络异常默认重试 3 次，服务端标记可重试时默认自动 /retry 1 次；
    - 可用 LACTSSL_TOKEN_FILE 或 --token-file 避免令牌出现在命令行参数中；
    - 加密私钥通过环境变量 LACTSSL_KEY_PASSPHRASE 提供口令。

多语言：
    python3 scripts/csr_client.py --lang en-US --help
    # 支持 zh-CN、zh-TW、en-US、ja-JP、ru-RU；也可设置 LACTSSL_LANG

退出码：0 成功；1 通用失败；2 参数/配置；3 认证/权限；4 网络；
        5 服务端申请失败；6 本地校验失败；130 用户取消。
"""

from __future__ import annotations

import argparse
import copy
import datetime
import email.utils
import getpass
import hashlib
import http.client
import ipaddress
import json
import os
import re
import random
import shutil
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass, field
from functools import lru_cache
from pathlib import Path
from typing import Any

__version__ = "1.2.0"
USER_AGENT = f"lactssl-csr-client/{__version__}"
TERMINAL_STATUSES = {"issued", "failed", "cancelled", "expired"}
KNOWN_STATUSES = {"pending", "running", *TERMINAL_STATUSES}
DEFAULT_TIMEOUT = 600
DEFAULT_POLL_INTERVAL = 3.0
DEFAULT_HTTP_TIMEOUT = 35.0
DEFAULT_RETRIES = 3
DEFAULT_AUTO_RETRY = 1
MAX_RESPONSE_BYTES = 1024 * 1024
MAX_SAFE_NAME = 120
STATE_SCHEMA_VERSION = 1
KEY_PASSPHRASE_ENV = "LACTSSL_KEY_PASSPHRASE"
TRANSIENT_STATUS = {408, 425, 429, 500, 502, 503, 504}
PRIVATE_CA_PREFIX = "private-ca:"
DEFAULT_LANGUAGE = "zh-CN"
SUPPORTED_LANGUAGES = ("zh-CN", "zh-TW", "en-US", "ja-JP", "ru-RU")
LANGUAGE_OPTIONS = [
    ("zh-CN", "简体中文"),
    ("zh-TW", "繁體中文"),
    ("en-US", "English"),
    ("ja-JP", "日本語"),
    ("ru-RU", "Русский"),
]
KEY_TYPES = ("ecdsa", "ecdsa384", "ed25519", "rsa2048", "rsa4096")

# ---------------------------------------------------------------------------
# 多语言文案
# ---------------------------------------------------------------------------

MESSAGES: dict[str, dict[str, str]] = {
    "en-US": {
        "cli.description": "Submit a client CSR to LACTSSL and download a public CA or private CA certificate (the private key never leaves the client)",
        "cli.usage": "usage",
        "cli.options": "options",
        "cli.help": "show this help message and exit",
        "cli.epilog": "Examples:\n  python3 scripts/csr_client.py --domains example.com --dns-account-id 3\n  python3 scripts/csr_client.py --domains app.internal --private-ca-id 2\n  python3 scripts/csr_client.py --request-id csr_xxxxxxxx",
        "cli.base_url": "LACTSSL base URL; defaults to LACTSSL_URL or http://127.0.0.1:8080",
        "cli.token": "API key; defaults to LACTSSL_TOKEN",
        "cli.domains": "Domains to request, separated by commas or spaces; may be repeated",
        "cli.csr": "Use an existing CSR file instead of generating a new private key",
        "cli.key": "Existing private key file used to verify the downloaded certificate",
        "cli.request_id": "Resume an existing CSR request without creating a new one",
        "cli.ca": "Public CA identifier, or private-ca:<id> for a private CA",
        "cli.private_ca_id": "Private CA ID to issue from; equivalent to --ca private-ca:<id>",
        "cli.dns_account_id": "DNS account ID for DNS-01; omitted means resolve by server zone bindings",
        "cli.challenge": "Validation method; public CA requests currently support dns01 only",
        "cli.key_type": "Local private key type (default: ecdsa)",
        "cli.out_dir": "Output directory",
        "cli.name": "Output file base name; defaults to the first domain",
        "cli.idempotency_key": "Idempotency key; generated automatically by default",
        "cli.wait_seconds": "Seconds the server waits synchronously on create (0-30)",
        "cli.timeout": "Overall issuance timeout in seconds",
        "cli.poll_interval": "Polling interval in seconds",
        "cli.http_timeout": "Per-request HTTP timeout in seconds",
        "cli.ca_bundle": "Custom CA file used to verify the LACTSSL HTTPS certificate",
        "cli.insecure": "Skip HTTPS certificate verification (not recommended)",
        "cli.force": "Overwrite existing output files",
        "cli.quiet": "Reduce progress output",
        "cli.interactive": "Start the interactive wizard",
        "cli.lang": "Interface language: zh-CN, zh-TW, en-US, ja-JP, ru-RU",
        "app.title": "LACTSSL CSR client · interactive mode",
        "wizard.step": "Step {current}/{total}",
        "wizard.current": "Current settings",
        "app.privacy": "The private key is generated and stored locally only and is never uploaded. Press Ctrl+C to exit at any time.",
        "app.error": "Error: {error}",
        "app.cancelled": "Cancelled",
        "app.cancelled_period": "Cancelled.",
        "app.tty_required": "Interactive mode requires a terminal (TTY); use command-line arguments instead",
        "section.connection": "== Connection ==",
        "section.language": "== Language ==",
        "section.logic": "== Execution logic ==",
        "logic.prompt": "Choose the execution logic:",
        "logic.sequential": "Sequential execution",
        "language.prompt": "Choose the interface language:",
        "section.mode": "== Operation ==",
        "section.request": "== Request ==",
        "section.resume": "== Resume request ==",
        "section.csr": "== Existing CSR ==",
        "section.output": "== Output ==",
        "section.summary": "== Ready to run ==",
        "conn.url": "LACTSSL URL",
        "conn.reuse_token": "An API key was provided. Reuse it?",
        "conn.token": "API key",
        "conn.token_hint": "input is hidden",
        "conn.insecure": "Skip HTTPS certificate verification (not recommended)?",
        "conn.ca_bundle": "Custom CA file (optional, for a self-hosted HTTPS endpoint)",
        "warn.insecure": "Warning: HTTPS certificate verification is disabled; use only for local testing",
        "mode.prompt": "Choose an operation:",
        "mode.new": "Certificate request via public CA (generate the private key and CSR locally)",
        "mode.private": "Certificate request via private CA (generate the private key and CSR locally)",
        "mode.resume": "Resume an existing request (download the certificate only)",
        "mode.existing_csr": "Use an existing CSR and private key",
        "request.domains": "Domains (comma or space separated; the first is the primary domain; use *.example.com for a wildcard)",
        "request.private_domains": "Domains (internal names are allowed; comma or space separated; the first is the primary domain)",
        "request.dns_account": "DNS account ID (optional; leave empty to resolve from zone bindings)",
        "request.public_ca": "Public CA identifier",
        "request.private_ca": "Private CA ID",
        "request.private_ca_optional": "Private CA ID (optional; fill in to issue from a private CA)",
        "request.key_type": "Private key type:",
        "key.ecdsa": "ECDSA P-256 (recommended)",
        "key.rsa2048": "RSA 2048",
        "key.rsa4096": "RSA 4096",
        "resume.id": "Request ID",
        "resume.key": "Local private key path (optional; verifies the certificate matches the key)",
        "csr.path": "CSR file path",
        "csr.key": "Private key path (optional; verifies the certificate matches the key)",
        "csr.domains": "Domains (optional; leave empty to use the CSR SANs; if provided, they must match)",
        "output.dir": "Output directory",
        "output.name": "Output file base name (leave empty to use the first domain)",
        "output.force": "Overwrite existing output files?",
        "summary.url": "Server URL",
        "summary.token": "API key",
        "summary.set": "set",
        "summary.unset": "not set",
        "summary.action": "Operation",
        "summary.logic": "Execution logic",
        "summary.action.resume": "Resume request {id}",
        "summary.action.csr": "Use an existing CSR",
        "summary.action.private": "Certificate request via private CA",
        "summary.action.new": "Certificate request via public CA",
        "summary.domains": "Domains",
        "summary.key": "Private key",
        "summary.csr": "CSR",
        "summary.dns": "DNS account",
        "summary.ca": "CA",
        "summary.private_ca": "Private CA",
        "summary.key_type": "Key type",
        "summary.out_dir": "Output directory",
        "summary.out_name": "Output base name",
        "summary.auto": "auto-resolve",
        "summary.first_domain": "(first domain)",
        "summary.confirm": "Start?",
        "summary.retry": "Return to the wizard and retry?",
        "prompt.required": "  This field is required. Please try again.",
        "prompt.int": "  Please enter an integer.",
        "prompt.min": "  Please enter an integer greater than or equal to {min}.",
        "prompt.yesno": "  Please enter y or n.",
        "prompt.choice": "  Please enter a number from the list.",
        "prompt.file_missing": "  File not found: {path}",
        "prompt.eof": "Input ended; cannot continue interactive mode",
        "prompt.choose": "Choose [1-{count}]: ",
        "prompt.default": "(default)",
        "error.openssl_missing": "The openssl command was not found; install OpenSSL 1.1.1 or newer",
        "error.openssl_failed": "OpenSSL failed: {args}\n{detail}",
        "error.unsupported_key_type": "Unsupported key type: {key_type}",
        "error.openssl_output_missing": "OpenSSL did not generate the private key or CSR",
        "error.csr_no_san": "The CSR contains no DNS SANs; cannot determine the requested domains",
        "error.ca_bundle": "Failed to load the CA certificate: {error}",
        "error.request_failed": "request failed",
        "error.retry_after": " (retry after {seconds} seconds)",
        "error.connect": "Cannot connect to LACTSSL: {reason}",
        "error.bad_create_response": "The server returned an unrecognized request response",
        "error.bad_status_response": "The server returned an unrecognized request state",
        "error.request_status": "Request {id} status: {status}",
        "error.not_issued": "Request did not succeed: {status} {detail}{retry_hint}",
        "error.retry_hint": "; retry with /retry",
        "error.timeout": "Timed out after {timeout} seconds; resume later with --request-id {id}",
        "error.download_not_pem": "Failed to download the {kind} certificate: the response is not a PEM certificate",
        "error.domain_mismatch": "{label} does not match the requested domains (missing: {missing}; extra: {extra})",
        "error.cert_missing_domains": "The certificate does not cover the requested domains: {domains}",
        "error.key_mismatch": "The downloaded certificate does not match the local private key; refusing to save it",
        "error.no_token": "Missing API key; use --token or set LACTSSL_TOKEN",
        "error.key_needs_source": "--key must be used together with --csr or --request-id",
        "error.private_ca_id_invalid": "The private CA ID must be a positive integer",
        "error.ca_conflict": "--private-ca-id conflicts with --ca",
        "error.files_exist": "These files already exist; use --force or change --name/--out-dir:\n  {files}",
        "error.key_not_found": "Private key file not found: {path}",
        "error.csr_not_found": "CSR file not found: {path}",
        "error.request_no_domains": "The request contains no domain information; specify --domains",
        "error.domains_required": "Specify the requested domains with --domains",
        "error.local_key_missing": "Failed to generate the local private key or CSR",
        "error.missing_csr": "Missing CSR",
        "log.key_written": "Generated local private key: {path}",
        "log.csr_written": "Generated local CSR: {path}",
        "log.created": "Created request: {id}",
        "log.issued": "Issuance succeeded; downloading the certificate",
        "log.no_key": "Note: no local private key was provided; skipping the key match check",
        "done.title": "Done.",
        "done.cert": "Certificate",
        "done.chain": "Full chain",
        "done.key": "Private key",
        "done.csr": "CSR",
        "done.meta": "Metadata",
    },
    "zh-CN": {
        "cli.description": "使用 LACTSSL 提交客户端 CSR，下载公共 CA 或私有 CA 证书（私钥不离开客户端）",
        "cli.usage": "用法",
        "cli.options": "选项",
        "cli.help": "显示此帮助信息并退出",
        "cli.epilog": "示例：\n  python3 scripts/csr_client.py --domains example.com --dns-account-id 3\n  python3 scripts/csr_client.py --domains app.internal --private-ca-id 2\n  python3 scripts/csr_client.py --request-id csr_xxxxxxxx",
        "cli.base_url": "LACTSSL 地址，默认读取 LACTSSL_URL 或 http://127.0.0.1:8080",
        "cli.token": "API 密钥，默认读取 LACTSSL_TOKEN",
        "cli.domains": "申请域名，逗号或空格分隔，可重复传入",
        "cli.csr": "使用已有 CSR 文件，不再生成新私钥",
        "cli.key": "已有私钥文件，用于校验下载证书与私钥匹配",
        "cli.request_id": "续传已有 CSR 申请，不创建新申请",
        "cli.ca": "公共 CA 标识；使用私有 CA 时填写 private-ca:<id>",
        "cli.private_ca_id": "用于签发的私有 CA ID，等价于 --ca private-ca:<id>",
        "cli.dns_account_id": "用于 DNS-01 的 DNS 账户 ID；省略时按服务端域名绑定自动解析",
        "cli.challenge": "验证方式；公共 CA 申请目前仅支持 dns01",
        "cli.key_type": "本地私钥类型，默认 ecdsa",
        "cli.out_dir": "输出目录",
        "cli.name": "输出文件基名，默认使用首个域名",
        "cli.idempotency_key": "幂等键，默认自动生成",
        "cli.wait_seconds": "创建申请时服务端同步等待秒数（0-30）",
        "cli.timeout": "整体签发超时秒数",
        "cli.poll_interval": "轮询间隔秒数",
        "cli.http_timeout": "单次 HTTP 超时秒数",
        "cli.ca_bundle": "自定义 CA 证书文件，用于校验 LACTSSL HTTPS",
        "cli.insecure": "跳过 HTTPS 证书校验（不推荐）",
        "cli.force": "覆盖已存在的输出文件",
        "cli.quiet": "减少进度输出",
        "cli.interactive": "进入交互式向导，逐项询问缺少的参数",
        "cli.lang": "界面语言：zh-CN、zh-TW、en-US、ja-JP、ru-RU",
        "app.title": "LACTSSL CSR 客户端 · 交互模式",
        "wizard.step": "步骤 {current}/{total}",
        "wizard.current": "当前配置",
        "app.privacy": "私钥只在本地生成并保存，不会上传到服务端。按 Ctrl+C 可随时退出。",
        "app.error": "错误: {error}",
        "app.cancelled": "已取消",
        "app.cancelled_period": "已取消。",
        "app.tty_required": "交互模式需要在终端中运行（未检测到 TTY），或改用命令行参数",
        "section.connection": "== 连接设置 ==",
        "section.language": "== 语言 ==",
        "section.logic": "== 操作逻辑 ==",
        "logic.prompt": "请选择执行逻辑：",
        "logic.sequential": "顺序执行",
        "language.prompt": "请选择界面语言：",
        "section.mode": "== 申请方式 ==",
        "section.request": "== 申请内容 ==",
        "section.resume": "== 续传申请 ==",
        "section.csr": "== 已有 CSR ==",
        "section.output": "== 输出设置 ==",
        "section.summary": "== 即将执行 ==",
        "conn.url": "LACTSSL 地址",
        "conn.reuse_token": "检测到已提供的 API 密钥，是否沿用？",
        "conn.token": "API 密钥",
        "conn.token_hint": "输入不回显",
        "conn.insecure": "跳过 HTTPS 证书校验（不推荐）？",
        "conn.ca_bundle": "自定义 CA 证书文件（可选，用于自建 HTTPS）",
        "warn.insecure": "警告: 已跳过 HTTPS 证书校验，仅建议在本地测试使用",
        "mode.prompt": "请选择操作：",
        "mode.new": "基于公共CA证书申请（本地生成私钥和 CSR）",
        "mode.private": "基于私有CA证书申请（本地生成私钥和 CSR）",
        "mode.resume": "续传已有申请（只下载证书，不创建新申请）",
        "mode.existing_csr": "使用已有 CSR 和私钥",
        "request.domains": "申请域名（逗号或空格分隔，首个为主域名，通配符写作 *.example.com）",
        "request.private_domains": "申请域名（支持内网域名，逗号或空格分隔，首个为主域名）",
        "request.dns_account": "DNS 账户 ID（可选，留空按域名绑定自动解析）",
        "request.public_ca": "公共 CA 标识",
        "request.private_ca": "私有 CA ID",
        "request.private_ca_optional": "私有 CA ID（可选，填写后使用私有 CA 签发）",
        "request.key_type": "私钥类型：",
        "key.ecdsa": "ECDSA P-256（推荐）",
        "key.rsa2048": "RSA 2048",
        "key.rsa4096": "RSA 4096",
        "resume.id": "申请 ID",
        "resume.key": "本地私钥路径（可选，用于校验证书与私钥匹配）",
        "csr.path": "CSR 文件路径",
        "csr.key": "私钥文件路径（可选，用于校验证书与私钥匹配）",
        "csr.domains": "申请域名（可选，留空以 CSR 中的 SAN 为准；填写则做一致性校验）",
        "output.dir": "输出目录",
        "output.name": "输出文件基名（留空使用首个域名）",
        "output.force": "若输出文件已存在，是否覆盖？",
        "summary.url": "服务地址",
        "summary.token": "API 密钥",
        "summary.set": "已设置",
        "summary.unset": "未设置",
        "summary.action": "操作",
        "summary.logic": "执行逻辑",
        "summary.action.resume": "续传申请 {id}",
        "summary.action.csr": "使用已有 CSR",
        "summary.action.private": "基于私有CA证书申请",
        "summary.action.new": "基于公共CA证书申请",
        "summary.domains": "域名",
        "summary.key": "私钥",
        "summary.csr": "CSR",
        "summary.dns": "DNS 账户",
        "summary.ca": "CA",
        "summary.private_ca": "私有 CA",
        "summary.key_type": "私钥类型",
        "summary.out_dir": "输出目录",
        "summary.out_name": "输出基名",
        "summary.auto": "自动解析",
        "summary.first_domain": "（首个域名）",
        "summary.confirm": "确认开始？",
        "summary.retry": "返回向导修改配置后重试？",
        "prompt.required": "  该项不能为空，请重新输入。",
        "prompt.int": "  请输入整数。",
        "prompt.min": "  请输入不小于 {min} 的整数。",
        "prompt.yesno": "  请输入 y 或 n。",
        "prompt.choice": "  请输入列表中的序号。",
        "prompt.file_missing": "  文件不存在: {path}",
        "prompt.eof": "输入已结束，无法继续交互模式",
        "prompt.choose": "请选择 [1-{count}]: ",
        "prompt.default": "（默认）",
        "error.openssl_missing": "未找到 openssl 命令，请先安装 OpenSSL 1.1.1 或更高版本",
        "error.openssl_failed": "OpenSSL 执行失败: {args}\n{detail}",
        "error.unsupported_key_type": "不支持的密钥类型: {key_type}",
        "error.openssl_output_missing": "OpenSSL 未生成私钥或 CSR",
        "error.csr_no_san": "CSR 中没有 DNS SAN，无法确定申请域名",
        "error.ca_bundle": "无法加载 CA 证书: {error}",
        "error.request_failed": "请求失败",
        "error.retry_after": "（建议 {seconds} 秒后重试）",
        "error.connect": "无法连接 LACTSSL: {reason}",
        "error.bad_create_response": "服务端返回了无法识别的申请响应",
        "error.bad_status_response": "服务端返回了无法识别的申请状态",
        "error.request_status": "申请 {id} 状态: {status}",
        "error.not_issued": "申请未成功: {status} {detail}{retry_hint}",
        "error.retry_hint": "，可调用 /retry 重试",
        "error.timeout": "等待签发超时（{timeout} 秒），可稍后用 --request-id {id} 续传",
        "error.download_not_pem": "下载 {kind} 证书失败：响应不是 PEM 证书",
        "error.domain_mismatch": "{label}与申请域名不一致（缺少: {missing}；多出: {extra}）",
        "error.cert_missing_domains": "证书未覆盖请求域名: {domains}",
        "error.key_mismatch": "下载证书与本地私钥不匹配，已拒绝保存",
        "error.no_token": "缺少 API 密钥，请使用 --token 或设置 LACTSSL_TOKEN",
        "error.key_needs_source": "--key 需要与 --csr 或 --request-id 一起使用",
        "error.private_ca_id_invalid": "私有 CA ID 必须是正整数",
        "error.ca_conflict": "--private-ca-id 与 --ca 冲突",
        "error.files_exist": "以下文件已存在，使用 --force 覆盖或更换 --name/--out-dir:\n  {files}",
        "error.key_not_found": "私钥文件不存在: {path}",
        "error.csr_not_found": "CSR 文件不存在: {path}",
        "error.request_no_domains": "申请中没有域名信息，请使用 --domains 指定",
        "error.domains_required": "请使用 --domains 指定申请域名",
        "error.local_key_missing": "本地私钥或 CSR 生成失败",
        "error.missing_csr": "缺少 CSR",
        "log.key_written": "已生成本地私钥: {path}",
        "log.csr_written": "已生成本地 CSR: {path}",
        "log.created": "已创建申请: {id}",
        "log.issued": "签发成功，正在下载证书",
        "log.no_key": "提示: 未提供本地私钥，跳过证书与私钥匹配校验",
        "done.title": "完成。",
        "done.cert": "证书",
        "done.chain": "完整链",
        "done.key": "私钥",
        "done.csr": "CSR",
        "done.meta": "元数据",
    },
    "zh-TW": {
        "cli.description": "使用 LACTSSL 提交客戶端 CSR，下載公共 CA 或私有 CA 憑證（私鑰不離開客戶端）",
        "cli.usage": "用法",
        "cli.options": "選項",
        "cli.help": "顯示此說明並結束",
        "cli.epilog": "範例：\n  python3 scripts/csr_client.py --domains example.com --dns-account-id 3\n  python3 scripts/csr_client.py --domains app.internal --private-ca-id 2\n  python3 scripts/csr_client.py --request-id csr_xxxxxxxx",
        "cli.base_url": "LACTSSL 位址，預設讀取 LACTSSL_URL 或 http://127.0.0.1:8080",
        "cli.token": "API 金鑰，預設讀取 LACTSSL_TOKEN",
        "cli.domains": "申請網域，逗號或空白分隔，可重複傳入",
        "cli.csr": "使用既有 CSR 檔案，不再產生新私鑰",
        "cli.key": "既有私鑰檔案，用於校驗下載憑證與私鑰匹配",
        "cli.request_id": "續傳既有 CSR 申請，不建立新申請",
        "cli.ca": "公共 CA 標識；使用私有 CA 時填寫 private-ca:<id>",
        "cli.private_ca_id": "用於簽發的私有 CA ID，等價於 --ca private-ca:<id>",
        "cli.dns_account_id": "用於 DNS-01 的 DNS 帳戶 ID；省略時依服務端網域綁定自動解析",
        "cli.challenge": "驗證方式；公共 CA 申請目前僅支援 dns01",
        "cli.key_type": "本機私鑰類型，預設 ecdsa",
        "cli.out_dir": "輸出目錄",
        "cli.name": "輸出檔案基名，預設使用首個網域",
        "cli.idempotency_key": "冪等鍵，預設自動產生",
        "cli.wait_seconds": "建立申請時服務端同步等待秒數（0-30）",
        "cli.timeout": "整體簽發逾時秒數",
        "cli.poll_interval": "輪詢間隔秒數",
        "cli.http_timeout": "單次 HTTP 逾時秒數",
        "cli.ca_bundle": "自訂 CA 憑證檔案，用於校驗 LACTSSL HTTPS",
        "cli.insecure": "跳過 HTTPS 憑證校驗（不建議）",
        "cli.force": "覆寫已存在的輸出檔案",
        "cli.quiet": "減少進度輸出",
        "cli.interactive": "進入互動式精靈，逐項詢問缺少的參數",
        "cli.lang": "介面語言：zh-CN、zh-TW、en-US、ja-JP、ru-RU",
        "app.title": "LACTSSL CSR 客戶端 · 互動模式",
        "wizard.step": "步驟 {current}/{total}",
        "wizard.current": "目前設定",
        "app.privacy": "私鑰只在本機產生並保存，不會上傳到服務端。按 Ctrl+C 可隨時退出。",
        "app.error": "錯誤: {error}",
        "app.cancelled": "已取消",
        "app.cancelled_period": "已取消。",
        "app.tty_required": "互動模式需要在終端機中執行（未偵測到 TTY），或改用命令列參數",
        "section.connection": "== 連線設定 ==",
        "section.language": "== 語言 ==",
        "section.logic": "== 操作邏輯 ==",
        "logic.prompt": "請選擇執行邏輯：",
        "logic.sequential": "順序執行",
        "language.prompt": "請選擇介面語言：",
        "section.mode": "== 申請方式 ==",
        "section.request": "== 申請內容 ==",
        "section.resume": "== 續傳申請 ==",
        "section.csr": "== 既有 CSR ==",
        "section.output": "== 輸出設定 ==",
        "section.summary": "== 即將執行 ==",
        "conn.url": "LACTSSL 位址",
        "conn.reuse_token": "偵測到已提供的 API 金鑰，是否沿用？",
        "conn.token": "API 金鑰",
        "conn.token_hint": "輸入不回顯",
        "conn.insecure": "跳過 HTTPS 憑證校驗（不建議）？",
        "conn.ca_bundle": "自訂 CA 憑證檔案（可選，用於自建 HTTPS）",
        "warn.insecure": "警告: 已跳過 HTTPS 憑證校驗，僅建議在本機測試使用",
        "mode.prompt": "請選擇操作：",
        "mode.new": "基於公共CA憑證申請（本機產生私鑰和 CSR）",
        "mode.private": "基於私有CA憑證申請（本機產生私鑰和 CSR）",
        "mode.resume": "續傳既有申請（只下載憑證，不建立新申請）",
        "mode.existing_csr": "使用既有 CSR 和私鑰",
        "request.domains": "申請網域（逗號或空白分隔，首個為主網域，萬用字元寫作 *.example.com）",
        "request.private_domains": "申請網域（支援內網網域，逗號或空白分隔，首個為主網域）",
        "request.dns_account": "DNS 帳戶 ID（可選，留空依網域綁定自動解析）",
        "request.public_ca": "公共 CA 標識",
        "request.private_ca": "私有 CA ID",
        "request.private_ca_optional": "私有 CA ID（可選，填寫後使用私有 CA 簽發）",
        "request.key_type": "私鑰類型：",
        "key.ecdsa": "ECDSA P-256（建議）",
        "key.rsa2048": "RSA 2048",
        "key.rsa4096": "RSA 4096",
        "resume.id": "申請 ID",
        "resume.key": "本機私鑰路徑（可選，用於校驗憑證與私鑰匹配）",
        "csr.path": "CSR 檔案路徑",
        "csr.key": "私鑰檔案路徑（可選，用於校驗憑證與私鑰匹配）",
        "csr.domains": "申請網域（可選，留空以 CSR 中的 SAN 為準；填寫則做一致性校驗）",
        "output.dir": "輸出目錄",
        "output.name": "輸出檔案基名（留空使用首個網域）",
        "output.force": "若輸出檔案已存在，是否覆寫？",
        "summary.url": "服務位址",
        "summary.token": "API 金鑰",
        "summary.set": "已設定",
        "summary.unset": "未設定",
        "summary.action": "操作",
        "summary.logic": "執行邏輯",
        "summary.action.resume": "續傳申請 {id}",
        "summary.action.csr": "使用既有 CSR",
        "summary.action.private": "基於私有CA憑證申請",
        "summary.action.new": "基於公共CA憑證申請",
        "summary.domains": "網域",
        "summary.key": "私鑰",
        "summary.csr": "CSR",
        "summary.dns": "DNS 帳戶",
        "summary.ca": "CA",
        "summary.private_ca": "私有 CA",
        "summary.key_type": "私鑰類型",
        "summary.out_dir": "輸出目錄",
        "summary.out_name": "輸出基名",
        "summary.auto": "自動解析",
        "summary.first_domain": "（首個網域）",
        "summary.confirm": "確認開始？",
        "summary.retry": "返回精靈修改設定後重試？",
        "prompt.required": "  該項不能為空，請重新輸入。",
        "prompt.int": "  請輸入整數。",
        "prompt.min": "  請輸入不小於 {min} 的整數。",
        "prompt.yesno": "  請輸入 y 或 n。",
        "prompt.choice": "  請輸入列表中的序號。",
        "prompt.file_missing": "  檔案不存在: {path}",
        "prompt.eof": "輸入已結束，無法繼續互動模式",
        "prompt.choose": "請選擇 [1-{count}]: ",
        "prompt.default": "（預設）",
        "error.openssl_missing": "找不到 openssl 命令，請先安裝 OpenSSL 1.1.1 或更新版本",
        "error.openssl_failed": "OpenSSL 執行失敗: {args}\n{detail}",
        "error.unsupported_key_type": "不支援的金鑰類型: {key_type}",
        "error.openssl_output_missing": "OpenSSL 未產生私鑰或 CSR",
        "error.csr_no_san": "CSR 中沒有 DNS SAN，無法確定申請網域",
        "error.ca_bundle": "無法載入 CA 憑證: {error}",
        "error.request_failed": "請求失敗",
        "error.retry_after": "（建議 {seconds} 秒後重試）",
        "error.connect": "無法連線 LACTSSL: {reason}",
        "error.bad_create_response": "服務端返回了無法辨識的申請回應",
        "error.bad_status_response": "服務端返回了無法辨識的申請狀態",
        "error.request_status": "申請 {id} 狀態: {status}",
        "error.not_issued": "申請未成功: {status} {detail}{retry_hint}",
        "error.retry_hint": "，可呼叫 /retry 重試",
        "error.timeout": "等待簽發逾時（{timeout} 秒），可稍後用 --request-id {id} 續傳",
        "error.download_not_pem": "下載 {kind} 憑證失敗：回應不是 PEM 憑證",
        "error.domain_mismatch": "{label}與申請網域不一致（缺少: {missing}；多出: {extra}）",
        "error.cert_missing_domains": "憑證未覆蓋請求網域: {domains}",
        "error.key_mismatch": "下載憑證與本機私鑰不匹配，已拒絕保存",
        "error.no_token": "缺少 API 金鑰，請使用 --token 或設定 LACTSSL_TOKEN",
        "error.key_needs_source": "--key 需要與 --csr 或 --request-id 一起使用",
        "error.private_ca_id_invalid": "私有 CA ID 必須是正整數",
        "error.ca_conflict": "--private-ca-id 與 --ca 衝突",
        "error.files_exist": "以下檔案已存在，使用 --force 覆寫或更換 --name/--out-dir:\n  {files}",
        "error.key_not_found": "私鑰檔案不存在: {path}",
        "error.csr_not_found": "CSR 檔案不存在: {path}",
        "error.request_no_domains": "申請中沒有網域資訊，請使用 --domains 指定",
        "error.domains_required": "請使用 --domains 指定申請網域",
        "error.local_key_missing": "本機私鑰或 CSR 產生失敗",
        "error.missing_csr": "缺少 CSR",
        "log.key_written": "已產生本機私鑰: {path}",
        "log.csr_written": "已產生本機 CSR: {path}",
        "log.created": "已建立申請: {id}",
        "log.issued": "簽發成功，正在下載憑證",
        "log.no_key": "提示: 未提供本機私鑰，跳過憑證與私鑰匹配校驗",
        "done.title": "完成。",
        "done.cert": "憑證",
        "done.chain": "完整鏈",
        "done.key": "私鑰",
        "done.csr": "CSR",
        "done.meta": "中繼資料",
    },
    "ja-JP": {
        "cli.description": "LACTSSL にクライアント CSR を送信し、パブリック CA またはプライベート CA の証明書をダウンロードします（秘密キーはクライアントから出ません）",
        "cli.usage": "使用法",
        "cli.options": "オプション",
        "cli.help": "このヘルプを表示して終了",
        "cli.epilog": "例:\n  python3 scripts/csr_client.py --domains example.com --dns-account-id 3\n  python3 scripts/csr_client.py --domains app.internal --private-ca-id 2\n  python3 scripts/csr_client.py --request-id csr_xxxxxxxx",
        "cli.base_url": "LACTSSL のベース URL。既定値は LACTSSL_URL または http://127.0.0.1:8080",
        "cli.token": "API キー。既定値は LACTSSL_TOKEN",
        "cli.domains": "申請するドメイン。カンマまたは空白区切りで複数指定できます",
        "cli.csr": "新しい秘密キーを生成せず、既存の CSR ファイルを使用します",
        "cli.key": "ダウンロードした証明書との一致確認に使う既存の秘密キー",
        "cli.request_id": "新しい申請を作成せず、既存の CSR 申請を再開します",
        "cli.ca": "パブリック CA の識別子。プライベート CA は private-ca:<id>",
        "cli.private_ca_id": "署名に使用するプライベート CA ID（--ca private-ca:<id> と同等）",
        "cli.dns_account_id": "DNS-01 に使用する DNS アカウント ID。省略時はサーバーのゾーン紐付けで自動解決",
        "cli.challenge": "検証方式。パブリック CA 申請は現在 dns01 のみ対応",
        "cli.key_type": "ローカル秘密キーの種類（既定: ecdsa）",
        "cli.out_dir": "出力ディレクトリ",
        "cli.name": "出力ファイルのベース名。既定値は最初のドメイン",
        "cli.idempotency_key": "冪等キー。既定では自動生成",
        "cli.wait_seconds": "作成時にサーバーが同期的に待機する秒数（0-30）",
        "cli.timeout": "発行全体のタイムアウト秒数",
        "cli.poll_interval": "ポーリング間隔の秒数",
        "cli.http_timeout": "1 回の HTTP タイムアウト秒数",
        "cli.ca_bundle": "LACTSSL の HTTPS 証明書検証に使うカスタム CA ファイル",
        "cli.insecure": "HTTPS 証明書の検証をスキップ（非推奨）",
        "cli.force": "既存の出力ファイルを上書き",
        "cli.quiet": "進捗出力を減らす",
        "cli.interactive": "対話ウィザードを開始",
        "cli.lang": "表示言語: zh-CN、zh-TW、en-US、ja-JP、ru-RU",
        "app.title": "LACTSSL CSR クライアント · 対話モード",
        "wizard.step": "ステップ {current}/{total}",
        "wizard.current": "現在の設定",
        "app.privacy": "秘密キーはローカルでのみ生成・保存され、アップロードされません。Ctrl+C でいつでも終了できます。",
        "app.error": "エラー: {error}",
        "app.cancelled": "キャンセルしました",
        "app.cancelled_period": "キャンセルしました。",
        "app.tty_required": "対話モードは端末（TTY）で実行してください。コマンドライン引数を使用してください",
        "section.connection": "== 接続設定 ==",
        "section.language": "== 言語 ==",
        "section.logic": "== 実行ロジック ==",
        "logic.prompt": "実行ロジックを選択してください:",
        "logic.sequential": "順次実行",
        "language.prompt": "表示言語を選択してください:",
        "section.mode": "== 操作 ==",
        "section.request": "== 申請内容 ==",
        "section.resume": "== 申請の再開 ==",
        "section.csr": "== 既存の CSR ==",
        "section.output": "== 出力設定 ==",
        "section.summary": "== 実行内容 ==",
        "conn.url": "LACTSSL URL",
        "conn.reuse_token": "API キーが指定されています。再利用しますか？",
        "conn.token": "API キー",
        "conn.token_hint": "入力は表示されません",
        "conn.insecure": "HTTPS 証明書の検証をスキップしますか（非推奨）？",
        "conn.ca_bundle": "カスタム CA ファイル（任意、自己ホスト HTTPS 用）",
        "warn.insecure": "警告: HTTPS 証明書の検証が無効です。ローカルテストでのみ使用してください",
        "mode.prompt": "操作を選択してください:",
        "mode.new": "パブリック CA による証明書申請（秘密キーと CSR をローカル生成）",
        "mode.private": "プライベート CA による証明書申請（秘密キーと CSR をローカル生成）",
        "mode.resume": "既存申請の再開（証明書のダウンロードのみ）",
        "mode.existing_csr": "既存の CSR と秘密キーを使用",
        "request.domains": "ドメイン（カンマまたは空白区切り、先頭がメインドメイン、ワイルドカードは *.example.com）",
        "request.private_domains": "ドメイン（内部名も使用可、カンマまたは空白区切り、先頭がメインドメイン）",
        "request.dns_account": "DNS アカウント ID（任意、空欄ならゾーン紐付けから自動解決）",
        "request.public_ca": "パブリック CA の識別子",
        "request.private_ca": "プライベート CA ID",
        "request.private_ca_optional": "プライベート CA ID（任意、入力するとプライベート CA で署名）",
        "request.key_type": "秘密キーの種類:",
        "key.ecdsa": "ECDSA P-256（推奨）",
        "key.rsa2048": "RSA 2048",
        "key.rsa4096": "RSA 4096",
        "resume.id": "申請 ID",
        "resume.key": "ローカル秘密キーのパス（任意、証明書との一致確認用）",
        "csr.path": "CSR ファイルのパス",
        "csr.key": "秘密キーのパス（任意、証明書との一致確認用）",
        "csr.domains": "ドメイン（任意、空欄なら CSR の SAN を使用。入力時は一致が必要）",
        "output.dir": "出力ディレクトリ",
        "output.name": "出力ファイルのベース名（空欄なら最初のドメイン）",
        "output.force": "既存の出力ファイルを上書きしますか？",
        "summary.url": "サーバー URL",
        "summary.token": "API キー",
        "summary.set": "設定済み",
        "summary.unset": "未設定",
        "summary.action": "操作",
        "summary.logic": "実行ロジック",
        "summary.action.resume": "申請 {id} を再開",
        "summary.action.csr": "既存の CSR を使用",
        "summary.action.private": "プライベート CA による証明書申請",
        "summary.action.new": "パブリック CA による証明書申請",
        "summary.domains": "ドメイン",
        "summary.key": "秘密キー",
        "summary.csr": "CSR",
        "summary.dns": "DNS アカウント",
        "summary.ca": "CA",
        "summary.private_ca": "プライベート CA",
        "summary.key_type": "秘密キーの種類",
        "summary.out_dir": "出力ディレクトリ",
        "summary.out_name": "出力ベース名",
        "summary.auto": "自動解決",
        "summary.first_domain": "（最初のドメイン）",
        "summary.confirm": "開始しますか？",
        "summary.retry": "ウィザードに戻って設定を変更し、再試行しますか？",
        "prompt.required": "  この項目は必須です。再入力してください。",
        "prompt.int": "  整数を入力してください。",
        "prompt.min": "  {min} 以上の整数を入力してください。",
        "prompt.yesno": "  y または n を入力してください。",
        "prompt.choice": "  一覧の番号を入力してください。",
        "prompt.file_missing": "  ファイルが存在しません: {path}",
        "prompt.eof": "入力が終了したため、対話モードを続行できません",
        "prompt.choose": "選択 [1-{count}]: ",
        "prompt.default": "（既定）",
        "error.openssl_missing": "openssl コマンドが見つかりません。OpenSSL 1.1.1 以降をインストールしてください",
        "error.openssl_failed": "OpenSSL の実行に失敗しました: {args}\n{detail}",
        "error.unsupported_key_type": "未対応のキー種別: {key_type}",
        "error.openssl_output_missing": "OpenSSL が秘密キーまたは CSR を生成しませんでした",
        "error.csr_no_san": "CSR に DNS SAN がないため、申請ドメインを特定できません",
        "error.ca_bundle": "CA 証明書を読み込めません: {error}",
        "error.request_failed": "リクエストに失敗しました",
        "error.retry_after": "（{seconds} 秒後に再試行してください）",
        "error.connect": "LACTSSL に接続できません: {reason}",
        "error.bad_create_response": "サーバーが認識できない申請レスポンスを返しました",
        "error.bad_status_response": "サーバーが認識できない申請状態を返しました",
        "error.request_status": "申請 {id} の状態: {status}",
        "error.not_issued": "申請が成功しませんでした: {status} {detail}{retry_hint}",
        "error.retry_hint": "。/retry で再試行できます",
        "error.timeout": "{timeout} 秒でタイムアウトしました。後で --request-id {id} で再開できます",
        "error.download_not_pem": "{kind} 証明書をダウンロードできませんでした: レスポンスが PEM 証明書ではありません",
        "error.domain_mismatch": "{label}が申請ドメインと一致しません（不足: {missing}、余分: {extra}）",
        "error.cert_missing_domains": "証明書が申請ドメインをカバーしていません: {domains}",
        "error.key_mismatch": "ダウンロードした証明書がローカル秘密キーと一致しません。保存を中止しました",
        "error.no_token": "API キーがありません。--token を指定するか LACTSSL_TOKEN を設定してください",
        "error.key_needs_source": "--key は --csr または --request-id と一緒に指定してください",
        "error.private_ca_id_invalid": "プライベート CA ID は正の整数である必要があります",
        "error.ca_conflict": "--private-ca-id と --ca が競合しています",
        "error.files_exist": "次のファイルが既に存在します。--force で上書きするか --name/--out-dir を変更してください:\n  {files}",
        "error.key_not_found": "秘密キーファイルが存在しません: {path}",
        "error.csr_not_found": "CSR ファイルが存在しません: {path}",
        "error.request_no_domains": "申請にドメイン情報がありません。--domains を指定してください",
        "error.domains_required": "--domains で申請ドメインを指定してください",
        "error.local_key_missing": "ローカル秘密キーまたは CSR の生成に失敗しました",
        "error.missing_csr": "CSR がありません",
        "log.key_written": "ローカル秘密キーを生成しました: {path}",
        "log.csr_written": "ローカル CSR を生成しました: {path}",
        "log.created": "申請を作成しました: {id}",
        "log.issued": "発行に成功しました。証明書をダウンロードしています",
        "log.no_key": "注意: ローカル秘密キーが指定されていないため、一致確認をスキップします",
        "done.title": "完了。",
        "done.cert": "証明書",
        "done.chain": "フルチェーン",
        "done.key": "秘密キー",
        "done.csr": "CSR",
        "done.meta": "メタデータ",
    },
    "ru-RU": {
        "cli.description": "Отправить клиентский CSR в LACTSSL и скачать сертификат публичного или приватного CA (закрытый ключ не покидает клиент)",
        "cli.usage": "использование",
        "cli.options": "параметры",
        "cli.help": "показать эту справку и выйти",
        "cli.epilog": "Примеры:\n  python3 scripts/csr_client.py --domains example.com --dns-account-id 3\n  python3 scripts/csr_client.py --domains app.internal --private-ca-id 2\n  python3 scripts/csr_client.py --request-id csr_xxxxxxxx",
        "cli.base_url": "Базовый URL LACTSSL; по умолчанию LACTSSL_URL или http://127.0.0.1:8080",
        "cli.token": "API-ключ; по умолчанию LACTSSL_TOKEN",
        "cli.domains": "Запрашиваемые домены через запятую или пробел; можно указывать несколько раз",
        "cli.csr": "Использовать существующий CSR вместо создания нового закрытого ключа",
        "cli.key": "Существующий закрытый ключ для проверки соответствия скачанному сертификату",
        "cli.request_id": "Продолжить существующий CSR-запрос, не создавая новый",
        "cli.ca": "Идентификатор публичного CA или private-ca:<id> для приватного CA",
        "cli.private_ca_id": "ID приватного CA для выпуска; эквивалентно --ca private-ca:<id>",
        "cli.dns_account_id": "ID DNS-аккаунта для DNS-01; если не указан, определяется по привязкам зон на сервере",
        "cli.challenge": "Метод проверки; для публичного CA сейчас поддерживается только dns01",
        "cli.key_type": "Тип локального закрытого ключа (по умолчанию ecdsa)",
        "cli.out_dir": "Каталог вывода",
        "cli.name": "Базовое имя выходных файлов; по умолчанию первый домен",
        "cli.idempotency_key": "Ключ идемпотентности; по умолчанию создаётся автоматически",
        "cli.wait_seconds": "Сколько секунд сервер ждёт синхронно при создании (0-30)",
        "cli.timeout": "Общий тайм-аут выпуска в секундах",
        "cli.poll_interval": "Интервал опроса в секундах",
        "cli.http_timeout": "Тайм-аут одного HTTP-запроса в секундах",
        "cli.ca_bundle": "Пользовательский CA-файл для проверки HTTPS LACTSSL",
        "cli.insecure": "Пропустить проверку HTTPS-сертификата (не рекомендуется)",
        "cli.force": "Перезаписать существующие выходные файлы",
        "cli.quiet": "Сократить вывод о ходе работы",
        "cli.interactive": "Запустить интерактивный мастер",
        "cli.lang": "Язык интерфейса: zh-CN, zh-TW, en-US, ja-JP, ru-RU",
        "app.title": "Клиент LACTSSL CSR · интерактивный режим",
        "wizard.step": "Шаг {current}/{total}",
        "wizard.current": "Текущие настройки",
        "app.privacy": "Закрытый ключ создаётся и хранится только локально и не загружается. Нажмите Ctrl+C для выхода.",
        "app.error": "Ошибка: {error}",
        "app.cancelled": "Отменено",
        "app.cancelled_period": "Отменено.",
        "app.tty_required": "Интерактивный режим требует терминал (TTY); используйте аргументы командной строки",
        "section.connection": "== Подключение ==",
        "section.language": "== Язык ==",
        "section.logic": "== Логика выполнения ==",
        "logic.prompt": "Выберите логику выполнения:",
        "logic.sequential": "Последовательное выполнение",
        "language.prompt": "Выберите язык интерфейса:",
        "section.mode": "== Операция ==",
        "section.request": "== Запрос ==",
        "section.resume": "== Продолжение запроса ==",
        "section.csr": "== Существующий CSR ==",
        "section.output": "== Вывод ==",
        "section.summary": "== Готово к запуску ==",
        "conn.url": "URL LACTSSL",
        "conn.reuse_token": "API-ключ уже указан. Использовать его?",
        "conn.token": "API-ключ",
        "conn.token_hint": "ввод не отображается",
        "conn.insecure": "Пропустить проверку HTTPS-сертификата (не рекомендуется)?",
        "conn.ca_bundle": "Пользовательский CA-файл (необязательно, для собственного HTTPS)",
        "warn.insecure": "Внимание: проверка HTTPS-сертификата отключена; используйте только для локальных тестов",
        "mode.prompt": "Выберите операцию:",
        "mode.new": "Запрос сертификата через публичный CA (создать ключ и CSR локально)",
        "mode.private": "Запрос сертификата через приватный CA (создать ключ и CSR локально)",
        "mode.resume": "Продолжить существующий запрос (только скачать сертификат)",
        "mode.existing_csr": "Использовать существующий CSR и закрытый ключ",
        "request.domains": "Домены (через запятую или пробел; первый — основной; шаблон *.example.com)",
        "request.private_domains": "Домены (внутренние имена разрешены; через запятую или пробел; первый — основной)",
        "request.dns_account": "ID DNS-аккаунта (необязательно; пусто — определить по привязкам зон)",
        "request.public_ca": "Идентификатор публичного CA",
        "request.private_ca": "ID приватного CA",
        "request.private_ca_optional": "ID приватного CA (необязательно; укажите для выпуска приватным CA)",
        "request.key_type": "Тип закрытого ключа:",
        "key.ecdsa": "ECDSA P-256 (рекомендуется)",
        "key.rsa2048": "RSA 2048",
        "key.rsa4096": "RSA 4096",
        "resume.id": "ID запроса",
        "resume.key": "Путь к локальному ключу (необязательно; проверка соответствия сертификату)",
        "csr.path": "Путь к файлу CSR",
        "csr.key": "Путь к закрытому ключу (необязательно; проверка соответствия сертификату)",
        "csr.domains": "Домены (необязательно; пусто — использовать SAN из CSR; при указании должны совпадать)",
        "output.dir": "Каталог вывода",
        "output.name": "Базовое имя файлов (пусто — первый домен)",
        "output.force": "Перезаписать существующие файлы?",
        "summary.url": "Адрес сервера",
        "summary.token": "API-ключ",
        "summary.set": "задан",
        "summary.unset": "не задан",
        "summary.action": "Операция",
        "summary.logic": "Логика выполнения",
        "summary.action.resume": "Продолжить запрос {id}",
        "summary.action.csr": "Использовать существующий CSR",
        "summary.action.private": "Запрос сертификата через приватный CA",
        "summary.action.new": "Запрос сертификата через публичный CA",
        "summary.domains": "Домены",
        "summary.key": "Закрытый ключ",
        "summary.csr": "CSR",
        "summary.dns": "DNS-аккаунт",
        "summary.ca": "CA",
        "summary.private_ca": "Приватный CA",
        "summary.key_type": "Тип ключа",
        "summary.out_dir": "Каталог вывода",
        "summary.out_name": "Базовое имя",
        "summary.auto": "автоопределение",
        "summary.first_domain": "(первый домен)",
        "summary.confirm": "Начать?",
        "summary.retry": "Вернуться в мастер и повторить?",
        "prompt.required": "  Это поле обязательно. Повторите ввод.",
        "prompt.int": "  Введите целое число.",
        "prompt.min": "  Введите целое число не меньше {min}.",
        "prompt.yesno": "  Введите y или n.",
        "prompt.choice": "  Введите номер из списка.",
        "prompt.file_missing": "  Файл не найден: {path}",
        "prompt.eof": "Ввод завершён; интерактивный режим невозможен",
        "prompt.choose": "Выбор [1-{count}]: ",
        "prompt.default": "(по умолчанию)",
        "error.openssl_missing": "Команда openssl не найдена; установите OpenSSL 1.1.1 или новее",
        "error.openssl_failed": "Ошибка OpenSSL: {args}\n{detail}",
        "error.unsupported_key_type": "Неподдерживаемый тип ключа: {key_type}",
        "error.openssl_output_missing": "OpenSSL не создал закрытый ключ или CSR",
        "error.csr_no_san": "В CSR нет DNS SAN; невозможно определить домены",
        "error.ca_bundle": "Не удалось загрузить CA-сертификат: {error}",
        "error.request_failed": "запрос не выполнен",
        "error.retry_after": " (повторите через {seconds} секунд)",
        "error.connect": "Не удалось подключиться к LACTSSL: {reason}",
        "error.bad_create_response": "Сервер вернул нераспознанный ответ на запрос",
        "error.bad_status_response": "Сервер вернул нераспознанное состояние запроса",
        "error.request_status": "Статус запроса {id}: {status}",
        "error.not_issued": "Запрос не выполнен: {status} {detail}{retry_hint}",
        "error.retry_hint": "; повторите через /retry",
        "error.timeout": "Тайм-аут через {timeout} секунд; продолжите позже с --request-id {id}",
        "error.download_not_pem": "Не удалось скачать сертификат {kind}: ответ не является PEM-сертификатом",
        "error.domain_mismatch": "{label} не совпадает с запрошенными доменами (нет: {missing}; лишние: {extra})",
        "error.cert_missing_domains": "Сертификат не покрывает запрошенные домены: {domains}",
        "error.key_mismatch": "Скачанный сертификат не соответствует локальному ключу; сохранение отменено",
        "error.no_token": "Нет API-ключа; укажите --token или задайте LACTSSL_TOKEN",
        "error.key_needs_source": "--key используется вместе с --csr или --request-id",
        "error.private_ca_id_invalid": "ID приватного CA должен быть положительным целым числом",
        "error.ca_conflict": "--private-ca-id конфликтует с --ca",
        "error.files_exist": "Эти файлы уже существуют; используйте --force или измените --name/--out-dir:\n  {files}",
        "error.key_not_found": "Файл закрытого ключа не найден: {path}",
        "error.csr_not_found": "Файл CSR не найден: {path}",
        "error.request_no_domains": "В запросе нет информации о доменах; укажите --domains",
        "error.domains_required": "Укажите домены через --domains",
        "error.local_key_missing": "Не удалось создать локальный ключ или CSR",
        "error.missing_csr": "Отсутствует CSR",
        "log.key_written": "Создан локальный закрытый ключ: {path}",
        "log.csr_written": "Создан локальный CSR: {path}",
        "log.created": "Создан запрос: {id}",
        "log.issued": "Выпуск успешен; скачивание сертификата",
        "log.no_key": "Примечание: локальный ключ не указан; проверка соответствия пропущена",
        "done.title": "Готово.",
        "done.cert": "Сертификат",
        "done.chain": "Полная цепочка",
        "done.key": "Закрытый ключ",
        "done.csr": "CSR",
        "done.meta": "Метаданные",
    },
}

# ---------------------------------------------------------------------------
# 扩展文案（P1/P2 新增能力）；运行期与 MESSAGES 合并，保证 5 种语言键集合一致。
# ---------------------------------------------------------------------------

EXTRA_MESSAGES: dict[str, dict[str, str]] = {
    "en-US": {
        "cli.version": "show version and exit",
        "cli.token_file": "Read the API key from a file; defaults to LACTSSL_TOKEN_FILE",
        "cli.retries": "Retries for transient network errors and 5xx responses (default: 3)",
        "cli.auto_retry": "Automatic server-side /retry attempts for retryable failures (default: 1)",
        "cli.json": "Print the final result as JSON on stdout; progress goes to stderr",
        "cli.renew_from": "Renew from a previous <name>.request.json (reuse its key and settings)",
        "cli.key": "Existing private key file; verifies the downloaded certificate, or with --domains generates a new CSR",
        "cli.reuse_key": "Generate a new CSR from an existing --key instead of a new private key",
        "cli.list": "List requests visible to the API key and exit",
        "list.title": "Requests",
        "list.empty": "No requests found",
        "cli.status": "Print the status of an existing request and exit",
        "cli.cancel": "Cancel an existing request and exit",
        "cli.yes": "Do not ask for confirmation",
        "cli.manifest": "Batch mode: JSON manifest containing a certificates array",
        "cli.on_error": "Batch behavior on failure: stop or continue",
        "summary.status": "Status",
        "summary.challenge": "Challenge",
        "summary.attempts": "Attempts",
        "summary.retryable": "Retryable",
        "summary.expires": "Expires",
        "summary.error": "Error",
        "summary.yes": "yes",
        "summary.no": "no",
        "summary.dns_routes": "DNS routes",
        "prompt.default_clear": "[{default}; - to clear]",
        "discovery.title": "Options reported by the server:",
        "discovery.unavailable": "Could not list options ({error}); enter the value manually",
        "discovery.public_ca": "Public CA options",
        "discovery.private_ca": "Private CA options",
        "discovery.dns": "DNS account options",
        "wizard.failure": "What do you want to do?",
        "wizard.retry_download": "Retry the certificate download with the same request",
        "wizard.edit": "Return to the wizard and change settings",
        "wizard.cancel_request": "Cancel the request on the server",
        "wizard.exit": "Exit",
        "wizard.no_request": "No request was created; returning to the wizard",
        "status.title": "Request status",
        "cancel.confirm": "Cancel request {id}?",
        "cancel.done": "Request {id} cancelled.",
        "batch.title": "Batch results",
        "batch.item_ok": "{index}. {name}: issued",
        "batch.item_failed": "{index}. {name}: {error}",
        "batch.summary": "{succeeded} succeeded, {failed} failed",
        "done.request_id": "Request ID",
        "done.status": "Status",
        "done.domains": "Domains",
        "done.issuer": "Issuer",
        "done.serial": "Serial",
        "done.not_after": "Valid until",
        "done.existing": "existing",
        "log.retry": "Retry {attempt}/{total} in {delay:.1f}s: {reason}",
        "log.state_saved": "Request state saved: {path}",
        "log.resume": "Resume with: {command}",
        "log.auto_key": "Using the private key from a previous attempt: {path}",
        "log.retry_request": "Request {id} is retryable; retrying on the server ({attempt}/{total})",
        "warn.staged_kept": "Staged files from the failed attempt were kept: {files}",
        "warn.state_failed": "Could not update the request state file: {error}",
        "error.connect_retry": "Cannot reach LACTSSL after {attempts} attempt(s): {reason}",
        "error.redirect": "The server returned HTTP {status} redirect to {url}; set --base-url to the final address",
        "error.invalid_lang": "Unsupported language: {lang}; choose from {languages}",
        "error.value": "{option}: {message}",
        "error.wait_seconds": "--wait-seconds must be between 0 and 30",
        "error.timeout_value": "--timeout must be at least 1 second",
        "error.poll_interval": "--poll-interval must be at least 0.5 seconds",
        "error.http_timeout": "--http-timeout must be greater than 0 and greater than --wait-seconds",
        "error.dns_account_id": "--dns-account-id must be a positive integer",
        "error.retries": "--retries must be between 0 and 10",
        "error.auto_retry": "--auto-retry must be between 0 and 5",
        "error.fs": "File operation failed at {path}: {error}",
        "error.csr_invalid": "Invalid CSR: {error}",
        "error.csr_key_weak": "CSR public key is too weak (RSA must be at least 2048 bits)",
        "error.csr_key_unsupported": "CSR public key algorithm or curve is not supported",

        "error.csr_too_large": "The CSR is larger than 16 KiB",
        "error.csr_too_many": "The CSR contains more than {max} DNS names",

        "error.csr_key_mismatch": "The CSR does not match the provided private key",
        "error.csr_extra_san": "The CSR contains unsupported identifiers (IP/URI/email): {items}",
        "error.csr_noncanonical": "CSR identifier {domain} is not canonical (lowercase, no trailing dot)",
        "error.csr_cn_invalid": "Invalid CSR common name: {domain}",
        "error.csr_cn_not_in_san": "CSR common name {domain} is not present in the SAN list",
        "error.csr_dup": "Duplicate CSR identifier: {domain}",
        "error.invalid_domain": "Invalid domain: {domain}",
        "error.unknown_status": "Request {id} returned an unknown status: {status}",
        "error.expired": "Request {id} expired at {expires}; create a new request",
        "error.manifest": "Cannot read the manifest: {error}",
        "error.manifest_entry": "Manifest entry {index}: {error}",
        "error.manifest_duplicate": "Duplicate output target in the manifest: {target}",
        "error.renew_state": "Cannot use the renewal state {path}: {error}",
        "error.key_required": "A private key is required; pass --key or keep <name>.key next to the state file",
        "error.token_file": "Cannot read the token file {path}: {error}",
        "error.token_file_empty": "The token file is empty: {path}",
        "error.base_url": "Invalid --base-url (must start with http:// or https://): {url}",
        "error.cancel_confirm": "Cancellation was not confirmed; use --yes in non-interactive mode",
        "error.operation_conflict": "--status, --cancel, and --manifest cannot be combined",
        "error.staged_exists": "Staged files from a previous attempt exist; resume with --request-id or use --force:\n  {files}",
        "error.cert_expired": "The downloaded certificate is expired or not yet valid",
        "error.cert_csr_mismatch": "The downloaded certificate does not match the submitted CSR",
        "error.key_needs_source": "--key must be used with --csr, --request-id, or --domains",
        "error.idempotency_key": "--idempotency-key must be at most 128 characters",
        "error.reuse_key": "--reuse-key requires --key",

    },
    "zh-CN": {
        "cli.version": "显示版本并退出",
        "cli.token_file": "从文件读取 API 密钥，默认读取 LACTSSL_TOKEN_FILE",
        "cli.retries": "网络异常与 5xx 响应的重试次数，默认 3",
        "cli.auto_retry": "服务端标记可重试时的自动 /retry 次数，默认 1",
        "cli.json": "在 stdout 输出 JSON 结果，进度信息写入 stderr",
        "cli.renew_from": "从已有的 <name>.request.json 续期，复用私钥与设置",
        "cli.key": "已有私钥文件，用于校验证书；与 --domains 一起使用时用其生成新 CSR",
        "cli.reuse_key": "使用已有 --key 生成新 CSR，不重新生成私钥",
        "cli.list": "列出当前 API 密钥可见的申请并退出",
        "list.title": "申请列表",
        "list.empty": "没有找到申请",
        "cli.status": "查询指定申请的状态并退出",
        "cli.cancel": "取消指定申请并退出",
        "cli.yes": "不询问确认",
        "cli.manifest": "批量模式：包含 certificates 数组的 JSON 清单",
        "cli.on_error": "批量执行遇到失败时：stop 停止，continue 继续",
        "summary.status": "状态",
        "summary.challenge": "验证方式",
        "summary.attempts": "尝试次数",
        "summary.retryable": "可重试",
        "summary.expires": "过期时间",
        "summary.error": "错误",
        "summary.yes": "是",
        "summary.no": "否",
        "summary.dns_routes": "DNS 路由",
        "prompt.default_clear": "[{default}；输入 - 清空]",
        "discovery.title": "服务端返回的可选项：",
        "discovery.unavailable": "无法获取可选项（{error}），请手动填写",
        "discovery.public_ca": "公共 CA 选项",
        "discovery.private_ca": "私有 CA 选项",
        "discovery.dns": "DNS 账户选项",
        "wizard.failure": "接下来要做什么？",
        "wizard.retry_download": "使用同一申请重试下载证书",
        "wizard.edit": "返回向导修改配置",
        "wizard.cancel_request": "在服务端取消该申请",
        "wizard.exit": "退出",
        "wizard.no_request": "尚未创建申请，返回向导",
        "status.title": "申请状态",
        "cancel.confirm": "确认取消申请 {id}？",
        "cancel.done": "申请 {id} 已取消。",
        "batch.title": "批量结果",
        "batch.item_ok": "{index}. {name}：已签发",
        "batch.item_failed": "{index}. {name}：{error}",
        "batch.summary": "成功 {succeeded} 个，失败 {failed} 个",
        "done.request_id": "申请 ID",
        "done.status": "状态",
        "done.domains": "域名",
        "done.issuer": "签发者",
        "done.serial": "序列号",
        "done.not_after": "有效期至",
        "done.existing": "已有",
        "log.retry": "第 {attempt}/{total} 次重试，{delay:.1f} 秒后：{reason}",
        "log.state_saved": "申请状态已保存: {path}",
        "log.resume": "可使用以下命令续传: {command}",
        "log.auto_key": "使用上次申请留下的私钥: {path}",
        "log.retry_request": "申请 {id} 可重试，正在服务端重试（{attempt}/{total}）",
        "warn.staged_kept": "已保留失败尝试的暂存文件: {files}",
        "warn.state_failed": "无法更新申请状态文件: {error}",
        "error.connect_retry": "尝试 {attempts} 次后仍无法连接 LACTSSL: {reason}",
        "error.redirect": "服务端返回 HTTP {status} 重定向到 {url}，请把 --base-url 改为最终地址",
        "error.invalid_lang": "不支持的语言: {lang}；可选：{languages}",
        "error.value": "{option}: {message}",
        "error.wait_seconds": "--wait-seconds 必须在 0 到 30 之间",
        "error.timeout_value": "--timeout 必须至少为 1 秒",
        "error.poll_interval": "--poll-interval 必须至少为 0.5 秒",
        "error.http_timeout": "--http-timeout 必须大于 0 且大于 --wait-seconds",
        "error.dns_account_id": "--dns-account-id 必须是正整数",
        "error.retries": "--retries 必须在 0 到 10 之间",
        "error.auto_retry": "--auto-retry 必须在 0 到 5 之间",
        "error.fs": "文件操作失败 {path}: {error}",
        "error.csr_invalid": "CSR 无效: {error}",
        "error.csr_key_weak": "CSR 公钥强度不足（RSA 至少 2048 位）",
        "error.csr_key_unsupported": "CSR 公钥算法或曲线不受支持",

        "error.csr_too_large": "CSR 超过 16 KiB",
        "error.csr_too_many": "CSR 中的 DNS 域名超过 {max} 个",

        "error.csr_key_mismatch": "CSR 与提供的私钥不匹配",
        "error.csr_extra_san": "CSR 包含不支持的标识（IP/URI/email）: {items}",
        "error.csr_noncanonical": "CSR 域名 {domain} 不是规范形式（小写、无尾点）",
        "error.csr_cn_invalid": "CSR 通用名称无效: {domain}",
        "error.csr_cn_not_in_san": "CSR 通用名称 {domain} 不在 SAN 列表中",
        "error.csr_dup": "CSR 中存在重复域名: {domain}",
        "error.invalid_domain": "域名无效: {domain}",
        "error.unknown_status": "申请 {id} 返回未知状态: {status}",
        "error.expired": "申请 {id} 已于 {expires} 过期，请重新申请",
        "error.manifest": "无法读取批量清单: {error}",
        "error.manifest_entry": "批量清单第 {index} 项: {error}",
        "error.manifest_duplicate": "批量清单中存在重复的输出目标: {target}",
        "error.renew_state": "无法使用续期状态文件 {path}: {error}",
        "error.key_required": "续期需要私钥；请传入 --key，或把 <name>.key 放在状态文件旁",
        "error.token_file": "无法读取密钥文件 {path}: {error}",
        "error.token_file_empty": "密钥文件为空: {path}",
        "error.base_url": "--base-url 无效（必须以 http:// 或 https:// 开头）: {url}",
        "error.cancel_confirm": "未确认取消；非交互模式请使用 --yes",
        "error.operation_conflict": "--status、--cancel 与 --manifest 不能同时使用",
        "error.staged_exists": "检测到上次尝试的暂存文件，请用 --request-id 续传或使用 --force:\n  {files}",
        "error.cert_expired": "下载的证书已过期或尚未生效",
        "error.cert_csr_mismatch": "下载的证书与提交的 CSR 不匹配",
        "error.key_needs_source": "--key 需要与 --csr、--request-id 或 --domains 一起使用",
        "error.idempotency_key": "--idempotency-key 最多 128 个字符",
        "error.reuse_key": "--reuse-key 需要同时提供 --key",

    },
    "zh-TW": {
        "cli.version": "顯示版本並結束",
        "cli.token_file": "從檔案讀取 API 金鑰，預設讀取 LACTSSL_TOKEN_FILE",
        "cli.retries": "網路異常與 5xx 回應的重試次數，預設 3",
        "cli.auto_retry": "服務端標記可重試時的自動 /retry 次數，預設 1",
        "cli.json": "在 stdout 輸出 JSON 結果，進度訊息寫入 stderr",
        "cli.renew_from": "從既有的 <name>.request.json 續期，重用私鑰與設定",
        "cli.key": "既有私鑰檔案，用於校驗憑證；與 --domains 一起使用時用其產生新 CSR",
        "cli.reuse_key": "使用既有 --key 產生新 CSR，不重新產生私鑰",
        "cli.list": "列出目前 API 金鑰可見的申請並結束",
        "list.title": "申請清單",
        "list.empty": "找不到申請",
        "cli.status": "查詢指定申請的狀態並結束",
        "cli.cancel": "取消指定申請並結束",
        "cli.yes": "不詢問確認",
        "cli.manifest": "批次模式：包含 certificates 陣列的 JSON 清單",
        "cli.on_error": "批次執行遇到失敗時：stop 停止，continue 繼續",
        "summary.status": "狀態",
        "summary.challenge": "驗證方式",
        "summary.attempts": "嘗試次數",
        "summary.retryable": "可重試",
        "summary.expires": "過期時間",
        "summary.error": "錯誤",
        "summary.yes": "是",
        "summary.no": "否",
        "summary.dns_routes": "DNS 路由",
        "prompt.default_clear": "[{default}；輸入 - 清空]",
        "discovery.title": "服務端傳回的可選項：",
        "discovery.unavailable": "無法取得可選項（{error}），請手動填寫",
        "discovery.public_ca": "公共 CA 選項",
        "discovery.private_ca": "私有 CA 選項",
        "discovery.dns": "DNS 帳戶選項",
        "wizard.failure": "接下來要做什麼？",
        "wizard.retry_download": "使用同一申請重試下載憑證",
        "wizard.edit": "返回精靈修改設定",
        "wizard.cancel_request": "在服務端取消該申請",
        "wizard.exit": "結束",
        "wizard.no_request": "尚未建立申請，返回精靈",
        "status.title": "申請狀態",
        "cancel.confirm": "確認取消申請 {id}？",
        "cancel.done": "申請 {id} 已取消。",
        "batch.title": "批次結果",
        "batch.item_ok": "{index}. {name}：已簽發",
        "batch.item_failed": "{index}. {name}：{error}",
        "batch.summary": "成功 {succeeded} 個，失敗 {failed} 個",
        "done.request_id": "申請 ID",
        "done.status": "狀態",
        "done.domains": "網域",
        "done.issuer": "簽發者",
        "done.serial": "序號",
        "done.not_after": "有效期限",
        "done.existing": "既有",
        "log.retry": "第 {attempt}/{total} 次重試，{delay:.1f} 秒後：{reason}",
        "log.state_saved": "申請狀態已儲存: {path}",
        "log.resume": "可使用以下指令續傳: {command}",
        "log.auto_key": "使用上次申請留下的私鑰: {path}",
        "log.retry_request": "申請 {id} 可重試，正在服務端重試（{attempt}/{total}）",
        "warn.staged_kept": "已保留失敗嘗試的暫存檔案: {files}",
        "warn.state_failed": "無法更新申請狀態檔案: {error}",
        "error.connect_retry": "嘗試 {attempts} 次後仍無法連線 LACTSSL: {reason}",
        "error.redirect": "服務端傳回 HTTP {status} 重新導向至 {url}，請把 --base-url 改為最終位址",
        "error.invalid_lang": "不支援的語言: {lang}；可選：{languages}",
        "error.value": "{option}: {message}",
        "error.wait_seconds": "--wait-seconds 必須介於 0 到 30",
        "error.timeout_value": "--timeout 必須至少為 1 秒",
        "error.poll_interval": "--poll-interval 必須至少為 0.5 秒",
        "error.http_timeout": "--http-timeout 必須大於 0 且大於 --wait-seconds",
        "error.dns_account_id": "--dns-account-id 必須是正整數",
        "error.retries": "--retries 必須介於 0 到 10",
        "error.auto_retry": "--auto-retry 必須介於 0 到 5",
        "error.fs": "檔案操作失敗 {path}: {error}",
        "error.csr_invalid": "CSR 無效: {error}",
        "error.csr_key_weak": "CSR 公鑰強度不足（RSA 至少 2048 位元）",
        "error.csr_key_unsupported": "CSR 公鑰演算法或曲線不受支援",

        "error.csr_too_large": "CSR 超過 16 KiB",
        "error.csr_too_many": "CSR 中的 DNS 網域超過 {max} 個",

        "error.csr_key_mismatch": "CSR 與提供的私鑰不匹配",
        "error.csr_extra_san": "CSR 包含不支援的識別（IP/URI/email）: {items}",
        "error.csr_noncanonical": "CSR 網域 {domain} 不是規範形式（小寫、無尾點）",
        "error.csr_cn_invalid": "CSR 通用名稱無效: {domain}",
        "error.csr_cn_not_in_san": "CSR 通用名稱 {domain} 不在 SAN 清單中",
        "error.csr_dup": "CSR 中存在重複網域: {domain}",
        "error.invalid_domain": "網域無效: {domain}",
        "error.unknown_status": "申請 {id} 傳回未知狀態: {status}",
        "error.expired": "申請 {id} 已於 {expires} 過期，請重新申請",
        "error.manifest": "無法讀取批次清單: {error}",
        "error.manifest_entry": "批次清單第 {index} 項: {error}",
        "error.manifest_duplicate": "批次清單中存在重複的輸出目標: {target}",
        "error.renew_state": "無法使用續期狀態檔案 {path}: {error}",
        "error.key_required": "續期需要私鑰；請傳入 --key，或把 <name>.key 放在狀態檔案旁",
        "error.token_file": "無法讀取金鑰檔案 {path}: {error}",
        "error.token_file_empty": "金鑰檔案為空: {path}",
        "error.base_url": "--base-url 無效（必須以 http:// 或 https:// 開頭）: {url}",
        "error.cancel_confirm": "未確認取消；非互動模式請使用 --yes",
        "error.operation_conflict": "--status、--cancel 與 --manifest 不能同時使用",
        "error.staged_exists": "偵測到上次嘗試的暫存檔案，請用 --request-id 續傳或使用 --force:\n  {files}",
        "error.cert_expired": "下載的憑證已過期或尚未生效",
        "error.cert_csr_mismatch": "下載的憑證與提交的 CSR 不匹配",
        "error.key_needs_source": "--key 需要與 --csr、--request-id 或 --domains 一起使用",
        "error.idempotency_key": "--idempotency-key 最多 128 個字元",
        "error.reuse_key": "--reuse-key 需要同時提供 --key",

    },
    "ja-JP": {
        "cli.version": "バージョンを表示して終了",
        "cli.token_file": "API キーをファイルから読み取る（既定: LACTSSL_TOKEN_FILE）",
        "cli.retries": "一時的なネットワークエラーと 5xx の再試行回数（既定: 3）",
        "cli.auto_retry": "再試行可能な失敗に対するサーバー側 /retry の自動実行回数（既定: 1）",
        "cli.json": "最終結果を stdout に JSON で出力し、進捗は stderr に出す",
        "cli.renew_from": "既存の <name>.request.json から更新し、鍵と設定を再利用する",
        "cli.key": "既存の秘密キーファイル。証明書の検証に使用し、--domains と併用すると新しい CSR を生成します",
        "cli.reuse_key": "新しい鍵を作らず、既存の --key から新しい CSR を生成する",
        "cli.list": "API キーから参照できる申請を一覧表示して終了",
        "list.title": "申請一覧",
        "list.empty": "申請がありません",
        "cli.status": "指定した申請の状態を表示して終了",
        "cli.cancel": "指定した申請をキャンセルして終了",
        "cli.yes": "確認を求めない",
        "cli.manifest": "バッチモード: certificates 配列を含む JSON マニフェスト",
        "cli.on_error": "バッチで失敗した場合の動作: stop または continue",
        "summary.status": "状態",
        "summary.challenge": "検証方式",
        "summary.attempts": "試行回数",
        "summary.retryable": "再試行可能",
        "summary.expires": "有効期限",
        "summary.error": "エラー",
        "summary.yes": "はい",
        "summary.no": "いいえ",
        "summary.dns_routes": "DNS ルート",
        "prompt.default_clear": "[{default}。 - でクリア]",
        "discovery.title": "サーバーが返した選択肢:",
        "discovery.unavailable": "選択肢を取得できません（{error}）。手動で入力してください",
        "discovery.public_ca": "パブリック CA の選択肢",
        "discovery.private_ca": "プライベート CA の選択肢",
        "discovery.dns": "DNS アカウントの選択肢",
        "wizard.failure": "次にどうしますか？",
        "wizard.retry_download": "同じ申請で証明書のダウンロードを再試行",
        "wizard.edit": "ウィザードに戻って設定を変更",
        "wizard.cancel_request": "サーバー側で申請をキャンセル",
        "wizard.exit": "終了",
        "wizard.no_request": "申請が作成されていません。ウィザードに戻ります",
        "status.title": "申請の状態",
        "cancel.confirm": "申請 {id} をキャンセルしますか？",
        "cancel.done": "申請 {id} をキャンセルしました。",
        "batch.title": "バッチ結果",
        "batch.item_ok": "{index}. {name}: 発行済み",
        "batch.item_failed": "{index}. {name}: {error}",
        "batch.summary": "成功 {succeeded} 件、失敗 {failed} 件",
        "done.request_id": "申請 ID",
        "done.status": "状態",
        "done.domains": "ドメイン",
        "done.issuer": "発行者",
        "done.serial": "シリアル",
        "done.not_after": "有効期限",
        "done.existing": "既存",
        "log.retry": "{delay:.1f} 秒後に再試行 {attempt}/{total}: {reason}",
        "log.state_saved": "申請状態を保存しました: {path}",
        "log.resume": "再開するには: {command}",
        "log.auto_key": "前回の試行で使った秘密キーを使用します: {path}",
        "log.retry_request": "申請 {id} は再試行可能です。サーバー側で再試行します（{attempt}/{total}）",
        "warn.staged_kept": "失敗した試行の一時ファイルを保持しました: {files}",
        "warn.state_failed": "申請状態ファイルを更新できません: {error}",
        "error.connect_retry": "{attempts} 回試行しましたが LACTSSL に接続できません: {reason}",
        "error.redirect": "サーバーが HTTP {status} で {url} にリダイレクトしました。--base-url を最終的なアドレスに変更してください",
        "error.invalid_lang": "未対応の言語: {lang}。次から選択してください: {languages}",
        "error.value": "{option}: {message}",
        "error.wait_seconds": "--wait-seconds は 0〜30 の範囲で指定してください",
        "error.timeout_value": "--timeout は 1 秒以上にしてください",
        "error.poll_interval": "--poll-interval は 0.5 秒以上にしてください",
        "error.http_timeout": "--http-timeout は 0 より大きく、--wait-seconds より大きくしてください",
        "error.dns_account_id": "--dns-account-id は正の整数にしてください",
        "error.retries": "--retries は 0〜10 の範囲で指定してください",
        "error.auto_retry": "--auto-retry は 0〜5 の範囲で指定してください",
        "error.fs": "ファイル操作に失敗しました {path}: {error}",
        "error.csr_invalid": "CSR が無効です: {error}",
        "error.csr_key_weak": "CSR の公開キーが弱すぎます（RSA は 2048 ビット以上）",
        "error.csr_key_unsupported": "CSR の公開キーアルゴリズムまたは曲線は未対応です",

        "error.csr_too_large": "CSR が 16 KiB を超えています",
        "error.csr_too_many": "CSR の DNS 名が {max} 個を超えています",

        "error.csr_key_mismatch": "CSR が指定された秘密キーと一致しません",
        "error.csr_extra_san": "CSR に未対応の識別子（IP/URI/email）が含まれています: {items}",
        "error.csr_noncanonical": "CSR の識別子 {domain} が正規形ではありません（小文字、末尾ドットなし）",
        "error.csr_cn_invalid": "CSR のコモンネームが無効です: {domain}",
        "error.csr_cn_not_in_san": "CSR のコモンネーム {domain} が SAN に含まれていません",
        "error.csr_dup": "CSR に重複した識別子があります: {domain}",
        "error.invalid_domain": "無効なドメイン: {domain}",
        "error.unknown_status": "申請 {id} が不明な状態を返しました: {status}",
        "error.expired": "申請 {id} は {expires} に期限切れです。新しく申請してください",
        "error.manifest": "マニフェストを読み取れません: {error}",
        "error.manifest_entry": "マニフェスト {index} 番目: {error}",
        "error.manifest_duplicate": "マニフェスト内で出力先が重複しています: {target}",
        "error.renew_state": "更新用状態ファイル {path} を使用できません: {error}",
        "error.key_required": "更新には秘密キーが必要です。--key を指定するか、状態ファイルの隣に <name>.key を置いてください",
        "error.token_file": "キーファイル {path} を読み取れません: {error}",
        "error.token_file_empty": "キーファイルが空です: {path}",
        "error.base_url": "--base-url が無効です（http:// または https:// で始まる必要があります）: {url}",
        "error.cancel_confirm": "キャンセルが確認されていません。非対話モードでは --yes を使用してください",
        "error.operation_conflict": "--status、--cancel、--manifest は同時に使用できません",
        "error.staged_exists": "前回の一時ファイルが残っています。--request-id で再開するか --force を使用してください:\n  {files}",
        "error.cert_expired": "ダウンロードした証明書が期限切れ、またはまだ有効ではありません",
        "error.cert_csr_mismatch": "ダウンロードした証明書が送信した CSR と一致しません",
        "error.key_needs_source": "--key は --csr、--request-id、または --domains と一緒に指定してください",
        "error.idempotency_key": "--idempotency-key は 128 文字以内にしてください",
        "error.reuse_key": "--reuse-key には --key が必要です",

    },
    "ru-RU": {
        "cli.version": "показать версию и выйти",
        "cli.token_file": "Прочитать API-ключ из файла; по умолчанию LACTSSL_TOKEN_FILE",
        "cli.retries": "Число повторов при временных сетевых ошибках и ответах 5xx (по умолчанию: 3)",
        "cli.auto_retry": "Число автоматических /retry на сервере для повторяемых ошибок (по умолчанию: 1)",
        "cli.json": "Вывести итоговый результат в JSON в stdout; прогресс — в stderr",
        "cli.renew_from": "Продлить по существующему <name>.request.json (переиспользовать ключ и настройки)",
        "cli.key": "Существующий файл ключа; проверяет сертификат, а с --domains создаёт новый CSR",
        "cli.reuse_key": "Создать новый CSR из существующего --key без генерации нового ключа",
        "cli.list": "Показать запросы, доступные API-ключу, и выйти",
        "list.title": "Запросы",
        "list.empty": "Запросы не найдены",
        "cli.status": "Показать состояние запроса и выйти",
        "cli.cancel": "Отменить запрос и выйти",
        "cli.yes": "Не запрашивать подтверждение",
        "cli.manifest": "Пакетный режим: JSON-манифест с массивом certificates",
        "cli.on_error": "Поведение пакета при ошибке: stop или continue",
        "summary.status": "Состояние",
        "summary.challenge": "Метод проверки",
        "summary.attempts": "Попытки",
        "summary.retryable": "Можно повторить",
        "summary.expires": "Истекает",
        "summary.error": "Ошибка",
        "summary.yes": "да",
        "summary.no": "нет",
        "summary.dns_routes": "DNS-маршруты",
        "prompt.default_clear": "[{default}; - чтобы очистить]",
        "discovery.title": "Доступные варианты с сервера:",
        "discovery.unavailable": "Не удалось получить варианты ({error}); введите значение вручную",
        "discovery.public_ca": "Варианты публичного CA",
        "discovery.private_ca": "Варианты приватного CA",
        "discovery.dns": "Варианты DNS-аккаунта",
        "wizard.failure": "Что сделать дальше?",
        "wizard.retry_download": "Повторить скачивание сертификата для этого запроса",
        "wizard.edit": "Вернуться в мастер и изменить настройки",
        "wizard.cancel_request": "Отменить запрос на сервере",
        "wizard.exit": "Выйти",
        "wizard.no_request": "Запрос не создан; возврат в мастер",
        "status.title": "Состояние запроса",
        "cancel.confirm": "Отменить запрос {id}?",
        "cancel.done": "Запрос {id} отменён.",
        "batch.title": "Результаты пакета",
        "batch.item_ok": "{index}. {name}: выпущен",
        "batch.item_failed": "{index}. {name}: {error}",
        "batch.summary": "Успешно: {succeeded}, с ошибкой: {failed}",
        "done.request_id": "ID запроса",
        "done.status": "Состояние",
        "done.domains": "Домены",
        "done.issuer": "Издатель",
        "done.serial": "Серийный номер",
        "done.not_after": "Действителен до",
        "done.existing": "существующий",
        "log.retry": "Повтор {attempt}/{total} через {delay:.1f} с: {reason}",
        "log.state_saved": "Состояние запроса сохранено: {path}",
        "log.resume": "Продолжить: {command}",
        "log.auto_key": "Используется ключ из предыдущей попытки: {path}",
        "log.retry_request": "Запрос {id} можно повторить; повтор на сервере ({attempt}/{total})",
        "warn.staged_kept": "Временные файлы неудачной попытки сохранены: {files}",
        "warn.state_failed": "Не удалось обновить файл состояния запроса: {error}",
        "error.connect_retry": "Не удалось подключиться к LACTSSL после {attempts} попыток: {reason}",
        "error.redirect": "Сервер вернул HTTP {status} с перенаправлением на {url}; укажите конечный адрес в --base-url",
        "error.invalid_lang": "Неподдерживаемый язык: {lang}; выберите из {languages}",
        "error.value": "{option}: {message}",
        "error.wait_seconds": "--wait-seconds должен быть от 0 до 30",
        "error.timeout_value": "--timeout должен быть не меньше 1 секунды",
        "error.poll_interval": "--poll-interval должен быть не меньше 0.5 секунды",
        "error.http_timeout": "--http-timeout должен быть больше 0 и больше --wait-seconds",
        "error.dns_account_id": "--dns-account-id должен быть положительным целым числом",
        "error.retries": "--retries должен быть от 0 до 10",
        "error.auto_retry": "--auto-retry должен быть от 0 до 5",
        "error.fs": "Ошибка файловой операции {path}: {error}",
        "error.csr_invalid": "Некорректный CSR: {error}",
        "error.csr_key_weak": "Открытый ключ CSR слишком слабый (RSA не менее 2048 бит)",
        "error.csr_key_unsupported": "Алгоритм или кривая открытого ключа CSR не поддерживаются",

        "error.csr_too_large": "CSR больше 16 KiB",
        "error.csr_too_many": "CSR содержит больше {max} DNS-имён",

        "error.csr_key_mismatch": "CSR не соответствует указанному закрытому ключу",
        "error.csr_extra_san": "CSR содержит неподдерживаемые идентификаторы (IP/URI/email): {items}",
        "error.csr_noncanonical": "Идентификатор CSR {domain} не канонический (нижний регистр, без точки в конце)",
        "error.csr_cn_invalid": "Некорректное общее имя CSR: {domain}",
        "error.csr_cn_not_in_san": "Общее имя CSR {domain} отсутствует в списке SAN",
        "error.csr_dup": "Повторяющийся идентификатор в CSR: {domain}",
        "error.invalid_domain": "Некорректный домен: {domain}",
        "error.unknown_status": "Запрос {id} вернул неизвестное состояние: {status}",
        "error.expired": "Запрос {id} истёк {expires}; создайте новый запрос",
        "error.manifest": "Не удалось прочитать манифест: {error}",
        "error.manifest_entry": "Элемент манифеста {index}: {error}",
        "error.manifest_duplicate": "В манифесте повторяется выходной объект: {target}",
        "error.renew_state": "Не удалось использовать состояние продления {path}: {error}",
        "error.key_required": "Для продления нужен закрытый ключ; укажите --key или положите <name>.key рядом с файлом состояния",
        "error.token_file": "Не удалось прочитать файл ключа {path}: {error}",
        "error.token_file_empty": "Файл ключа пуст: {path}",
        "error.base_url": "Некорректный --base-url (должен начинаться с http:// или https://): {url}",
        "error.cancel_confirm": "Отмена не подтверждена; в неинтерактивном режиме используйте --yes",
        "error.operation_conflict": "--status, --cancel и --manifest нельзя использовать вместе",
        "error.staged_exists": "Обнаружены временные файлы предыдущей попытки; продолжите с --request-id или используйте --force:\n  {files}",
        "error.cert_expired": "Скачанный сертификат просрочен или ещё не действует",
        "error.cert_csr_mismatch": "Скачанный сертификат не соответствует отправленному CSR",
        "error.key_needs_source": "--key используется вместе с --csr, --request-id или --domains",
        "error.idempotency_key": "--idempotency-key должен быть не длиннее 128 символов",
        "error.reuse_key": "для --reuse-key требуется --key",

    },
}

for _lang, _extra in EXTRA_MESSAGES.items():
    MESSAGES.setdefault(_lang, {}).update(_extra)

_ACTIVE_LANGUAGE = DEFAULT_LANGUAGE

# ---------------------------------------------------------------------------
# 语言
# ---------------------------------------------------------------------------


def normalize_language(value: str | None) -> str | None:
    """把常见语言标签归一化为脚本支持的具体 locale。"""
    if not value:
        return None
    text = value.strip().lower().replace("_", "-")
    text = text.split(".")[0].split("@")[0]
    aliases = {
        "zh": "zh-CN",
        "zh-cn": "zh-CN",
        "zh-hans": "zh-CN",
        "zh-sg": "zh-CN",
        "cn": "zh-CN",
        "zh-tw": "zh-TW",
        "zh-hk": "zh-TW",
        "zh-mo": "zh-TW",
        "zh-hant": "zh-TW",
        "tw": "zh-TW",
        "en": "en-US",
        "en-us": "en-US",
        "en-gb": "en-US",
        "ja": "ja-JP",
        "ja-jp": "ja-JP",
        "jp": "ja-JP",
        "ru": "ru-RU",
        "ru-ru": "ru-RU",
    }
    if text in aliases:
        return aliases[text]
    # zh-Hant-HK / zh-Hans-SG 之类的组合按脚本归一化。
    if text.startswith("zh-hant") or text.startswith("zh-tw") or text.startswith("zh-hk") or text.startswith("zh-mo"):
        return "zh-TW"
    if text.startswith("zh-hans") or text.startswith("zh-cn") or text.startswith("zh-sg"):
        return "zh-CN"
    base = text.split("-")[0]
    return aliases.get(base)


def detect_language() -> str:
    explicit = normalize_language(os.environ.get("LACTSSL_LANG"))
    if explicit:
        return explicit
    for name in ("LC_ALL", "LC_MESSAGES", "LANG"):
        detected = normalize_language(os.environ.get(name))
        if detected:
            return detected
    return DEFAULT_LANGUAGE


def set_language(value: str | None) -> str:
    global _ACTIVE_LANGUAGE
    normalized = normalize_language(value) or DEFAULT_LANGUAGE
    _ACTIVE_LANGUAGE = normalized
    return normalized


def resolve_language(value: str | None) -> str:
    """归一化语言；显式传入非法语言时报错，而不是静默回退。"""
    normalized = normalize_language(value)
    if value and not normalized:
        raise ConfigError(
            t(
                "error.invalid_lang",
                lang=value,
                languages=", ".join(SUPPORTED_LANGUAGES),
            )
        )
    return normalized or DEFAULT_LANGUAGE


def t(key: str, **params: Any) -> str:
    catalog = MESSAGES.get(_ACTIVE_LANGUAGE) or MESSAGES[DEFAULT_LANGUAGE]
    text = catalog.get(key) or MESSAGES[DEFAULT_LANGUAGE].get(key) or key
    if params:
        try:
            text = text.format(**params)
        except (KeyError, IndexError, ValueError):
            pass
    return text


# ---------------------------------------------------------------------------
# 错误与输出
# ---------------------------------------------------------------------------


class ClientError(Exception):
    """客户端可预期错误，main 统一打印并按 exit_code 退出。"""

    exit_code = 1

    def __init__(self, message: str, exit_code: int | None = None) -> None:
        super().__init__(message)
        if exit_code is not None:
            self.exit_code = exit_code
        # 由 execute 在失败后补齐，供交互模式与 --json 使用。
        self.request_id: str | None = None
        self.state_path: str | None = None
        self.staged: list[str] = []
        self.resume_command: str | None = None


class ConfigError(ClientError):
    """参数、配置或本地文件错误。"""

    exit_code = 2


class AuthError(ClientError):
    """认证或权限错误。"""

    exit_code = 3


class NetworkError(ClientError):
    """网络不可达、超时或连接中断。"""

    exit_code = 4


class ApiError(ClientError):
    """服务端返回的业务错误或申请失败。"""

    exit_code = 5


class VerifyError(ClientError):
    """本地证书校验失败。"""

    exit_code = 6


def log(message: str, quiet: bool = False) -> None:
    """进度信息统一写 stderr，保证 --json 时 stdout 干净。"""
    if not quiet:
        print(message, file=sys.stderr, flush=True)


def warn(message: str) -> None:
    print(message, file=sys.stderr, flush=True)


# ---------------------------------------------------------------------------
# CA 与域名
# ---------------------------------------------------------------------------


def parse_private_ca_value(value: str | None) -> int | None:
    """解析 private-ca:<id> 引用；不是私有 CA 引用时返回 None。"""
    text = (value or "").strip()
    if not text.lower().startswith(PRIVATE_CA_PREFIX):
        return None
    raw = text[len(PRIVATE_CA_PREFIX) :].strip()
    if not raw.isdigit():
        raise ConfigError(t("error.private_ca_id_invalid"))
    identifier = int(raw)
    if identifier <= 0:
        raise ConfigError(t("error.private_ca_id_invalid"))
    return identifier


def safe_private_ca_value(value: str | None) -> int | None:
    """交互向导中用于推断默认值，非法引用不应中断整个流程。"""
    try:
        return parse_private_ca_value(value)
    except ClientError:
        return None


def resolve_ca(args: argparse.Namespace) -> str:
    """把 --private-ca-id 与 --ca 合并成服务端可识别的 CA 值。"""
    ca = (getattr(args, "ca", "") or "").strip()
    private_id = getattr(args, "private_ca_id", None)
    if private_id is not None:
        if private_id <= 0:
            raise ConfigError(t("error.private_ca_id_invalid"))
        if ca:
            parsed = parse_private_ca_value(ca)
            if parsed != private_id:
                raise ConfigError(t("error.ca_conflict"))
        return f"{PRIVATE_CA_PREFIX}{private_id}"
    if not ca:
        return "letsencrypt"
    parsed = parse_private_ca_value(ca)
    if parsed is not None:
        return f"{PRIVATE_CA_PREFIX}{parsed}"
    return ca


def public_ca_default(value: str | None) -> str:
    text = (value or "").strip()
    if text.lower().startswith(PRIVATE_CA_PREFIX):
        return "letsencrypt"
    return text or "letsencrypt"


def to_ascii_domain(value: str) -> str:
    """把 Unicode 域名转为 punycode；通配符前缀保持不变。"""
    value = value.strip().lower().rstrip(".")
    if not value or value.isascii():
        return value
    wildcard = value.startswith("*.")
    body = value[2:] if wildcard else value
    try:
        body = body.encode("idna").decode("ascii")
    except UnicodeError:
        return value
    return ("*." if wildcard else "") + body


def normalize_domains(values: list[str]) -> list[str]:
    """拆分逗号/空白分隔的域名并去重；支持 IDN 转 punycode。"""
    result: list[str] = []
    for value in values:
        for item in re.split(r"[\s,]+", value.strip()):
            item = to_ascii_domain(item)
            if item and item not in result:
                result.append(item)
    return result


def domain_problem(domain: str) -> str | None:
    """返回域名的规范化问题；None 表示通过客户端基础校验。"""
    if not domain:
        return "empty"
    if len(domain) > 253:
        return "too long"
    body = domain[2:] if domain.startswith("*.") else domain
    if not body or "*" in body:
        return "wildcard"
    try:
        ipaddress.ip_address(body)
        return "ip address"
    except ValueError:
        pass
    for label in body.split("."):
        if not label or len(label) > 63:
            return "label length"
        if label.startswith("-") or label.endswith("-"):
            return "label hyphen"
        if not re.fullmatch(r"[a-z0-9-]+", label):
            return "label characters"
    return None


def ensure_valid_domains(domains: list[str]) -> None:
    if not domains:
        raise ConfigError(t("error.domains_required"))
    for domain in domains:
        if domain_problem(domain):
            raise ConfigError(t("error.invalid_domain", domain=domain))


def ensure_same_domains(expected: list[str], actual: list[str], label: str) -> None:
    expected_set = set(expected)
    actual_set = set(actual)
    if expected_set == actual_set:
        return
    missing = sorted(expected_set - actual_set)
    extra = sorted(actual_set - expected_set)
    raise ConfigError(
        t(
            "error.domain_mismatch",
            label=label,
            missing=", ".join(missing) or "-",
            extra=", ".join(extra) or "-",
        )
    )


def safe_name(value: str) -> str:
    value = value.replace("*.", "wildcard.")
    value = re.sub(r"[^A-Za-z0-9._-]+", "_", value).strip("._-")
    value = value or "certificate"
    if len(value) > MAX_SAFE_NAME:
        digest = hashlib.sha256(value.encode("utf-8")).hexdigest()[:8]
        value = value[: MAX_SAFE_NAME - 9].rstrip("._-") + "-" + digest
    return value


# ---------------------------------------------------------------------------
# OpenSSL
# ---------------------------------------------------------------------------


@lru_cache(maxsize=1)
def require_openssl() -> str:
    binary = shutil.which("openssl")
    if not binary:
        raise ConfigError(t("error.openssl_missing"))
    return binary


def run_openssl(
    args: list[str],
    stdin: bytes | None = None,
    passphrase: bool = False,
) -> bytes:
    binary = require_openssl()
    command = list(args)
    if passphrase and os.environ.get(KEY_PASSPHRASE_ENV):
        command.extend(["-passin", f"env:{KEY_PASSPHRASE_ENV}"])
    kwargs: dict[str, Any] = {
        "stdout": subprocess.PIPE,
        "stderr": subprocess.PIPE,
        "check": True,
    }
    if stdin is not None:
        kwargs["input"] = stdin
    else:
        # 避免 OpenSSL 在需要口令时从终端等待输入导致脚本挂起。
        kwargs["stdin"] = subprocess.DEVNULL
    try:
        return subprocess.run([binary, *command], **kwargs).stdout
    except subprocess.CalledProcessError as exc:
        detail = exc.stderr.decode("utf-8", errors="replace").strip()
        raise ClientError(
            t("error.openssl_failed", args=" ".join(command), detail=detail)
        ) from exc
    except OSError as exc:
        raise ClientError(
            t("error.fs", path=binary, error=exc)
        ) from exc


def ensure_openssl_output(paths: list[Path]) -> None:
    if any(not path.exists() for path in paths):
        raise ClientError(t("error.openssl_output_missing"))


def generate_key_and_csr(
    domains: list[str],
    key_type: str,
    work_dir: Path,
) -> tuple[Path, Path]:
    """使用 OpenSSL 在临时目录生成私钥和 CSR。"""
    key_path = work_dir / "client.key"
    csr_path = work_dir / "client.csr"
    common_name = domains[0]
    # CN 最大 64 字节；超长时省略 CN，SAN 仍是域名声明的事实来源。
    subject = f"/CN={common_name}" if len(common_name.encode("ascii")) <= 64 else "/"
    san = ",".join(f"DNS:{domain}" for domain in domains)

    args = [
        "req",
        "-new",
        "-nodes",
        "-sha256",
        "-subj",
        subject,
        "-addext",
        f"subjectAltName={san}",
        "-keyout",
        str(key_path),
        "-out",
        str(csr_path),
    ]
    if key_type in {"ecdsa", "ecdsa384"}:
        curve = "P-256" if key_type == "ecdsa" else "P-384"
        args.extend(["-newkey", "ec", "-pkeyopt", f"ec_paramgen_curve:{curve}"])
    elif key_type == "ed25519":
        args.extend(["-newkey", "ed25519"])
    elif key_type == "rsa2048":
        args.extend(["-newkey", "rsa:2048"])
    elif key_type == "rsa4096":
        args.extend(["-newkey", "rsa:4096"])
    else:
        raise ConfigError(t("error.unsupported_key_type", key_type=key_type))

    run_openssl(args)
    ensure_openssl_output([key_path, csr_path])
    try:
        os.chmod(key_path, 0o600)
    except OSError as exc:
        raise ClientError(t("error.fs", path=key_path, error=exc)) from exc
    return key_path, csr_path


def generate_csr_from_key(
    domains: list[str],
    key_path: Path,
    work_dir: Path,
) -> Path:
    """用已有私钥生成新 CSR（续期场景），私钥不复制、不修改。"""
    csr_path = work_dir / "renew.csr"
    common_name = domains[0]
    subject = f"/CN={common_name}" if len(common_name.encode("ascii")) <= 64 else "/"
    san = ",".join(f"DNS:{domain}" for domain in domains)
    run_openssl(
        [
            "req",
            "-new",
            "-sha256",
            "-subj",
            subject,
            "-addext",
            f"subjectAltName={san}",
            "-key",
            str(key_path),
            "-out",
            str(csr_path),
        ],
        passphrase=True,
    )
    ensure_openssl_output([csr_path])
    return csr_path


def read_text(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8")
    except OSError as exc:
        raise ClientError(t("error.fs", path=path, error=exc)) from exc


# ---------------------------------------------------------------------------
# CSR 解析与预检
# ---------------------------------------------------------------------------


@dataclass
class CSRInfo:
    path: Path
    dns_names: list[str]
    common_name: str
    ip_addresses: list[str] = field(default_factory=list)
    emails: list[str] = field(default_factory=list)
    uris: list[str] = field(default_factory=list)

    @property
    def primary_domain(self) -> str:
        return self.common_name or (self.dns_names[0] if self.dns_names else "")

    @property
    def extra_identifiers(self) -> list[str]:
        return [
            *[f"IP:{value}" for value in self.ip_addresses],
            *[f"email:{value}" for value in self.emails],
            *[f"URI:{value}" for value in self.uris],
        ]


def san_extension_block(text: str) -> str:
    """提取 X509v3 Subject Alternative Name 扩展的正文。"""
    lines = text.splitlines()
    result: list[str] = []
    collecting = False
    header_indent = 0
    for line in lines:
        stripped = line.strip()
        if "X509v3 Subject Alternative Name" in line:
            collecting = True
            header_indent = len(line) - len(line.lstrip())
            continue
        if not collecting:
            continue
        if not stripped:
            continue
        indent = len(line) - len(line.lstrip())
        if indent <= header_indent:
            break
        result.append(stripped)
    return " ".join(result)


def csr_info(csr_path: Path) -> CSRInfo:
    """解析 CSR 的 SAN、CN 与不支持的标识，并校验自签名。"""
    try:
        if csr_path.stat().st_size > 16 * 1024:
            raise ConfigError(t("error.csr_too_large"))
    except OSError as exc:
        raise ClientError(t("error.fs", path=csr_path, error=exc)) from exc
    try:
        verify = subprocess.run(
            [require_openssl(), "req", "-in", str(csr_path), "-noout", "-verify"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except OSError as exc:
        raise ClientError(t("error.fs", path=csr_path, error=exc)) from exc
    if verify.returncode != 0:
        detail = verify.stderr.decode("utf-8", errors="replace").strip()
        raise ConfigError(t("error.csr_invalid", error=detail or "verify failed"))
    text = run_openssl(
        ["req", "-in", str(csr_path), "-noout", "-text"]
    ).decode("utf-8", errors="replace")
    crypto_line = re.search(r"Public Key Algorithm: (\S+)", text)
    algorithm = crypto_line.group(1) if crypto_line else ""
    bits = re.search(r"Public-Key: \((\d+) bit\)", text)
    if algorithm == "rsaEncryption":
        if bits and int(bits.group(1)) < 2048:
            raise ConfigError(t("error.csr_key_weak"))
    elif algorithm == "id-ecPublicKey":
        curve = re.search(r"NIST CURVE:\s*(\S+)", text)
        if curve and curve.group(1) not in {"P-256", "P-384", "P-521"}:
            raise ConfigError(t("error.csr_key_unsupported"))
    elif algorithm not in {"ED25519", "rsassaPss"}:
        raise ConfigError(t("error.csr_key_unsupported"))
    san = san_extension_block(text)
    # 保留原始大小写与尾点，交给 validate_csr_info 按服务端规则检查。
    dns_names = [value.strip() for value in re.findall(r"DNS:([^,\s]+)", san)]
    dns_names = [value for value in dns_names if value]
    ip_addresses = [value for value in re.findall(r"IP Address:([^,\s]+)", san) if value]
    emails = [value for value in re.findall(r"email:([^,\s]+)", san) if value]
    uris = [value for value in re.findall(r"URI:([^,\s]+)", san) if value]
    subject = run_openssl(
        ["req", "-in", str(csr_path), "-noout", "-subject", "-nameopt", "RFC2253"]
    ).decode("utf-8", errors="replace")
    match = re.search(r"\bCN=([^,/\n]+)", subject)
    common_name = match.group(1).strip().strip('"') if match else ""
    return CSRInfo(
        path=csr_path,
        dns_names=dns_names,
        common_name=common_name,
        ip_addresses=ip_addresses,
        emails=emails,
        uris=uris,
    )


def validate_csr_info(info: CSRInfo) -> str:
    """按服务端会执行的关键规则预检 CSR，返回主域名。"""
    extra = info.extra_identifiers
    if extra:
        raise ConfigError(t("error.csr_extra_san", items=", ".join(extra)))
    if not info.dns_names:
        raise ConfigError(t("error.csr_no_san"))
    if len(info.dns_names) > 100:
        raise ConfigError(t("error.csr_too_many", max=100))
    seen: set[str] = set()
    for domain in info.dns_names:
        if domain != domain.lower() or domain.endswith("."):
            raise ConfigError(t("error.csr_noncanonical", domain=domain))
        if domain_problem(domain):
            raise ConfigError(t("error.invalid_domain", domain=domain))
        if domain in seen:
            raise ConfigError(t("error.csr_dup", domain=domain))
        seen.add(domain)
    if info.common_name:
        if len(info.common_name.encode("utf-8")) > 64 or domain_problem(info.common_name):
            raise ConfigError(t("error.csr_cn_invalid", domain=info.common_name))
        if info.common_name not in seen:
            raise ConfigError(t("error.csr_cn_not_in_san", domain=info.common_name))
    return info.primary_domain


def public_key_der_from_key(key_path: Path) -> bytes:
    return run_openssl(
        ["pkey", "-in", str(key_path), "-pubout", "-outform", "DER"],
        passphrase=True,
    )


def public_key_der_from_csr(csr_path: Path) -> bytes:
    pem = run_openssl(["req", "-in", str(csr_path), "-pubkey", "-noout"])
    return run_openssl(["pkey", "-pubin", "-outform", "DER"], stdin=pem)


def key_matches_csr(key_path: Path, csr_path: Path) -> bool:
    return public_key_der_from_key(key_path) == public_key_der_from_csr(csr_path)


def csr_sha256(csr_path: Path) -> str:
    der = run_openssl(["req", "-in", str(csr_path), "-outform", "DER"])
    return hashlib.sha256(der).hexdigest()

# ---------------------------------------------------------------------------
# HTTP 传输
# ---------------------------------------------------------------------------


def api_base(base_url: str) -> str:
    base = base_url.rstrip("/")
    return base if base.endswith("/api") else base + "/api"


@dataclass(frozen=True)
class HttpResponse:
    status: int
    body: bytes
    headers: Any


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """API 不应重定向；返回 None 让 urllib 以 HTTPError 暴露 3xx。"""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[override]
        return None


@lru_cache(maxsize=8)
def _ssl_context(ca_bundle: str | None, insecure: bool) -> ssl.SSLContext:
    try:
        context = ssl.create_default_context(cafile=str(ca_bundle) if ca_bundle else None)
    except OSError as exc:
        raise ConfigError(t("error.ca_bundle", error=exc)) from exc
    if insecure:
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
    return context


@lru_cache(maxsize=8)
def _opener(ca_bundle: str | None, insecure: bool) -> urllib.request.OpenerDirector:
    return urllib.request.build_opener(
        urllib.request.HTTPSHandler(context=_ssl_context(ca_bundle, insecure)),
        _NoRedirectHandler(),
    )


def retry_after_seconds(headers: Any) -> float | None:
    value = headers.get("Retry-After") if headers else None
    if not value:
        return None
    value = str(value).strip()
    if value.isdigit():
        return min(float(value), 60.0)
    try:
        when = email.utils.parsedate_to_datetime(value)
        if when.tzinfo is None:
            when = when.replace(tzinfo=datetime.timezone.utc)
        return max(0.0, min((when - datetime.datetime.now(datetime.timezone.utc)).total_seconds(), 60.0))
    except (TypeError, ValueError):
        return None


def retry_delay(attempt: int, headers: Any = None) -> float:
    after = retry_after_seconds(headers)
    if after is not None:
        return max(after, 0.2)
    return min(2.0 ** attempt, 8.0) + random.uniform(0.0, 0.3)


def api_request(
    args: argparse.Namespace,
    method: str,
    path: str,
    payload: dict[str, Any] | None = None,
) -> HttpResponse:
    url = api_base(args.base_url) + path
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(url, data=data, method=method)
    if getattr(args, "token", ""):
        request.add_header("Authorization", f"Bearer {args.token}")
    request.add_header("Accept", "application/json, application/x-pem-file")
    request.add_header("Accept-Language", getattr(args, "lang", None) or DEFAULT_LANGUAGE)
    request.add_header("X-LACTSSL-Language", getattr(args, "lang", None) or DEFAULT_LANGUAGE)
    request.add_header("User-Agent", USER_AGENT)
    if data is not None:
        request.add_header("Content-Type", "application/json")

    attempts = max(0, int(getattr(args, "retries", DEFAULT_RETRIES))) + 1
    opener = _opener(
        str(args.ca_bundle) if getattr(args, "ca_bundle", None) else None,
        bool(getattr(args, "insecure", False)),
    )
    last_error: Exception | None = None
    for attempt in range(attempts):
        try:
            with opener.open(request, timeout=args.http_timeout) as response:
                body = response.read(MAX_RESPONSE_BYTES)
                length = response.headers.get("Content-Length")
                if length and str(length).isdigit():
                    expected = int(length)
                    if expected > MAX_RESPONSE_BYTES:
                        raise http.client.HTTPException(
                            f"response too large: {expected} bytes"
                        )
                    if len(body) < expected:
                        raise http.client.IncompleteRead(body, expected - len(body))
                return HttpResponse(response.status, body, response.headers)
        except urllib.error.HTTPError as exc:
            try:
                body = exc.read(MAX_RESPONSE_BYTES)
            except (http.client.HTTPException, OSError):
                body = b""
            headers = exc.headers
            if exc.code in TRANSIENT_STATUS and attempt + 1 < attempts:
                delay = retry_delay(attempt, headers)
                log(
                    t("log.retry", attempt=attempt + 1, total=attempts - 1,
                      delay=delay, reason=f"HTTP {exc.code}"),
                    getattr(args, "quiet", False),
                )
                time.sleep(delay)
                continue
            if 300 <= exc.code < 400:
                location = headers.get("Location") if headers else ""
                raise ClientError(
                    t("error.redirect", status=exc.code, url=location or "-")
                ) from exc
            return HttpResponse(exc.code, body, headers)
        except (TimeoutError, ConnectionError, http.client.HTTPException, ssl.SSLError, OSError) as exc:
            last_error = exc
            if attempt + 1 < attempts:
                delay = retry_delay(attempt)
                log(
                    t("log.retry", attempt=attempt + 1, total=attempts - 1,
                      delay=delay, reason=str(exc)),
                    getattr(args, "quiet", False),
                )
                time.sleep(delay)
                continue
            break
    reason = getattr(last_error, "reason", last_error)
    raise NetworkError(
        t("error.connect_retry", attempts=attempts, reason=reason)
    ) from last_error


def decode_json(raw: bytes) -> Any:
    try:
        return json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None


def response_error(status: int, raw: bytes, headers: Any) -> ClientError:
    payload = decode_json(raw)
    message = ""
    code = ""
    if isinstance(payload, dict):
        message = str(payload.get("message") or "")
        code = str(payload.get("code") or "")
    if not message:
        message = raw.decode("utf-8", errors="replace").strip()[:500] or t("error.request_failed")
    suffix = ""
    if code:
        suffix += f" [{code}]"
    if status == 429:
        retry_after = headers.get("Retry-After") if headers else None
        if retry_after:
            suffix += t("error.retry_after", seconds=retry_after)
    error_type: type[ClientError] = AuthError if status in (401, 403) else ApiError
    return error_type(f"HTTP {status}: {message}{suffix}")


def response_data(status: int, raw: bytes, headers: Any) -> Any:
    if status not in (200, 201, 202):
        raise response_error(status, raw, headers)
    body = decode_json(raw)
    return body.get("data") if isinstance(body, dict) else None


def get_request(args: argparse.Namespace, request_id: str) -> dict[str, Any]:
    request_id = urllib.parse.quote(str(request_id), safe="")
    result = api_request(args, "GET", f"/certificate-requests/{request_id}")
    data = response_data(result.status, result.body, result.headers)
    if not isinstance(data, dict) or not data.get("id"):
        raise ApiError(t("error.bad_status_response"))
    return data


def create_request(
    args: argparse.Namespace,
    csr_pem: str,
    idempotency_key: str,
) -> dict[str, Any]:
    private_ca_id = parse_private_ca_value(args.ca)
    payload: dict[str, Any] = {
        "csr": csr_pem,
        "ca": args.ca,
        "idempotency_key": idempotency_key,
        "wait_seconds": max(0, min(int(args.wait_seconds), 30)),
    }
    if private_ca_id is not None:
        # 私有 CA 由服务端直接签名，不需要 DNS-01 挑战与 DNS 账户。
        payload["private_ca_id"] = private_ca_id
        payload["challenge"] = "private"
    else:
        payload["challenge"] = args.challenge
        # 省略时由服务端按管理员维护的域名绑定自动解析 DNS 账户。
        if args.dns_account_id:
            payload["dns_account_id"] = args.dns_account_id
    result = api_request(args, "POST", "/certificate-requests", payload)
    data = response_data(result.status, result.body, result.headers)
    if not isinstance(data, dict) or not data.get("id"):
        raise ApiError(t("error.bad_create_response"))
    return data


def retry_request(args: argparse.Namespace, request_id: str) -> dict[str, Any]:
    quoted = urllib.parse.quote(str(request_id), safe="")
    result = api_request(args, "POST", f"/certificate-requests/{quoted}/retry")
    data = response_data(result.status, result.body, result.headers)
    if not isinstance(data, dict) or not data.get("id"):
        raise ApiError(t("error.bad_status_response"))
    return data


def cancel_request(args: argparse.Namespace, request_id: str) -> dict[str, Any]:
    quoted = urllib.parse.quote(str(request_id), safe="")
    result = api_request(args, "POST", f"/certificate-requests/{quoted}/cancel")
    data = response_data(result.status, result.body, result.headers)
    if isinstance(data, dict) and data.get("id"):
        return data
    return get_request(args, request_id)


def download_certificate(
    args: argparse.Namespace,
    request_id: str,
    kind: str,
) -> str:
    quoted = urllib.parse.quote(str(request_id), safe="")
    result = api_request(
        args, "GET", f"/certificate-requests/{quoted}/certificate?type={kind}"
    )
    if result.status != 200:
        raise response_error(result.status, result.body, result.headers)
    return result.body.decode("utf-8", errors="replace")


def api_list(args: argparse.Namespace, path: str) -> list[dict[str, Any]]:
    result = api_request(args, "GET", path)
    data = response_data(result.status, result.body, result.headers)
    if not isinstance(data, list):
        return []
    return [item for item in data if isinstance(item, dict)]


def list_requests(args: argparse.Namespace) -> list[dict[str, Any]]:
    return api_list(args, "/certificate-requests")


# ---------------------------------------------------------------------------
# 证书校验
# ---------------------------------------------------------------------------

PEM_CERT_RE = re.compile(
    r"-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----", re.S
)


def parse_pem_certificates(text: str, kind: str) -> list[str]:
    blocks = PEM_CERT_RE.findall(text)
    if not blocks:
        raise VerifyError(t("error.download_not_pem", kind=kind))
    for block in blocks:
        try:
            ssl.PEM_cert_to_DER_cert(block)
        except ValueError as exc:
            raise VerifyError(t("error.download_not_pem", kind=kind)) from exc
        try:
            # PEM_cert_to_DER_cert 只做 base64 解码，不校验 DER 结构。
            run_openssl(["x509", "-noout"], stdin=block.encode("utf-8"))
        except ConfigError:
            raise
        except ClientError as exc:
            raise VerifyError(t("error.download_not_pem", kind=kind)) from exc
    return blocks


def certificate_sans(pem: str) -> list[str]:
    text = run_openssl(
        ["x509", "-noout", "-text"], stdin=pem.encode("utf-8")
    ).decode("utf-8", errors="replace")
    san = san_extension_block(text)
    names: list[str] = []
    for name in re.findall(r"DNS:([^,\s]+)", san):
        name = name.strip().lower().rstrip(".")
        if name and name not in names:
            names.append(name)
    return names


def certificate_covers(names: list[str], requested: list[str]) -> list[str]:
    """返回未被证书覆盖的请求域名。"""
    missing: list[str] = []
    for domain in requested:
        domain = domain.lower().rstrip(".")
        if domain in names:
            continue
        covered = False
        for name in names:
            if not name.startswith("*."):
                continue
            suffix = name[1:]  # .example.com
            # 通配符只覆盖一级子域，且不覆盖裸域。
            if domain.endswith(suffix) and domain.count(".") == name.count("."):
                covered = True
                break
        if not covered:
            missing.append(domain)
    return missing


def _openssl_date(value: str) -> datetime.datetime | None:
    try:
        parsed = datetime.datetime.strptime(value.strip(), "%b %d %H:%M:%S %Y %Z")
    except ValueError:
        return None
    return parsed.replace(tzinfo=datetime.timezone.utc)


def certificate_is_current(pem: str) -> bool:
    """同时检查 notBefore 与 notAfter；无法解析时退化为仅检查过期。"""
    try:
        text = run_openssl(
            ["x509", "-noout", "-startdate", "-enddate"], stdin=pem.encode("utf-8")
        ).decode("utf-8", errors="replace")
    except ConfigError:
        raise
    except ClientError:
        return False
    not_before = re.search(r"notBefore=(.+)", text)
    not_after = re.search(r"notAfter=(.+)", text)
    now = datetime.datetime.now(datetime.timezone.utc)
    start = _openssl_date(not_before.group(1)) if not_before else None
    end = _openssl_date(not_after.group(1)) if not_after else None
    if start is not None and now < start:
        return False
    if end is not None and now > end:
        return False
    return True


def certificate_public_key_der(pem: str) -> bytes:
    public = run_openssl(["x509", "-pubkey", "-noout"], stdin=pem.encode("utf-8"))
    return run_openssl(["pkey", "-pubin", "-outform", "DER"], stdin=public)


def private_key_matches_pem(key_path: Path, pem: str) -> bool:
    return public_key_der_from_key(key_path) == certificate_public_key_der(pem)


def certificate_der(pem: str) -> bytes:
    try:
        return ssl.PEM_cert_to_DER_cert(pem)
    except ValueError as exc:
        raise VerifyError(t("error.download_not_pem", kind="cert")) from exc


def download_leaf_and_chain(
    args: argparse.Namespace,
    request_id: str,
) -> tuple[str, str]:
    """下载 fullchain，首张即叶子证书；接口不兼容时回退到 cert。"""
    try:
        chain = download_certificate(args, request_id, "fullchain")
        blocks = parse_pem_certificates(chain, "fullchain")
    except (ApiError, VerifyError):
        chain = download_certificate(args, request_id, "cert")
        blocks = parse_pem_certificates(chain, "cert")
    leaf = blocks[0].strip() + "\n"
    chain = chain.strip() + "\n"
    return leaf, chain


# ---------------------------------------------------------------------------
# 本地文件与申请状态
# ---------------------------------------------------------------------------


def atomic_write(path: Path, content: str, mode: int) -> None:
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        fd, temporary = tempfile.mkstemp(prefix=".lactssl-", dir=str(path.parent))
    except OSError as exc:
        raise ClientError(t("error.fs", path=path, error=exc)) from exc
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(content)
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    except OSError as exc:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise ClientError(t("error.fs", path=path, error=exc)) from exc


def read_state(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ConfigError(t("error.renew_state", path=path, error=exc)) from exc
    if not isinstance(payload, dict):
        raise ConfigError(t("error.renew_state", path=path, error="not a JSON object"))
    return payload


def write_state(path: Path, payload: dict[str, Any]) -> None:
    atomic_write(
        path,
        json.dumps(payload, ensure_ascii=False, indent=2) + "\n",
        0o644,
    )


def update_state(path: Path | None, quiet: bool = False, **fields: Any) -> None:
    if path is None:
        return
    try:
        payload = read_state(path) if path.exists() else {"schema_version": STATE_SCHEMA_VERSION}
        payload.update(fields)
        payload["updated_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        write_state(path, payload)
    except ClientError as exc:
        log(t("warn.state_failed", error=exc), quiet)


def state_request_id(path: Path) -> str:
    try:
        return str(read_state(path).get("request_id") or "")
    except ClientError:
        return ""


# ---------------------------------------------------------------------------
# 输出规划
# ---------------------------------------------------------------------------


@dataclass
class OutputPlan:
    out_dir: Path
    base: str
    cert: Path
    chain: Path
    meta: Path
    key_final: Path | None = None
    key_staged: Path | None = None
    csr_final: Path | None = None
    csr_staged: Path | None = None
    generated_key: bool = False
    generated_csr: bool = False

    @property
    def staged(self) -> list[Path]:
        return [path for path in (self.key_staged, self.csr_staged) if path is not None]


def plan_outputs(
    out_dir: Path,
    base_name: str,
    generated_key: bool,
    generated_csr: bool,
) -> OutputPlan:
    plan = OutputPlan(
        out_dir=out_dir,
        base=base_name,
        cert=out_dir / f"{base_name}.crt",
        chain=out_dir / f"{base_name}.fullchain.crt",
        meta=out_dir / f"{base_name}.request.json",
        generated_key=generated_key,
        generated_csr=generated_csr,
    )
    if generated_key:
        plan.key_final = out_dir / f"{base_name}.key"
        plan.key_staged = out_dir / f"{base_name}.key.new"
    if generated_key or generated_csr:
        plan.csr_final = out_dir / f"{base_name}.csr"
        plan.csr_staged = out_dir / f"{base_name}.csr.new"
    return plan


def plan_final_files(plan: OutputPlan) -> list[Path]:
    paths = [plan.cert, plan.chain, plan.meta]
    if plan.key_final is not None:
        paths.append(plan.key_final)
    if plan.csr_final is not None:
        paths.append(plan.csr_final)
    return paths


def check_outputs(
    plan: OutputPlan,
    args: argparse.Namespace,
    request_id: str = "",
) -> None:
    existing: list[str] = []
    same_request = request_id and state_request_id(plan.meta) == request_id
    for path in plan_final_files(plan):
        if not path.exists():
            continue
        if args.force or same_request:
            continue
        existing.append(str(path))
    if existing:
        raise ConfigError(t("error.files_exist", files="\n  ".join(existing)))
    if not args.force and not same_request:
        staged = [str(path) for path in plan.staged if path.exists()]
        if staged:
            raise ConfigError(t("error.staged_exists", files="\n  ".join(staged)))


def commit_outputs(
    plan: OutputPlan,
    cert_pem: str,
    chain_pem: str,
    metadata: dict[str, Any],
) -> None:
    atomic_write(plan.cert, cert_pem, 0o644)
    atomic_write(plan.chain, chain_pem, 0o644)
    try:
        if plan.key_staged is not None and plan.key_final is not None:
            os.replace(plan.key_staged, plan.key_final)
        if plan.csr_staged is not None and plan.csr_final is not None:
            os.replace(plan.csr_staged, plan.csr_final)
    except OSError as exc:
        raise ClientError(
            t("error.fs", path=plan.key_final or plan.csr_final, error=exc)
        ) from exc
    # 状态文件最后写，避免把未完成的结果标记为 issued。
    atomic_write(
        plan.meta,
        json.dumps(metadata, ensure_ascii=False, indent=2) + "\n",
        0o644,
    )


# ---------------------------------------------------------------------------
# 参数校验与配置
# ---------------------------------------------------------------------------


def resolve_token(args: argparse.Namespace) -> str:
    if args.token:
        return args.token.strip()
    token_file = getattr(args, "token_file", "") or ""
    if not token_file:
        return ""
    path = Path(token_file).expanduser()
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise ConfigError(t("error.token_file", path=path, error=exc)) from exc
    token = text.splitlines()[0].strip() if text.strip() else ""
    if not token:
        raise ConfigError(t("error.token_file_empty", path=path))
    return token


def state_relative_path(value: Any, base: Path) -> Path:
    path = Path(str(value)).expanduser()
    if not path.is_absolute():
        path = base / path
    return path


def apply_renew_state(args: argparse.Namespace) -> None:
    path = Path(str(args.renew_from)).expanduser()
    state = read_state(path)
    domains = state.get("domains") or []
    if not domains and state.get("primary_domain"):
        domains = [state["primary_domain"]]
    if not isinstance(domains, list):
        raise ConfigError(t("error.renew_state", path=path, error="domains is not a list"))
    if not normalize_domains(args.domains):
        args.domains = [str(value) for value in domains]
    if not args.ca:
        args.ca = str(state.get("ca") or "")
    if args.private_ca_id is None and state.get("private_ca_id"):
        args.private_ca_id = int(state["private_ca_id"])
    if args.dns_account_id is None and state.get("dns_account_id"):
        args.dns_account_id = int(state["dns_account_id"])
    if not args.name and state.get("name"):
        args.name = str(state["name"])
    if not args.out_dir:
        # 旧版元数据没有 out_dir，默认使用状态文件所在目录。
        args.out_dir = str(state.get("out_dir") or path.parent)
    if not args.key_type and state.get("key_type"):
        args.key_type = str(state["key_type"])
    if not args.key:
        candidates = []
        if state.get("key_path"):
            candidates.append(state_relative_path(state["key_path"], path.parent))
        base = safe_name(args.name or (normalize_domains(args.domains)[0] if args.domains else ""))
        out_dir = Path(args.out_dir or ".").expanduser()
        candidates.extend([out_dir / f"{base}.key.new", out_dir / f"{base}.key"])
        for candidate in candidates:
            if candidate.is_file():
                args.key = str(candidate)
                break
    if not args.key:
        raise ConfigError(t("error.key_required"))
    args.reuse_key = True
    # 续期默认覆盖同一目标的旧证书；--name/--out-dir 可改到别处。
    args.force = True


def validate_args(args: argparse.Namespace) -> argparse.Namespace:
    args.base_url = (args.base_url or "").strip().rstrip("/")
    if not re.match(r"^https?://", args.base_url, re.IGNORECASE):
        raise ConfigError(t("error.base_url", url=args.base_url or "-"))
    args.token = resolve_token(args)
    if not args.token:
        raise ConfigError(t("error.no_token"))
    args.lang = resolve_language(args.lang)
    set_language(args.lang)

    if args.status and args.cancel:
        raise ConfigError(t("error.operation_conflict"))
    if args.list and (args.status or args.cancel):
        raise ConfigError(t("error.operation_conflict"))
    if args.manifest and (args.status or args.cancel or args.list):
        raise ConfigError(t("error.operation_conflict"))
    if args.renew_from:
        apply_renew_state(args)

    if args.out_dir is None:
        args.out_dir = "."
    if args.key_type is None:
        args.key_type = "ecdsa"
    if args.key_type not in KEY_TYPES:
        raise ConfigError(t("error.unsupported_key_type", key_type=args.key_type))
    if args.wait_seconds < 0 or args.wait_seconds > 30:
        raise ConfigError(t("error.wait_seconds"))
    if args.timeout < 1:
        raise ConfigError(t("error.timeout_value"))
    if args.poll_interval < 0.5:
        raise ConfigError(t("error.poll_interval"))
    if args.http_timeout <= 0 or args.http_timeout <= args.wait_seconds:
        raise ConfigError(t("error.http_timeout"))
    if args.dns_account_id is not None and args.dns_account_id < 1:
        raise ConfigError(t("error.dns_account_id"))
    if args.retries < 0 or args.retries > 10:
        raise ConfigError(t("error.retries"))
    if args.auto_retry < 0 or args.auto_retry > 5:
        raise ConfigError(t("error.auto_retry"))
    if args.reuse_key and not args.key:
        raise ConfigError(t("error.reuse_key"))
    if args.status or args.cancel or args.list:
        return args

    if args.key and not args.csr and not args.request_id and not normalize_domains(args.domains):
        raise ConfigError(t("error.key_needs_source"))
    return args

# ---------------------------------------------------------------------------
# 签发流程
# ---------------------------------------------------------------------------


def request_domains(request: dict[str, Any]) -> tuple[list[str], str]:
    domains: list[str] = []
    for value in request.get("identifiers") or []:
        item = str(value).strip().lower().rstrip(".")
        if item and item not in domains:
            domains.append(item)
    primary = str(request.get("primary_domain") or "").strip().lower().rstrip(".")
    if not domains and primary:
        domains = [primary]
    if not primary:
        primary = domains[0] if domains else ""
    if not domains:
        raise ConfigError(t("error.request_no_domains"))
    return domains, primary


def parse_timestamp(value: Any) -> datetime.datetime | None:
    if not value:
        return None
    text = str(value).strip()
    try:
        parsed = datetime.datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=datetime.timezone.utc)
    return parsed


def local_csr_sha256(csr_path: Path | None) -> str | None:
    if csr_path is None or not csr_path.is_file():
        return None
    try:
        return csr_sha256(csr_path)
    except ClientError:
        return None


def state_payload(
    args: argparse.Namespace,
    plan: OutputPlan,
    request: dict[str, Any],
    domains: list[str],
    primary: str,
    key_path: Path | None,
    csr_path: Path | None,
    idempotency_key: str,
) -> dict[str, Any]:
    return {
        "schema_version": STATE_SCHEMA_VERSION,
        "request_id": str(request.get("id") or ""),
        "status": request.get("status"),
        "server": args.base_url,
        "domains": domains,
        "primary_domain": primary,
        "ca": request.get("ca") or args.ca,
        "private_ca_id": parse_private_ca_value(request.get("ca") or args.ca),
        "dns_account_id": request.get("dns_account_id") or args.dns_account_id,
        "challenge": request.get("challenge"),
        "idempotency_key": idempotency_key,
        "csr_sha256": request.get("csr_sha256") or local_csr_sha256(csr_path),
        "expires_at": request.get("expires_at"),
        "created_at": request.get("created_at"),
        "completed_at": request.get("completed_at"),
        "out_dir": str(plan.out_dir),
        "name": plan.base,
        "key_type": args.key_type,
        "key_path": str(key_path) if key_path else None,
        "csr_path": str(csr_path) if csr_path else None,
        "generated_key": plan.generated_key,
        "generated_csr": plan.generated_csr,
        "attempts": request.get("attempts"),
        "retryable": request.get("retryable"),
        "resolved_zone": request.get("resolved_zone"),
    }


def build_resume_command(args: argparse.Namespace, request_id: str, state_path: Path) -> str:
    parts = [
        "python3 scripts/csr_client.py",
        f"--base-url {shlex_quote(args.base_url)}",
        f"--request-id {shlex_quote(request_id)}",
        f"--out-dir {shlex_quote(str(Path(args.out_dir).expanduser()))}",
    ]
    if args.name:
        parts.append(f"--name {shlex_quote(args.name)}")
    if args.key:
        parts.append(f"--key {shlex_quote(args.key)}")
    if getattr(args, "ca_bundle", None):
        parts.append(f"--ca-bundle {shlex_quote(str(args.ca_bundle))}")
    if getattr(args, "insecure", False):
        parts.append("--insecure")
    parts.append(f"--lang {args.lang}")
    if state_path.name.endswith(".request.json"):
        parts.append(f"# state: {state_path}")
    return " ".join(parts)


def shlex_quote(value: str) -> str:
    if re.fullmatch(r"[A-Za-z0-9_./:@%+=,-]+", value or ""):
        return value
    return "'" + value.replace("'", "'\"'\"'") + "'"


def resume_files(
    args: argparse.Namespace,
    plan: OutputPlan,
    request_id: str,
    current_csr: Path | None,
) -> tuple[Path | None, Path | None]:
    """为 --request-id 自动寻找上次留下的私钥/CSR。"""
    if args.key:
        return Path(args.key).expanduser(), current_csr
    state_matches = plan.meta.exists() and state_request_id(plan.meta) == request_id
    if plan.meta.exists() and not state_matches:
        # 状态文件属于别的申请时不猜测私钥，避免用错证书对应的密钥。
        return None, current_csr
    candidates: list[Path] = []
    if state_matches:
        try:
            state = read_state(plan.meta)
        except ClientError:
            state = {}
        for field_name in ("key_path", "key_staged"):
            if state.get(field_name):
                candidates.append(
                    state_relative_path(state[field_name], plan.meta.parent)
                )
        if current_csr is None and state.get("csr_path"):
            candidate = state_relative_path(state["csr_path"], plan.meta.parent)
            if candidate.is_file():
                current_csr = candidate
    candidates.extend([plan.out_dir / f"{plan.base}.key.new", plan.out_dir / f"{plan.base}.key"])
    for candidate in candidates:
        if candidate.is_file():
            log(t("log.auto_key", path=candidate), args.quiet)
            # 上一次失败留下的暂存文件在下载成功后提升为最终文件。
            if candidate.name == f"{plan.base}.key.new" and candidate.parent == plan.out_dir:
                plan.key_staged = candidate
                plan.key_final = plan.out_dir / f"{plan.base}.key"
            if current_csr is not None and current_csr.name == f"{plan.base}.csr.new" and current_csr.parent == plan.out_dir:
                plan.csr_staged = current_csr
                plan.csr_final = plan.out_dir / f"{plan.base}.csr"
            return candidate, current_csr
    if current_csr is not None and current_csr.name == f"{plan.base}.csr.new" and current_csr.parent == plan.out_dir:
        plan.csr_staged = current_csr
        plan.csr_final = plan.out_dir / f"{plan.base}.csr"
    return None, current_csr


def wait_for_request(
    args: argparse.Namespace,
    initial: dict[str, Any],
    state_path: Path | None,
) -> dict[str, Any]:
    request = initial
    request_id = str(request["id"])
    deadline = time.monotonic() + args.timeout
    last_status = ""
    unknown = 0
    poll = 0
    retries_left = args.auto_retry
    while True:
        status = str(request.get("status") or "")
        if status != last_status:
            extras: list[str] = []
            if request.get("attempts"):
                extras.append(f"{t('summary.attempts')}={request['attempts']}")
            if request.get("resolved_zone"):
                extras.append(f"{t('summary.dns_routes')}={request['resolved_zone']}")
            suffix = f" ({', '.join(extras)})" if extras else ""
            log(
                t("error.request_status", id=request_id, status=status) + suffix,
                args.quiet,
            )
            last_status = status
            update_state(
                state_path,
                quiet=args.quiet,
                status=status,
                attempts=request.get("attempts"),
                retryable=request.get("retryable"),
                expires_at=request.get("expires_at"),
                resolved_zone=request.get("resolved_zone"),
            )
        if status == "issued":
            return request
        if status in TERMINAL_STATUSES:
            if status == "failed" and request.get("retryable") and retries_left > 0:
                log(
                    t(
                        "log.retry_request",
                        id=request_id,
                        attempt=args.auto_retry - retries_left + 1,
                        total=args.auto_retry,
                    ),
                    args.quiet,
                )
                request = retry_request(args, request_id)
                retries_left -= 1
                last_status = ""
                continue
            error = request.get("error")
            detail = ""
            if isinstance(error, dict):
                detail = " ".join(
                    str(value) for value in (error.get("code"), error.get("message")) if value
                )
            retry_hint = t("error.retry_hint") if request.get("retryable") else ""
            raise ApiError(
                t("error.not_issued", status=status, detail=detail, retry_hint=retry_hint).strip()
            )
        if status not in KNOWN_STATUSES:
            unknown += 1
            if unknown >= 3:
                raise ApiError(
                    t("error.unknown_status", id=request_id, status=status or "-")
                )
        else:
            unknown = 0

        expires = parse_timestamp(request.get("expires_at"))
        if expires is not None and datetime.datetime.now(datetime.timezone.utc) > expires:
            raise ApiError(
                t("error.expired", id=request_id, expires=request.get("expires_at"))
            )
        if time.monotonic() >= deadline:
            raise ApiError(t("error.timeout", timeout=args.timeout, id=request_id))

        poll += 1
        delay = min(max(args.poll_interval, 0.5) * (1.4 ** min(poll, 5)), 15.0)
        time.sleep(delay + random.uniform(0.0, 0.2))
        request = get_request(args, request_id)


def execute(args: argparse.Namespace) -> dict[str, Any]:
    validate_args(args)
    args.ca = resolve_ca(args)
    if args.insecure:
        warn(t("warn.insecure"))

    out_dir = Path(args.out_dir).expanduser().resolve()
    work_root = Path(tempfile.mkdtemp(prefix="lactssl-csr-"))
    request: dict[str, Any] | None = None
    plan: OutputPlan | None = None
    state_path: Path | None = None
    key_path: Path | None = None
    csr_path: Path | None = None
    idempotency_key = ""
    domain_list: list[str] = []
    primary = ""
    staged_paths: list[Path] = []
    try:
        provided_domains = normalize_domains(args.domains)
        if args.csr:
            csr_path = Path(args.csr).expanduser().resolve()
            if not csr_path.is_file():
                raise ConfigError(t("error.csr_not_found", path=csr_path))
        if args.key:
            key_path = Path(args.key).expanduser().resolve()
            if not key_path.is_file():
                raise ConfigError(t("error.key_not_found", path=key_path))

        if args.request_id:
            request = get_request(args, args.request_id)
            domain_list, primary = request_domains(request)
            if provided_domains:
                ensure_same_domains(domain_list, provided_domains, "--domains")
            plan = plan_outputs(
                out_dir, safe_name(args.name or primary), generated_key=False, generated_csr=False
            )
            key_path, csr_path = resume_files(args, plan, str(request["id"]), csr_path)
            if csr_path is not None:
                info = csr_info(csr_path)
                validate_csr_info(info)
                ensure_same_domains(domain_list, info.dns_names, "CSR")
                if key_path is not None and not key_matches_csr(key_path, csr_path):
                    raise ConfigError(t("error.csr_key_mismatch"))
            check_outputs(plan, args, str(request["id"]))
        elif csr_path is not None:
            info = csr_info(csr_path)
            primary = validate_csr_info(info)
            domain_list = info.dns_names
            if provided_domains:
                ensure_same_domains(domain_list, provided_domains, "--domains")
            if key_path is not None and not key_matches_csr(key_path, csr_path):
                raise ConfigError(t("error.csr_key_mismatch"))
            plan = plan_outputs(
                out_dir, safe_name(args.name or primary), generated_key=False, generated_csr=False
            )
            check_outputs(plan, args)
        elif key_path is not None:
            ensure_valid_domains(provided_domains)
            domain_list = provided_domains
            primary = domain_list[0]
            csr_path = generate_csr_from_key(domain_list, key_path, work_root)
            info = csr_info(csr_path)
            validate_csr_info(info)
            if not key_matches_csr(key_path, csr_path):
                raise ConfigError(t("error.csr_key_mismatch"))
            plan = plan_outputs(
                out_dir, safe_name(args.name or primary), generated_key=False, generated_csr=True
            )
            check_outputs(plan, args)
            if plan.csr_staged is None:
                raise ClientError(t("error.local_key_missing"))
            atomic_write(plan.csr_staged, read_text(csr_path), 0o644)
            staged_paths.append(plan.csr_staged)
            csr_path = plan.csr_staged
            log(t("log.csr_written", path=plan.csr_staged), args.quiet)
        else:
            ensure_valid_domains(provided_domains)
            domain_list = provided_domains
            primary = domain_list[0]
            temp_key, temp_csr = generate_key_and_csr(domain_list, args.key_type, work_root)
            info = csr_info(temp_csr)
            validate_csr_info(info)
            plan = plan_outputs(
                out_dir, safe_name(args.name or primary), generated_key=True, generated_csr=True
            )
            check_outputs(plan, args)
            if plan.key_staged is None or plan.csr_staged is None:
                raise ClientError(t("error.local_key_missing"))
            atomic_write(plan.key_staged, read_text(temp_key), 0o600)
            atomic_write(plan.csr_staged, read_text(temp_csr), 0o644)
            staged_paths.extend([plan.key_staged, plan.csr_staged])
            key_path = plan.key_staged
            csr_path = plan.csr_staged
            log(t("log.key_written", path=plan.key_staged), args.quiet)
            log(t("log.csr_written", path=plan.csr_staged), args.quiet)

        if args.idempotency_key and len(args.idempotency_key) > 128:
            raise ConfigError(t("error.idempotency_key"))

        if request is None:
            if csr_path is None:
                raise ClientError(t("error.missing_csr"))
            idempotency_key = args.idempotency_key or f"csr-client-{uuid.uuid4().hex}"
            request = create_request(args, read_text(csr_path), idempotency_key)
            state_path = plan.meta
            state = state_payload(
                args, plan, request, domain_list, primary, key_path, csr_path, idempotency_key
            )
            write_state(state_path, state)
            log(t("log.created", id=request["id"]), args.quiet)
            log(t("log.state_saved", path=state_path), args.quiet)
        else:
            state_path = plan.meta
            if state_path.exists() and state_request_id(state_path) == str(request["id"]):
                try:
                    idempotency_key = str(read_state(state_path).get("idempotency_key") or "")
                except ClientError:
                    idempotency_key = ""
            if not state_path.exists():
                write_state(
                    state_path,
                    state_payload(
                        args, plan, request, domain_list, primary, key_path, csr_path, idempotency_key
                    ),
                )

        args._last_request_id = str(request["id"])
        args._last_state_path = str(state_path)
        request = wait_for_request(args, request, state_path)
        request_id = str(request["id"])
        log(t("log.issued"), args.quiet)

        leaf, chain = download_leaf_and_chain(args, request_id)
        if not certificate_is_current(leaf):
            raise VerifyError(t("error.cert_expired"))
        sans = certificate_sans(leaf)
        missing = certificate_covers(sans, domain_list)
        if missing:
            raise VerifyError(
                t("error.cert_missing_domains", domains=", ".join(missing))
            )
        if key_path is not None:
            if not private_key_matches_pem(key_path, leaf):
                raise VerifyError(t("error.key_mismatch"))
        else:
            log(t("log.no_key"), args.quiet)
        if csr_path is not None and csr_path.is_file():
            if certificate_public_key_der(leaf) != public_key_der_from_csr(csr_path):
                raise VerifyError(t("error.cert_csr_mismatch"))

        final_ca = str(request.get("ca") or args.ca)
        metadata = state_payload(
            args, plan, request, domain_list, primary, key_path, csr_path, idempotency_key
        )
        metadata.update(
            {
                "status": "issued",
                "certificate_id": request.get("certificate_id"),
                "issuer": request.get("issuer"),
                "serial": request.get("serial"),
                "not_before": request.get("not_before"),
                "not_after": request.get("not_after"),
                "completed_at": request.get("completed_at"),
                "cert_sha256": hashlib.sha256(certificate_der(leaf)).hexdigest(),
                "key_path": str(plan.key_final) if plan.key_final else (str(key_path) if key_path else None),
                "csr_path": str(plan.csr_final) if plan.csr_final else (str(csr_path) if csr_path else None),
                "ca": final_ca,
                "private_ca_id": parse_private_ca_value(final_ca),
            }
        )
        commit_outputs(plan, leaf, chain, metadata)
        update_state(state_path, quiet=True, status="issued", completed_at=request.get("completed_at"))

        paths: dict[str, str] = {
            "certificate": str(plan.cert),
            "chain": str(plan.chain),
            "metadata": str(plan.meta),
        }
        if plan.key_final is not None:
            paths["key"] = str(plan.key_final)
        elif key_path is not None:
            paths["key"] = str(key_path)
        if plan.csr_final is not None:
            paths["csr"] = str(plan.csr_final)
        elif csr_path is not None:
            paths["csr"] = str(csr_path)
        return {
            "request_id": request_id,
            "status": "issued",
            "domains": domain_list,
            "primary_domain": primary,
            "ca": final_ca,
            "paths": paths,
            "generated_key": plan.generated_key,
            "generated_csr": plan.generated_csr,
            "issuer": request.get("issuer"),
            "serial": request.get("serial"),
            "not_before": request.get("not_before"),
            "not_after": request.get("not_after"),
            "csr_sha256": request.get("csr_sha256"),
            "cert_sha256": metadata["cert_sha256"],
            "tls_verified": not bool(args.insecure),
        }
    except KeyboardInterrupt:
        if request is not None:
            request_id = str(request.get("id") or "")
            update_state(state_path, quiet=True, error="interrupted")
            log(
                t(
                    "log.resume",
                    command=build_resume_command(
                        args, request_id, state_path or Path(args.out_dir) / "state"
                    ),
                )
            )
        raise
    except ClientError as exc:
        if request is not None:
            request_id = str(request.get("id") or "")
            exc.request_id = request_id
            update_state(
                state_path,
                quiet=True,
                error=str(exc),
                retryable=request.get("retryable"),
            )
            command = build_resume_command(
                args, request_id, state_path or Path(args.out_dir) / "state"
            )
            exc.resume_command = command
            log(t("log.resume", command=command))
        exc.state_path = str(state_path) if state_path else None
        exc.staged = [str(path) for path in staged_paths]
        if staged_paths:
            warn(t("warn.staged_kept", files=", ".join(str(path) for path in staged_paths)))
        raise
    finally:
        shutil.rmtree(work_root, ignore_errors=True)


def emit_result(args: argparse.Namespace, result: dict[str, Any]) -> None:
    if getattr(args, "json", False):
        print(json.dumps({"success": True, **result}, ensure_ascii=False, indent=2))
        return
    print(t("done.title"))
    print(f"  {t('done.request_id')}: {result['request_id']}")
    print(f"  {t('done.status')}: {result['status']}")
    print(f"  {t('done.domains')}: {', '.join(result['domains'])}")
    paths = result.get("paths") or {}
    print(f"  {t('done.cert')}: {paths.get('certificate', '-')}")
    print(f"  {t('done.chain')}: {paths.get('chain', '-')}")
    if paths.get("key"):
        suffix = ""
        if not result.get("generated_key"):
            suffix = f" ({t('done.existing')})"
        print(f"  {t('done.key')}: {paths['key']}{suffix}")
    if paths.get("csr"):
        print(f"  {t('done.csr')}: {paths['csr']}")
    if result.get("issuer"):
        print(f"  {t('done.issuer')}: {result['issuer']}")
    if result.get("serial"):
        print(f"  {t('done.serial')}: {result['serial']}")
    if result.get("not_after"):
        print(f"  {t('done.not_after')}: {result['not_after']}")
    print(f"  {t('done.meta')}: {paths.get('metadata', '-')}")


def format_request_view(request: dict[str, Any]) -> str:
    lines = [
        f"  {t('done.request_id')}: {request.get('id') or '-'}",
        f"  {t('done.status')}: {request.get('status') or '-'}",
        f"  {t('done.domains')}: {', '.join(str(value) for value in request.get('identifiers') or []) or '-'}",
        f"  {t('summary.ca')}: {request.get('ca') or '-'}",
        f"  {t('summary.challenge')}: {request.get('challenge') or '-'}",
        f"  {t('summary.attempts')}: {request.get('attempts') if request.get('attempts') is not None else '-'}",
        f"  {t('summary.retryable')}: {t('summary.yes') if request.get('retryable') else t('summary.no')}",
    ]
    if request.get("resolved_zone"):
        lines.append(f"  {t('summary.dns_routes')}: {request['resolved_zone']}")
    if request.get("expires_at"):
        lines.append(f"  {t('summary.expires')}: {request['expires_at']}")
    if request.get("not_after"):
        lines.append(f"  {t('done.not_after')}: {request['not_after']}")
    error = request.get("error")
    if isinstance(error, dict) and (error.get("code") or error.get("message")):
        detail = " ".join(str(value) for value in (error.get("code"), error.get("message")) if value)
        lines.append(f"  {t('summary.error')}: {detail}")
    return "\n".join(lines)


def command_status(args: argparse.Namespace) -> int:
    validate_args(args)
    request = get_request(args, args.status)
    if args.json:
        print(json.dumps({"success": True, "request": request}, ensure_ascii=False, indent=2))
    else:
        print(t("status.title"))
        print(format_request_view(request))
    return 0


def command_list(args: argparse.Namespace) -> int:
    validate_args(args)
    requests = list_requests(args)
    if args.json:
        print(json.dumps({"success": True, "requests": requests}, ensure_ascii=False, indent=2))
        return 0
    print(t("list.title"))
    if not requests:
        print(f"  {t('list.empty')}")
        return 0
    for request in requests:
        domains = ", ".join(str(value) for value in request.get("identifiers") or [])
        print(
            f"  {request.get('id') or '-'}  "
            f"{request.get('status') or '-':<10}  "
            f"{domains or '-'}  {request.get('ca') or '-'}"
        )
    return 0


def command_cancel(args: argparse.Namespace) -> int:
    validate_args(args)
    if not args.yes:
        if not sys.stdin.isatty() or not ask_yes_no(t("cancel.confirm", id=args.cancel), False):
            raise ConfigError(t("error.cancel_confirm"))
    request = cancel_request(args, args.cancel)
    if args.json:
        print(json.dumps({"success": True, "request": request}, ensure_ascii=False, indent=2))
    else:
        print(t("cancel.done", id=args.cancel))
        print(format_request_view(request))
    return 0


# ---------------------------------------------------------------------------
# 批量模式
# ---------------------------------------------------------------------------

MANIFEST_KEYS = {
    "domains",
    "name",
    "ca",
    "private_ca_id",
    "dns_account_id",
    "key_type",
    "out_dir",
    "key",
    "csr",
    "request_id",
    "challenge",
    "timeout",
    "poll_interval",
    "http_timeout",
    "wait_seconds",
    "retries",
    "auto_retry",
    "idempotency_key",
    "reuse_key",
    "renew_from",
    "force",
}


def load_manifest(path: Path) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ConfigError(t("error.manifest", error=exc)) from exc
    if not isinstance(payload, dict):
        raise ConfigError(t("error.manifest", error="root must be an object"))
    defaults = payload.get("defaults") or {}
    entries = payload.get("certificates")
    if not isinstance(defaults, dict) or not isinstance(entries, list) or not entries:
        raise ConfigError(t("error.manifest", error="certificates must be a non-empty array"))
    certificates = [entry for entry in entries if isinstance(entry, dict)]
    if not certificates:
        raise ConfigError(t("error.manifest", error="certificates must contain objects"))
    return defaults, certificates


def apply_manifest_values(
    child: argparse.Namespace,
    values: dict[str, Any],
    index: int,
) -> None:
    int_fields = {
        "private_ca_id",
        "dns_account_id",
        "timeout",
        "wait_seconds",
        "retries",
        "auto_retry",
    }
    float_fields = {"poll_interval", "http_timeout"}
    bool_fields = {"force", "reuse_key"}
    for key, value in values.items():
        if key not in MANIFEST_KEYS:
            raise ConfigError(
                t("error.manifest_entry", index=index, error=f"unknown field: {key}")
            )
        try:
            if key == "domains":
                if isinstance(value, str):
                    child.domains = [value]
                elif isinstance(value, list):
                    child.domains = [str(item) for item in value]
                else:
                    raise ValueError("domains must be a string or array")
            elif key in int_fields:
                if value is None:
                    if key in {"private_ca_id", "dns_account_id"}:
                        setattr(child, key, None)
                    else:
                        raise ValueError(f"{key} must be an integer")
                else:
                    setattr(child, key, int(value))
            elif key in float_fields:
                if value is None:
                    raise ValueError(f"{key} must be a number")
                setattr(child, key, float(value))
            elif key in bool_fields:
                if not isinstance(value, bool):
                    raise ValueError(f"{key} must be a boolean")
                setattr(child, key, value)
            else:
                setattr(child, key, None if value is None else str(value))
        except (TypeError, ValueError) as exc:
            raise ConfigError(
                t("error.manifest_entry", index=index, error=f"{key}: {exc}")
            ) from exc


def run_batch(args: argparse.Namespace) -> int:
    path = Path(args.manifest).expanduser()
    defaults, entries = load_manifest(path)
    results: list[dict[str, Any]] = []
    succeeded = 0
    failed = 0
    seen: set[tuple[str, str]] = set()
    for index, entry in enumerate(entries, 1):
        child = copy.deepcopy(args)
        child.status = None
        child.cancel = None
        child.list = False
        child.manifest = None
        child.json = False
        child.interactive = False
        child.renew_from = None
        child.reuse_key = False
        apply_manifest_values(child, defaults, index)
        apply_manifest_values(child, entry, index)
        if not child.challenge:
            child.challenge = "dns01"
        if not child.domains and not child.request_id and not child.csr and not child.renew_from:
            raise ConfigError(
                t("error.manifest_entry", index=index, error="domains are required")
            )
        target = (
            str(child.out_dir or "."),
            str(
                child.name
                or (normalize_domains(child.domains)[0] if child.domains else "")
                or child.request_id
                or ""
            ),
        )
        if target in seen:
            raise ConfigError(t("error.manifest_duplicate", target=f"{target[0]}/{target[1]}"))
        seen.add(target)
        label = child.name or (normalize_domains(child.domains)[0] if child.domains else child.request_id or "-")
        try:
            result = execute(child)
        except ClientError as exc:
            failed += 1
            results.append(
                {
                    "index": index,
                    "name": label,
                    "success": False,
                    "error": str(exc),
                    "exit_code": exc.exit_code,
                    "request_id": getattr(exc, "request_id", None),
                }
            )
            if args.on_error == "stop":
                break
        else:
            succeeded += 1
            results.append({"index": index, "name": label, "success": True, **result})
    if args.json:
        print(
            json.dumps(
                {
                    "success": failed == 0,
                    "succeeded": succeeded,
                    "failed": failed,
                    "results": results,
                },
                ensure_ascii=False,
                indent=2,
            )
        )
    else:
        print(t("batch.title"))
        for item in results:
            if item["success"]:
                print(
                    "  "
                    + t("batch.item_ok", index=item["index"], name=item["name"])
                )
            else:
                print(
                    "  "
                    + t(
                        "batch.item_failed",
                        index=item["index"],
                        name=item["name"],
                        error=item["error"],
                    )
                )
        print("  " + t("batch.summary", succeeded=succeeded, failed=failed))
    return 0 if failed == 0 else 1


# ---------------------------------------------------------------------------
# 交互模式
# ---------------------------------------------------------------------------


class WizardScreen:
    """交互向导的清屏与状态重绘。"""

    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self.phase_number = 1
        self.phase_key = "section.connection"
        no_clear = os.environ.get("LACTSSL_NO_CLEAR", "").strip().lower()
        self.enabled = (
            sys.stdout.isatty()
            and os.environ.get("TERM", "") != "dumb"
            and no_clear not in {"1", "true", "yes", "on"}
        )

    def clear(self) -> None:
        if not self.enabled:
            return
        sys.stdout.write("\033[2J\033[H")
        sys.stdout.flush()

    def phase(self, number: int, key: str) -> None:
        self.phase_number = number
        self.phase_key = key
        if self.enabled:
            self.redraw()
        else:
            print()
            print(t(key))

    def redraw(self) -> None:
        if not self.enabled:
            return
        self.clear()
        print(t("app.title"))
        print(t("app.privacy"))
        print()
        print(
            f"{t('wizard.step', current=self.phase_number, total=5)}"
            f" · {t(self.phase_key)}"
        )
        print("-" * 56)
        rows = build_summary_rows(self.args)
        if rows:
            print(t("wizard.current"))
            for label, value in rows:
                print(f"  {label}: {value}")
        print()

    def redraw_with_rows(self, rows: list[tuple[str, str]]) -> None:
        if not self.enabled:
            return
        self.clear()
        print(t("app.title"))
        print(t("app.privacy"))
        print()
        print(
            f"{t('wizard.step', current=self.phase_number, total=5)}"
            f" · {t(self.phase_key)}"
        )
        print("-" * 56)
        if rows:
            print(t("wizard.current"))
            for label, value in rows:
                print(f"  {label}: {value}")
        print()


def prompt_line(prompt: str) -> str:
    try:
        return input(prompt)
    except EOFError as exc:
        raise ConfigError(t("prompt.eof")) from exc


def ask_text(
    label: str,
    default: str | None = None,
    required: bool = False,
    secret: bool = False,
    hint: str | None = None,
    clearable: bool = False,
) -> str:
    """读取一行文本；留空时使用默认值，输入 - 可显式清空。"""
    default_text = str(default).strip() if default is not None else ""
    prompt = label
    if hint:
        prompt += f" ({hint})"
    if default_text and not secret:
        if clearable:
            prompt += " " + t("prompt.default_clear", default=default_text)
        else:
            prompt += f" [{default_text}]"
    prompt += ": "
    while True:
        try:
            raw = getpass.getpass(prompt) if secret else prompt_line(prompt)
        except EOFError as exc:
            raise ConfigError(t("prompt.eof")) from exc
        if clearable and raw.strip() == "-":
            return ""
        value = raw.strip() or default_text
        if value or not required:
            return value
        print(t("prompt.required"))


def ask_int(
    label: str,
    default: int | None = None,
    required: bool = False,
    minimum: int | None = None,
    clearable: bool = False,
) -> int | None:
    while True:
        raw = ask_text(
            label,
            None if default is None else str(default),
            required=required,
            clearable=clearable,
        )
        if not raw:
            return None
        try:
            value = int(raw)
        except ValueError:
            print(t("prompt.int"))
            continue
        if minimum is not None and value < minimum:
            print(t("prompt.min", min=minimum))
            continue
        return value


def ask_yes_no(label: str, default: bool = True) -> bool:
    suffix = "Y/n" if default else "y/N"
    while True:
        raw = prompt_line(f"{label} [{suffix}]: ").strip().lower()
        if not raw:
            return default
        if raw in {"y", "yes", "是", "да", "はい"}:
            return True
        if raw in {"n", "no", "否", "нет", "いいえ"}:
            return False
        print(t("prompt.yesno"))


def ask_choice(label: str, options: list[tuple[str, str]], default: str) -> str:
    keys = [key for key, _ in options]
    default_index = keys.index(default) if default in keys else 0
    print(label)
    for index, (_, description) in enumerate(options, 1):
        mark = t("prompt.default") if index == default_index + 1 else ""
        print(f"  {index}. {description}{mark}")
    while True:
        raw = prompt_line(t("prompt.choose", count=len(options))).strip()
        if not raw:
            return options[default_index][0]
        if raw.isdigit() and 1 <= int(raw) <= len(options):
            return options[int(raw) - 1][0]
        print(t("prompt.choice"))


def ask_existing_file(
    label: str,
    default: str | None = None,
    required: bool = False,
    clearable: bool = False,
) -> str | None:
    while True:
        raw = ask_text(label, default, required=required, clearable=clearable)
        if not raw:
            return None
        path = Path(raw).expanduser()
        if path.is_file():
            return str(path)
        print(t("prompt.file_missing", path=path))


class Discovery:
    """向导内按需读取服务端的 CA / DNS 账户列表；任何失败都降级为手输。"""

    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self._cache: dict[str, list[dict[str, Any]]] = {}
        self._errors: dict[str, str] = {}

    def items(self, path: str) -> list[dict[str, Any]]:
        if path in self._cache:
            return self._cache[path]
        try:
            items = api_list(self.args, path)
        except ClientError as exc:
            self._errors[path] = str(exc)
            items = []
        self._cache[path] = items
        return items

    def show(self, title: str, path: str, rows: list[tuple[str, str]]) -> None:
        if rows:
            print(title)
            for value, label in rows:
                print(f"  {value}: {label}")
        elif path in self._errors:
            print(t("discovery.unavailable", error=self._errors[path]))

    def public_cas(self) -> list[tuple[str, str]]:
        rows: list[tuple[str, str]] = []
        for item in self.items("/ca-configs"):
            value = str(item.get("value") or "")
            if value:
                rows.append((value, str(item.get("label") or value)))
        for item in self.items("/acme-cas"):
            if item.get("id"):
                value = f"acme-ca:{item['id']}"
                rows.append((value, str(item.get("name") or value)))
        self.show(t("discovery.public_ca"), "/ca-configs", rows)
        return rows

    def private_cas(self) -> list[tuple[str, str]]:
        rows = [
            (str(item.get("id")), str(item.get("name") or item.get("common_name") or item.get("id")))
            for item in self.items("/private-cas")
            if item.get("id")
        ]
        self.show(t("discovery.private_ca"), "/private-cas", rows)
        return rows

    def dns_accounts(self) -> list[tuple[str, str]]:
        rows = [
            (str(item.get("id")), str(item.get("name") or item.get("provider") or item.get("id")))
            for item in self.items("/dns-accounts")
            if item.get("id")
        ]
        self.show(t("discovery.dns"), "/dns-accounts", rows)
        return rows


def build_summary_rows(args: argparse.Namespace) -> list[tuple[str, str]]:
    rows: list[tuple[str, str]] = []
    if getattr(args, "base_url", None):
        rows.append((t("summary.url"), args.base_url))
    rows.append(
        (
            t("summary.token"),
            t("summary.set") if getattr(args, "token", None) else t("summary.unset"),
        )
    )
    mode = getattr(args, "_wizard_mode", "")
    if not mode:
        if getattr(args, "request_id", None):
            mode = "resume"
        elif getattr(args, "csr", None):
            mode = "csr"
        elif getattr(args, "private_ca_id", None) or safe_private_ca_value(getattr(args, "ca", "")):
            mode = "private"
        else:
            mode = "new"
    action_labels = {
        "new": t("summary.action.new"),
        "private": t("summary.action.private"),
        "resume": t("summary.action.resume", id=getattr(args, "request_id", "") or "-"),
        "csr": t("summary.action.csr"),
    }
    if mode in action_labels:
        rows.append((t("summary.action"), action_labels[mode]))
    if mode in {"new", "private"}:
        domains = normalize_domains(getattr(args, "domains", []))
        if domains:
            rows.append((t("summary.domains"), ", ".join(domains)))
        if mode == "private":
            private_id = getattr(args, "private_ca_id", None)
            if private_id is None:
                private_id = safe_private_ca_value(getattr(args, "ca", ""))
            if private_id:
                rows.append((t("summary.private_ca"), f"{PRIVATE_CA_PREFIX}{private_id}"))
        else:
            dns_account = getattr(args, "dns_account_id", None)
            if dns_account:
                rows.append((t("summary.dns"), str(dns_account)))
            if getattr(args, "ca", ""):
                rows.append((t("summary.ca"), args.ca))
        if getattr(args, "key_type", None):
            rows.append((t("summary.key_type"), args.key_type))
    elif mode == "resume":
        if getattr(args, "key", None):
            rows.append((t("summary.key"), args.key))
    elif mode == "csr":
        if getattr(args, "csr", None):
            rows.append((t("summary.csr"), args.csr))
        if getattr(args, "key", None):
            rows.append((t("summary.key"), args.key))
        private_id = getattr(args, "private_ca_id", None)
        if private_id is None:
            private_id = safe_private_ca_value(getattr(args, "ca", ""))
        if private_id:
            rows.append((t("summary.private_ca"), f"{PRIVATE_CA_PREFIX}{private_id}"))
        elif getattr(args, "ca", ""):
            rows.append((t("summary.ca"), args.ca))
        domains = normalize_domains(getattr(args, "domains", []))
        if domains:
            rows.append((t("summary.domains"), ", ".join(domains)))
    if getattr(args, "out_dir", None):
        rows.append((t("summary.out_dir"), args.out_dir))
    if getattr(args, "name", None):
        rows.append((t("summary.out_name"), args.name))
    return rows


def interactive_configure(
    args: argparse.Namespace,
    screen: WizardScreen | None = None,
) -> None:
    """逐项询问交互模式所需参数，直接写入 args。"""
    if screen is None:
        screen = WizardScreen(args)
    screen.phase(1, "section.language")
    args.lang = ask_choice(t("language.prompt"), LANGUAGE_OPTIONS, _ACTIVE_LANGUAGE)
    set_language(args.lang)
    screen.phase(2, "section.connection")
    args.base_url = ask_text(t("conn.url"), args.base_url or "http://127.0.0.1:8080")
    if not re.match(r"^https?://", args.base_url, re.IGNORECASE):
        args.base_url = "https://" + args.base_url
    screen.redraw_with_rows(build_summary_rows(args))
    if not args.token and getattr(args, "token_file", ""):
        try:
            args.token = resolve_token(args)
        except ClientError:
            args.token = ""
    token_default = args.token or None
    if token_default:
        reuse_token = ask_yes_no(t("conn.reuse_token"), True)
        if not reuse_token:
            args.token = ask_text(
                t("conn.token"),
                required=True,
                secret=True,
                hint=t("conn.token_hint"),
            )
        screen.redraw_with_rows(build_summary_rows(args))
    else:
        args.token = ask_text(
            t("conn.token"),
            required=True,
            secret=True,
            hint=t("conn.token_hint"),
        )
        screen.redraw_with_rows(build_summary_rows(args))
    if args.base_url.lower().startswith("https://"):
        args.insecure = ask_yes_no(t("conn.insecure"), args.insecure)
        screen.redraw_with_rows(build_summary_rows(args))
        if not args.insecure:
            args.ca_bundle = ask_existing_file(t("conn.ca_bundle"), args.ca_bundle or None)
            screen.redraw_with_rows(build_summary_rows(args))
    args.challenge = "dns01"

    discovery = Discovery(args)
    screen.phase(3, "section.mode")
    default_mode = "resume" if args.request_id else ("csr" if args.csr else "new")
    if not args.request_id and not args.csr and safe_private_ca_value(args.ca) is not None:
        default_mode = "private"
    if not args.request_id and not args.csr and args.private_ca_id:
        default_mode = "private"
    mode = ask_choice(
        t("mode.prompt"),
        [
            ("new", t("mode.new")),
            ("private", t("mode.private")),
            ("resume", t("mode.resume")),
            ("csr", t("mode.existing_csr")),
        ],
        default_mode,
    )
    args._wizard_mode = mode
    screen.redraw_with_rows(build_summary_rows(args))

    key_type_options = [
        ("ecdsa", t("key.ecdsa")),
        ("ecdsa384", "ECDSA P-384"),
        ("ed25519", "Ed25519"),
        ("rsa2048", t("key.rsa2048")),
        ("rsa4096", t("key.rsa4096")),
    ]
    if mode == "new":
        args.request_id = None
        args.csr = None
        args.key = None
        args.private_ca_id = None
        screen.phase(4, "section.request")
        current_domains = ",".join(normalize_domains(args.domains))
        domains = ask_text(t("request.domains"), current_domains or None, required=True)
        args.domains = [domains]
        screen.redraw_with_rows(build_summary_rows(args))
        discovery.dns_accounts()
        args.dns_account_id = ask_int(
            t("request.dns_account"), args.dns_account_id, minimum=1, clearable=True
        )
        screen.redraw_with_rows(build_summary_rows(args))
        discovery.public_cas()
        args.ca = ask_text(t("request.public_ca"), public_ca_default(args.ca))
        screen.redraw_with_rows(build_summary_rows(args))
        args.key_type = ask_choice(
            t("request.key_type"), key_type_options, args.key_type or "ecdsa"
        )
        screen.redraw_with_rows(build_summary_rows(args))
    elif mode == "private":
        args.request_id = None
        args.csr = None
        args.key = None
        args.dns_account_id = None
        screen.phase(4, "section.request")
        current_private = args.private_ca_id
        if current_private is None:
            current_private = safe_private_ca_value(args.ca)
        discovery.private_cas()
        private_id = ask_int(
            t("request.private_ca"), current_private, required=True, minimum=1
        )
        if private_id is None:
            raise ConfigError(t("error.private_ca_id_invalid"))
        args.private_ca_id = private_id
        args.ca = f"{PRIVATE_CA_PREFIX}{private_id}"
        screen.redraw_with_rows(build_summary_rows(args))
        current_domains = ",".join(normalize_domains(args.domains))
        domains = ask_text(t("request.private_domains"), current_domains or None, required=True)
        args.domains = [domains]
        screen.redraw_with_rows(build_summary_rows(args))
        args.key_type = ask_choice(
            t("request.key_type"), key_type_options, args.key_type or "ecdsa"
        )
        screen.redraw_with_rows(build_summary_rows(args))
    elif mode == "resume":
        args.csr = None
        args.domains = []
        screen.phase(4, "section.resume")
        args.request_id = ask_text(t("resume.id"), args.request_id, required=True)
        screen.redraw_with_rows(build_summary_rows(args))
        args.key = ask_existing_file(t("resume.key"), args.key or None, clearable=True)
        screen.redraw_with_rows(build_summary_rows(args))
    else:
        args.request_id = None
        screen.phase(4, "section.csr")
        args.csr = ask_existing_file(t("csr.path"), args.csr, required=True)
        screen.redraw_with_rows(build_summary_rows(args))
        args.key = ask_existing_file(t("csr.key"), args.key or None, clearable=True)
        screen.redraw_with_rows(build_summary_rows(args))
        current_private = args.private_ca_id
        if current_private is None:
            current_private = safe_private_ca_value(args.ca)
        discovery.private_cas()
        private_id = ask_int(
            t("request.private_ca_optional"), current_private, minimum=1, clearable=True
        )
        if private_id:
            args.private_ca_id = private_id
            args.ca = f"{PRIVATE_CA_PREFIX}{private_id}"
            args.dns_account_id = None
        else:
            args.private_ca_id = None
        screen.redraw_with_rows(build_summary_rows(args))
        if not private_id:
            discovery.dns_accounts()
            args.dns_account_id = ask_int(
                t("request.dns_account"), args.dns_account_id, minimum=1, clearable=True
            )
            screen.redraw_with_rows(build_summary_rows(args))
            discovery.public_cas()
            args.ca = ask_text(t("request.public_ca"), public_ca_default(args.ca))
            screen.redraw_with_rows(build_summary_rows(args))
        domains = ask_text(
            t("csr.domains"),
            ",".join(normalize_domains(args.domains)) or None,
            clearable=True,
        )
        args.domains = [domains] if domains else []
        screen.redraw_with_rows(build_summary_rows(args))

    screen.phase(5, "section.output")
    args.out_dir = ask_text(t("output.dir"), args.out_dir or ".")
    screen.redraw_with_rows(build_summary_rows(args))
    args.name = ask_text(t("output.name"), args.name, clearable=True) or None
    screen.redraw_with_rows(build_summary_rows(args))
    args.force = ask_yes_no(t("output.force"), args.force)
    screen.redraw_with_rows(build_summary_rows(args))


def print_interactive_summary(args: argparse.Namespace) -> None:
    print()
    print(t("section.summary"))
    for label, value in build_summary_rows(args):
        print(f"  {label}: {value}")


def interactive_main(args: argparse.Namespace) -> int:
    if not sys.stdin.isatty():
        raise ConfigError(t("app.tty_required"))
    screen = WizardScreen(args)
    print(t("app.title"))
    print(t("app.privacy"))
    while True:
        interactive_configure(args, screen)
        if screen.enabled:
            screen.clear()
            print(t("app.title"))
        print_interactive_summary(args)
        if not ask_yes_no(t("summary.confirm"), True):
            print(t("app.cancelled_period"), file=sys.stderr)
            return 130
        if screen.enabled:
            screen.clear()
        while True:
            try:
                result = execute(args)
            except ClientError as exc:
                screen.redraw_with_rows(build_summary_rows(args))
                print(t("app.error", error=exc), file=sys.stderr)
                options: list[tuple[str, str]] = []
                request_id = exc.request_id or getattr(args, "_last_request_id", None)
                if request_id:
                    options.append(("retry", t("wizard.retry_download")))
                options.append(("edit", t("wizard.edit")))
                if request_id:
                    options.append(("cancel", t("wizard.cancel_request")))
                options.append(("exit", t("wizard.exit")))
                action = ask_choice(t("wizard.failure"), options, options[0][0])
                if action == "retry" and request_id:
                    args.request_id = request_id
                    args.csr = None
                    args.domains = []
                    args.force = True
                    continue
                if action == "cancel" and request_id:
                    if ask_yes_no(t("cancel.confirm", id=request_id), False):
                        try:
                            cancel_request(args, request_id)
                            print(t("cancel.done", id=request_id))
                            return 0
                        except ClientError as cancel_exc:
                            print(t("app.error", error=cancel_exc), file=sys.stderr)
                    return exc.exit_code
                if action == "edit":
                    args.request_id = None
                    break
                return exc.exit_code
            else:
                emit_result(args, result)
                return 0


def should_use_interactive(args: argparse.Namespace) -> bool:
    """终端中缺少关键参数时自动进入向导；命令行完整时保持原行为。"""
    if args.json or args.status or args.cancel or args.list or args.manifest:
        return False
    if args.interactive:
        return True
    if not sys.stdin.isatty():
        return False
    if not args.token and not getattr(args, "token_file", ""):
        return True
    if args.request_id or args.csr:
        return False
    return not normalize_domains(args.domains)


# ---------------------------------------------------------------------------
# 命令行入口
# ---------------------------------------------------------------------------


class LocalizedHelpFormatter(argparse.RawDescriptionHelpFormatter):
    """让 argparse 的 usage 标题也跟随 --lang 切换。"""

    def add_usage(self, usage, actions, groups, prefix=None):
        return super().add_usage(
            usage, actions, groups, prefix=prefix or f"{t('cli.usage')}: "
        )


def language_from_argv(argv: list[str]) -> str | None:
    for index, arg in enumerate(argv):
        if arg == "--lang" and index + 1 < len(argv):
            return argv[index + 1]
        if arg.startswith("--lang="):
            return arg.split("=", 1)[1]
    return None


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    argv = list(sys.argv[1:] if argv is None else argv)
    requested = language_from_argv(argv) or detect_language()
    if requested and normalize_language(requested) is None:
        set_language(DEFAULT_LANGUAGE)
        raise ConfigError(
            t("error.invalid_lang", lang=requested, languages=", ".join(SUPPORTED_LANGUAGES))
        )
    set_language(normalize_language(requested) or DEFAULT_LANGUAGE)
    parser = argparse.ArgumentParser(
        add_help=False,
        description=t("cli.description"),
        formatter_class=LocalizedHelpFormatter,
        epilog=t("cli.epilog"),
    )
    parser._optionals.title = t("cli.options")
    parser.add_argument("-h", "--help", action="help", help=t("cli.help"))
    parser.add_argument("--version", action="version", version=f"%(prog)s {__version__}", help=t("cli.version"))
    parser.add_argument(
        "--base-url",
        default=os.environ.get("LACTSSL_URL", "http://127.0.0.1:8080"),
        help=t("cli.base_url"),
    )
    parser.add_argument(
        "--token",
        default=os.environ.get("LACTSSL_TOKEN", ""),
        help=t("cli.token"),
    )
    parser.add_argument(
        "--token-file",
        default=os.environ.get("LACTSSL_TOKEN_FILE", ""),
        help=t("cli.token_file"),
    )
    parser.add_argument(
        "--domains",
        action="append",
        default=[],
        help=t("cli.domains"),
    )
    parser.add_argument("--csr", help=t("cli.csr"))
    parser.add_argument("--key", help=t("cli.key"))
    parser.add_argument("--request-id", help=t("cli.request_id"))
    parser.add_argument("--ca", default="", help=t("cli.ca"))
    parser.add_argument("--private-ca-id", type=int, help=t("cli.private_ca_id"))
    parser.add_argument("--dns-account-id", type=int, help=t("cli.dns_account_id"))
    parser.add_argument(
        "--challenge",
        default="dns01",
        choices=["dns01"],
        help=t("cli.challenge"),
    )
    parser.add_argument(
        "--key-type",
        default=None,
        choices=list(KEY_TYPES),
        help=t("cli.key_type"),
    )
    parser.add_argument("--out-dir", default=None, help=t("cli.out_dir"))
    parser.add_argument("--name", help=t("cli.name"))
    parser.add_argument("--idempotency-key", help=t("cli.idempotency_key"))
    parser.add_argument("--wait-seconds", type=int, default=0, help=t("cli.wait_seconds"))
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT, help=t("cli.timeout"))
    parser.add_argument(
        "--poll-interval", type=float, default=DEFAULT_POLL_INTERVAL, help=t("cli.poll_interval")
    )
    parser.add_argument(
        "--http-timeout", type=float, default=DEFAULT_HTTP_TIMEOUT, help=t("cli.http_timeout")
    )
    parser.add_argument("--retries", type=int, default=DEFAULT_RETRIES, help=t("cli.retries"))
    parser.add_argument(
        "--auto-retry", type=int, default=DEFAULT_AUTO_RETRY, help=t("cli.auto_retry")
    )
    parser.add_argument("--ca-bundle", help=t("cli.ca_bundle"))
    parser.add_argument("--insecure", action="store_true", help=t("cli.insecure"))
    parser.add_argument("--force", action="store_true", help=t("cli.force"))
    parser.add_argument("--quiet", action="store_true", help=t("cli.quiet"))
    parser.add_argument("--json", action="store_true", help=t("cli.json"))
    parser.add_argument("--status", metavar="REQUEST_ID", help=t("cli.status"))
    parser.add_argument("--cancel", metavar="REQUEST_ID", help=t("cli.cancel"))
    parser.add_argument("--list", action="store_true", help=t("cli.list"))
    parser.add_argument("--yes", action="store_true", help=t("cli.yes"))
    parser.add_argument("--renew-from", metavar="STATE_FILE", help=t("cli.renew_from"))
    parser.add_argument("--reuse-key", action="store_true", help=t("cli.reuse_key"))
    parser.add_argument("--manifest", metavar="FILE", help=t("cli.manifest"))
    parser.add_argument(
        "--on-error",
        default="stop",
        choices=["stop", "continue"],
        help=t("cli.on_error"),
    )
    parser.add_argument("-i", "--interactive", action="store_true", help=t("cli.interactive"))
    parser.add_argument("--lang", help=t("cli.lang"))
    args = parser.parse_args(argv)
    args.lang = set_language(args.lang or requested)
    args.execution_logic = "sequential"
    args._wizard_mode = ""
    args._last_request_id = None
    args._last_state_path = None
    return args


def main() -> int:
    args = parse_args()
    if args.status:
        return command_status(args)
    if args.cancel:
        return command_cancel(args)
    if args.list:
        return command_list(args)
    if args.manifest:
        return run_batch(args)
    if should_use_interactive(args):
        return interactive_main(args)
    result = execute(args)
    emit_result(args, result)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ClientError as exc:
        if "--json" in sys.argv[1:]:
            print(
                json.dumps(
                    {
                        "success": False,
                        "error": str(exc),
                        "exit_code": exc.exit_code,
                        "request_id": getattr(exc, "request_id", None),
                        "state_path": getattr(exc, "state_path", None),
                        "resume_command": getattr(exc, "resume_command", None),
                    },
                    ensure_ascii=False,
                    indent=2,
                )
            )
        else:
            print(t("app.error", error=exc), file=sys.stderr)
        raise SystemExit(exc.exit_code)
    except KeyboardInterrupt:
        print(f"\n{t('app.cancelled')}", file=sys.stderr)
        if "--json" in sys.argv[1:]:
            print(
                json.dumps(
                    {"success": False, "error": t("app.cancelled"), "exit_code": 130},
                    ensure_ascii=False,
                    indent=2,
                )
            )
        raise SystemExit(130)
