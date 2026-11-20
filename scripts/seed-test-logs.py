#!/usr/bin/env python3
"""向本地 SQLite 写入测试日志，便于验证日志页面的分页、滚动与筛选。

日志直接写入 logs 表（操作日志 category=operation，系统日志 category=runtime），
时间戳使用 modernc.org/sqlite 可识别的 Go time.String() 格式。

用法：
    python3 scripts/seed-test-logs.py                    # 默认写 1000 条操作 + 2000 条系统日志
    python3 scripts/seed-test-logs.py --operation 500 --runtime 500
    python3 scripts/seed-test-logs.py --db /path/to/lactssl.db --days 2
"""

from __future__ import annotations

import argparse
import json
import random
import re
import sqlite3
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_DB = ROOT / "data" / "lactssl.db"

TOKEN_RE = re.compile(r"\{([a-z_]+)\}")

SOURCE_MESSAGES = {
    "task": [
        "任务「{name}」执行完成，耗时 {sec} 秒",
        "任务「{name}」已重新排队，等待下次调度",
        "任务「{name}」更新证书成功，共 {count} 个域名",
        "任务「{name}」跳过：证书仍在有效期内",
    ],
    "deploy": [
        "部署到节点 {host} 成功，耗时 {sec} 秒",
        "部署到节点 {host} 失败：连接超时",
        "已上传证书到 {host}，大小 {size} KB",
        "部署脚本在 {host} 返回非零退出码 {code}",
    ],
    "monitor": [
        "证书「{name}」剩余 {days} 天到期",
        "证书「{name}」已续期",
        "证书监控检查完成，共检查 {count} 个证书",
        "证书「{name}」解析失败：DNS 记录不存在",
    ],
    "auth": [
        "用户 {email} 登录成功",
        "用户 {email} 登录失败：密码错误",
        "用户 {email} 修改了密码",
        "已为 {email} 创建 API 密钥",
    ],
    "dns": [
        "DNS 账户「{name}」验证成功",
        "DNS 账户「{name}」验证失败：凭据无效",
        "已为域名 {domain} 创建 TXT 记录",
        "已清理域名 {domain} 的临时 TXT 记录",
    ],
    "ca": [
        "私有 CA「{name}」签发证书成功",
        "私有 CA「{name}」已吊销证书 {serial}",
        "私有 CA「{name}」根证书即将过期",
        "已导入 CA 证书链，包含 {count} 个证书",
    ],
    "settings": [
        "已更新系统设置：{key}",
        "已导出配置备份",
        "已启用日志归档功能",
        "已清理过期日志 {count} 条",
    ],
    "notify": [
        "通知渠道「{name}」测试发送成功",
        "通知渠道「{name}」发送失败：{reason}",
        "已发送证书到期提醒，共 {count} 个收件人",
        "通知渠道「{name}」已启用",
    ],
    "system": [
        "后台调度器已启动",
        "数据库维护任务完成，耗时 {sec} 秒",
        "已应用数据库迁移 {version}",
        "服务配置已重新加载",
    ],
    "acme": [
        "ACME 账户注册成功：{email}",
        "ACME 订单创建成功，包含 {count} 个域名",
        "ACME 挑战验证失败：{reason}",
        "ACME 证书下载完成，序列号 {serial}",
    ],
    "cert": [
        "证书「{name}」导入成功",
        "证书「{name}」即将过期，剩余 {days} 天",
        "证书「{name}」已删除",
        "证书链校验通过：{name}",
    ],
    "csr": [
        "CSR「{name}」已生成",
        "CSR「{name}」签名成功",
        "CSR「{name}」私钥已下载",
        "CSR「{name}」已删除",
    ],
    "apikey": [
        "API 密钥「{name}」已创建",
        "API 密钥「{name}」已吊销",
        "API 密钥「{name}」被拒绝：权限不足",
        "API 密钥「{name}」最后使用于 {host}",
    ],
}

RUNTIME_MESSAGES = {
    "app": [
        ("info", "后台调度器已启动"),
        ("info", "已加载配置，数据目录 {dir}"),
        ("warn", "未配置通知渠道，证书到期提醒不会发送"),
        ("debug", "缓存预热完成，共 {count} 项"),
    ],
    "main": [
        ("info", "进程启动，版本 {version}"),
        ("info", "HTTP 服务开始监听 {addr}"),
        ("warn", "数据库连接池接近上限，当前 {count} 个连接"),
        ("error", "数据库维护失败：{reason}"),
    ],
    "http": [
        ("info", "收到请求 {method} {path}"),
        ("info", "请求处理完成，状态码 {code}，耗时 {ms}ms"),
        ("warn", "请求处理超时：{path}"),
        ("error", "请求处理失败：{reason}"),
    ],
    "service": [
        ("info", "任务队列开始处理，待处理 {count} 个"),
        ("info", "证书续期任务完成，共 {count} 个证书"),
        ("warn", "任务「{name}」重试 {count} 次后仍然失败"),
        ("error", "任务「{name}」执行异常：{reason}"),
    ],
    "acme": [
        ("info", "ACME 目录加载成功：{url}"),
        ("info", "订单 {id} 验证通过"),
        ("warn", "ACME 响应较慢，耗时 {ms}ms"),
        ("error", "ACME 挑战失败：{reason}"),
    ],
}

FIELDS = [
    {"request_id": "req-{rand}", "duration_ms": 12, "status": 200},
    {"remote_addr": "10.0.0.{n}", "user_agent": "Mozilla/5.0", "method": "POST"},
    {"task_id": 42, "attempt": 2, "next_run": "2026-11-20T10:00:00Z"},
    {"domain": "example.com", "issuer": "Let's Encrypt", "days_left": 12},
    {"error": "connection reset by peer", "retryable": True},
]

LEVELS = ["info", "success", "warning", "error"]
RUNTIME_LEVELS = ["debug", "info", "warn", "error"]


def render(template: str, rng: random.Random) -> str:
    values = {
        "name": rng.choice(["主站证书", "API 证书", "内部服务", "监控面板", "测试任务"]),
        "sec": rng.randint(1, 300),
        "count": rng.randint(1, 500),
        "host": rng.choice(["10.0.0.11", "10.0.0.12", "node-a", "node-b"]),
        "size": rng.randint(2, 128),
        "code": rng.choice([0, 1, 2, 127, 255]),
        "days": rng.randint(1, 60),
        "email": rng.choice(["admin@example.com", "ops@example.com", "dev@example.com"]),
        "domain": rng.choice(["example.com", "api.example.com", "*.example.org"]),
        "serial": "%032X" % rng.getrandbits(128),
        "key": rng.choice(["log.level", "server.port", "notify.enabled"]),
        "reason": rng.choice(["连接超时", "凭据无效", "权限不足", "响应格式错误"]),
        "version": rng.choice(["v1.4.0", "v1.4.1", "20261120"]),
        "dir": "/var/lib/lactssl",
        "addr": ":8080",
        "method": rng.choice(["GET", "POST", "PUT", "DELETE"]),
        "path": rng.choice(["/api/logs", "/api/certificates", "/api/tasks", "/login"]),
        "ms": rng.randint(3, 2500),
        "url": "https://acme-v02.api.letsencrypt.org/directory",
        "id": "%016X" % rng.getrandbits(64),
        "rand": "%08x" % rng.getrandbits(32),
        "n": rng.randint(2, 254),
    }
    return TOKEN_RE.sub(lambda match: str(values[match.group(1)]), template)


def go_time(dt: datetime) -> str:
    """生成 modernc.org/sqlite 能解析的 Go time.String() 格式。"""
    dt = dt.astimezone(timezone.utc)
    return dt.strftime("%Y-%m-%d %H:%M:%S.%f") + "000 +0000 UTC"


def seed(db_path: Path, operation: int, runtime: int, days: int) -> tuple[int, int]:
    rng = random.Random(20261120)
    now = datetime.now(timezone.utc)
    operation_rows = []
    runtime_rows = []

    for _ in range(operation):
        source = rng.choice(list(SOURCE_MESSAGES))
        message = render(rng.choice(SOURCE_MESSAGES[source]), rng)
        created = now - timedelta(seconds=rng.randint(0, max(days, 1) * 86400))
        operation_rows.append(
            (
                "operation",
                source,
                source,
                rng.randint(0, 200),
                rng.choice(LEVELS),
                message,
                "",
                "",
                go_time(created),
            )
        )

    for _ in range(runtime):
        component = rng.choice(list(RUNTIME_MESSAGES))
        level, template = rng.choice(RUNTIME_MESSAGES[component])
        message = render(template, rng)
        fields = render(json.dumps(rng.choice(FIELDS), ensure_ascii=False), rng)
        created = now - timedelta(seconds=rng.randint(0, max(days, 1) * 86400))
        runtime_rows.append(
            (
                "runtime",
                component,
                "",
                0,
                level,
                message,
                fields,
                "",
                go_time(created),
            )
        )

    if not db_path.exists():
        raise SystemExit(f"数据库不存在：{db_path}")

    conn = sqlite3.connect(db_path, timeout=10)
    try:
        conn.execute("PRAGMA busy_timeout = 10000")
        with conn:
            conn.executemany(
                """INSERT INTO logs(category,source,ref_type,ref_id,level,message,fields,request_id,created_at)
                   VALUES(?,?,?,?,?,?,?,?,?)""",
                operation_rows + runtime_rows,
            )
    finally:
        conn.close()
    return len(operation_rows), len(runtime_rows)


def main() -> int:
    parser = argparse.ArgumentParser(description="向 LACTSSL 数据库写入测试日志")
    parser.add_argument("--db", type=Path, default=DEFAULT_DB, help=f"SQLite 路径（默认 {DEFAULT_DB}）")
    parser.add_argument("--operation", type=int, default=1000, help="操作日志条数（默认 1000）")
    parser.add_argument("--runtime", type=int, default=2000, help="系统日志条数（默认 2000）")
    parser.add_argument("--days", type=int, default=3, help="时间戳分散在过去多少天（默认 3）")
    args = parser.parse_args()

    if args.operation < 0 or args.runtime < 0 or args.days < 1:
        parser.error("条数不能为负，--days 至少为 1")

    try:
        op, rt = seed(args.db, args.operation, args.runtime, args.days)
    except sqlite3.Error as exc:
        print(f"写入失败：{exc}", file=sys.stderr)
        return 1

    print(f"已写入 {op} 条操作日志、{rt} 条系统日志到 {args.db}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
