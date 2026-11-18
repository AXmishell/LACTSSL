#!/usr/bin/env python3
"""LACTSSL 客户端 CSR 申请脚本。

客户端在本地生成私钥和 CSR，只把 CSR 提交给 LACTSSL；服务端完成权威 CA
的 DNS-01 验证并签发证书。私钥始终留在客户端。

依赖：Python 3.9+、OpenSSL 1.1.1+（命令行工具）。

示例（首次申请）：
    export LACTSSL_URL="https://ssl.example.com"
    export LACTSSL_TOKEN="lactssl_xxxxxxxxxxxxxxxx"
    python3 scripts/csr_client.py \
        --domains example.com,www.example.com \
        --dns-account-id 3 \
        --ca letsencrypt \
        --out-dir ./certs

续传已有申请（只有申请 ID，没有私钥时只能下载证书）：
    python3 scripts/csr_client.py \
        --request-id csr_xxxxxxxx \
        --out-dir ./certs

使用已有 CSR 和私钥（例如证书续期）：
    python3 scripts/csr_client.py \
        --csr ./certs/example.com.csr \
        --key ./certs/example.com.key \
        --dns-account-id 3 \
        --out-dir ./certs
"""

from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import shutil
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

TERMINAL_STATUSES = {"issued", "failed", "cancelled", "expired"}
DEFAULT_TIMEOUT = 600
DEFAULT_POLL_INTERVAL = 3.0


class ClientError(Exception):
    """客户端可预期错误，main 统一打印并退出。"""


def log(message: str, quiet: bool = False) -> None:
    if not quiet:
        print(message, flush=True)


def normalize_domains(values: list[str]) -> list[str]:
    """拆分逗号/空白分隔的域名并去重。"""
    result: list[str] = []
    for value in values:
        for item in re.split(r"[\s,]+", value.strip()):
            item = item.strip().lower().rstrip(".")
            if item and item not in result:
                result.append(item)
    return result


def safe_name(value: str) -> str:
    value = value.replace("*.", "wildcard.")
    value = re.sub(r"[^A-Za-z0-9._-]+", "_", value).strip("._-")
    return value or "certificate"


def require_openssl() -> str:
    binary = shutil.which("openssl")
    if not binary:
        raise ClientError("未找到 openssl 命令，请先安装 OpenSSL 1.1.1 或更高版本")
    return binary


def run_openssl(args: list[str], stdin: bytes | None = None) -> bytes:
    binary = require_openssl()
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
        return subprocess.run([binary, *args], **kwargs).stdout
    except subprocess.CalledProcessError as exc:
        detail = exc.stderr.decode("utf-8", errors="replace").strip()
        raise ClientError(f"OpenSSL 执行失败: {' '.join(args)}\n{detail}") from exc


def generate_key_and_csr(
    domains: list[str],
    key_type: str,
    work_dir: Path,
) -> tuple[Path, Path]:
    """使用 OpenSSL 在临时目录生成私钥和 CSR。"""
    key_path = work_dir / "client.key"
    csr_path = work_dir / "client.csr"
    common_name = domains[0]
    san = ",".join(f"DNS:{domain}" for domain in domains)

    args = [
        "req",
        "-new",
        "-nodes",
        "-sha256",
        "-subj",
        f"/CN={common_name}",
        "-addext",
        f"subjectAltName={san}",
        "-keyout",
        str(key_path),
        "-out",
        str(csr_path),
    ]
    if key_type == "ecdsa":
        args.extend(["-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256"])
    elif key_type == "rsa2048":
        args.extend(["-newkey", "rsa:2048"])
    elif key_type == "rsa4096":
        args.extend(["-newkey", "rsa:4096"])
    else:
        raise ClientError(f"不支持的密钥类型: {key_type}")

    run_openssl(args)
    if not key_path.exists() or not csr_path.exists():
        raise ClientError("OpenSSL 未生成私钥或 CSR")
    os.chmod(key_path, 0o600)
    return key_path, csr_path


def read_text(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def csr_domains(csr_path: Path) -> list[str]:
    """从已有 CSR 中提取 DNS SAN。"""
    text = run_openssl(["req", "-in", str(csr_path), "-noout", "-text"]).decode(
        "utf-8", errors="replace"
    )
    domains: list[str] = []
    for name in re.findall(r"DNS:([^,\s]+)", text):
        name = name.strip().lower().rstrip(".")
        if name and name not in domains:
            domains.append(name)
    if not domains:
        raise ClientError("CSR 中没有 DNS SAN，无法确定申请域名")
    return domains


def api_base(base_url: str) -> str:
    base = base_url.rstrip("/")
    return base if base.endswith("/api") else base + "/api"


def build_ssl_context(args: argparse.Namespace) -> ssl.SSLContext:
    try:
        context = ssl.create_default_context(cafile=args.ca_bundle or None)
    except OSError as exc:
        raise ClientError(f"无法加载 CA 证书: {exc}") from exc
    if args.insecure:
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
    return context


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
        message = raw.decode("utf-8", errors="replace").strip()[:500] or "请求失败"
    suffix = ""
    if code:
        suffix += f" [{code}]"
    if status == 429:
        retry_after = headers.get("Retry-After") if headers else None
        if retry_after:
            suffix += f"（建议 {retry_after} 秒后重试）"
    return ClientError(f"HTTP {status}: {message}{suffix}")


def api_request(
    args: argparse.Namespace,
    method: str,
    path: str,
    payload: dict[str, Any] | None = None,
) -> tuple[int, bytes, Any]:
    url = api_base(args.base_url) + path
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")

    request = urllib.request.Request(url, data=data, method=method)
    request.add_header("Authorization", f"Bearer {args.token}")
    request.add_header("Accept", "application/json")
    request.add_header("User-Agent", "lactssl-csr-client/1.0")
    if data is not None:
        request.add_header("Content-Type", "application/json")

    try:
        with urllib.request.urlopen(
            request, timeout=args.http_timeout, context=build_ssl_context(args)
        ) as response:
            return response.status, response.read(), response.headers
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read(), exc.headers
    except urllib.error.URLError as exc:
        raise ClientError(f"无法连接 LACTSSL: {exc.reason}") from exc


def create_request(
    args: argparse.Namespace,
    csr_pem: str,
    idempotency_key: str,
) -> dict[str, Any]:
    payload = {
        "csr": csr_pem,
        "ca": args.ca,
        "challenge": args.challenge,
        "dns_account_id": args.dns_account_id,
        "idempotency_key": idempotency_key,
        "wait_seconds": max(0, min(args.wait_seconds, 30)),
    }
    status, raw, headers = api_request(args, "POST", "/certificate-requests", payload)
    if status not in (200, 202):
        raise response_error(status, raw, headers)
    body = decode_json(raw)
    data = body.get("data") if isinstance(body, dict) else None
    if not isinstance(data, dict) or not data.get("id"):
        raise ClientError("服务端返回了无法识别的申请响应")
    return data


def get_request(args: argparse.Namespace, request_id: str) -> dict[str, Any]:
    status, raw, headers = api_request(args, "GET", f"/certificate-requests/{request_id}")
    if status != 200:
        raise response_error(status, raw, headers)
    body = decode_json(raw)
    data = body.get("data") if isinstance(body, dict) else None
    if not isinstance(data, dict):
        raise ClientError("服务端返回了无法识别的申请状态")
    return data


def wait_for_request(
    args: argparse.Namespace,
    initial: dict[str, Any],
) -> dict[str, Any]:
    request = initial
    request_id = str(request["id"])
    deadline = time.time() + args.timeout
    last_status = ""
    while True:
        status = str(request.get("status") or "")
        if status != last_status:
            log(f"申请 {request_id} 状态: {status}", args.quiet)
            last_status = status
        if status == "issued":
            return request
        if status in TERMINAL_STATUSES:
            error = request.get("error")
            detail = ""
            if isinstance(error, dict):
                detail = " ".join(
                    str(value) for value in (error.get("code"), error.get("message")) if value
                )
            retry_hint = "，可调用 /retry 重试" if request.get("retryable") else ""
            raise ClientError(f"申请未成功: {status} {detail}{retry_hint}".strip())

        if time.time() >= deadline:
            raise ClientError(
                f"等待签发超时（{args.timeout} 秒），可稍后用 --request-id {request_id} 续传"
            )
        time.sleep(max(0.5, args.poll_interval))
        request = get_request(args, request_id)


def download_certificate(
    args: argparse.Namespace,
    request_id: str,
    kind: str,
) -> str:
    status, raw, headers = api_request(
        args, "GET", f"/certificate-requests/{request_id}/certificate?type={kind}"
    )
    if status != 200:
        raise response_error(status, raw, headers)
    text = raw.decode("utf-8", errors="replace").strip()
    if "BEGIN CERTIFICATE" not in text:
        raise ClientError(f"下载 {kind} 证书失败：响应不是 PEM 证书")
    return text + "\n"


def certificate_sans(cert_path: Path) -> list[str]:
    text = run_openssl(["x509", "-in", str(cert_path), "-noout", "-text"]).decode(
        "utf-8", errors="replace"
    )
    names: list[str] = []
    for name in re.findall(r"DNS:([^,\s]+)", text):
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


def private_key_matches_cert(key_path: Path, cert_path: Path) -> bool:
    key_der = run_openssl(["pkey", "-in", str(key_path), "-pubout", "-outform", "DER"])
    cert_public = run_openssl(["x509", "-in", str(cert_path), "-pubkey", "-noout"])
    cert_der = run_openssl(["pkey", "-pubin", "-outform", "DER"], stdin=cert_public)
    return key_der == cert_der


def atomic_write(path: Path, content: str, mode: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=".lactssl-", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(content)
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    except Exception:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="使用 LACTSSL 提交客户端 CSR 并下载权威 CA 证书（私钥不离开客户端）",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "--base-url",
        default=os.environ.get("LACTSSL_URL", "http://127.0.0.1:8080"),
        help="LACTSSL 地址，默认读取 LACTSSL_URL 或 http://127.0.0.1:8080",
    )
    parser.add_argument(
        "--token",
        default=os.environ.get("LACTSSL_TOKEN", ""),
        help="API 密钥，默认读取 LACTSSL_TOKEN",
    )
    parser.add_argument(
        "--domains",
        action="append",
        default=[],
        help="申请域名，逗号或空格分隔，可重复传入",
    )
    parser.add_argument("--csr", help="使用已有 CSR 文件，不再生成新私钥")
    parser.add_argument("--key", help="已有私钥文件，用于校验下载证书与私钥匹配")
    parser.add_argument("--request-id", help="续传已有 CSR 申请，不创建新申请")
    parser.add_argument("--ca", default="letsencrypt", help="权威 CA 标识")
    parser.add_argument("--dns-account-id", type=int, help="用于 DNS-01 的 DNS 账户 ID")
    parser.add_argument(
        "--challenge",
        default="dns01",
        choices=["dns01"],
        help="验证方式，目前仅支持 dns01",
    )
    parser.add_argument(
        "--key-type",
        default="ecdsa",
        choices=["ecdsa", "rsa2048", "rsa4096"],
        help="本地私钥类型，默认 ecdsa",
    )
    parser.add_argument("--out-dir", default=".", help="输出目录")
    parser.add_argument("--name", help="输出文件基名，默认使用首个域名")
    parser.add_argument("--idempotency-key", help="幂等键，默认自动生成")
    parser.add_argument(
        "--wait-seconds",
        type=int,
        default=0,
        help="创建申请时服务端同步等待秒数（0-30），0 表示立即返回",
    )
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT, help="整体签发超时秒数")
    parser.add_argument(
        "--poll-interval", type=float, default=DEFAULT_POLL_INTERVAL, help="轮询间隔秒数"
    )
    parser.add_argument("--http-timeout", type=float, default=30.0, help="单次 HTTP 超时秒数")
    parser.add_argument("--ca-bundle", help="自定义 CA 证书文件，用于校验 LACTSSL HTTPS")
    parser.add_argument("--insecure", action="store_true", help="跳过 HTTPS 证书校验（不推荐）")
    parser.add_argument("--force", action="store_true", help="覆盖已存在的输出文件")
    parser.add_argument("--quiet", action="store_true", help="减少进度输出")
    return parser.parse_args()


def ensure_same_domains(expected: list[str], actual: list[str], label: str) -> None:
    expected_set = set(expected)
    actual_set = set(actual)
    if expected_set == actual_set:
        return
    missing = sorted(expected_set - actual_set)
    extra = sorted(actual_set - expected_set)
    raise ClientError(
        f"{label}与申请域名不一致（缺少: {', '.join(missing) or '-'}；多出: {', '.join(extra) or '-'}）"
    )


def main() -> int:
    args = parse_args()
    if not args.token:
        raise ClientError("缺少 API 密钥，请使用 --token 或设置 LACTSSL_TOKEN")
    if args.key and not args.csr and not args.request_id:
        raise ClientError("--key 需要与 --csr 或 --request-id 一起使用")
    if args.insecure:
        print("警告: 已跳过 HTTPS 证书校验，仅建议在本地测试使用", file=sys.stderr)

    out_dir = Path(args.out_dir).expanduser().resolve()
    work_root = Path(tempfile.mkdtemp(prefix="lactssl-csr-"))
    key_path: Path | None = None
    csr_path: Path | None = None
    existing_request: dict[str, Any] | None = None
    try:
        provided_domains = normalize_domains(args.domains)
        if args.key:
            key_path = Path(args.key).expanduser().resolve()
            if not key_path.exists():
                raise ClientError(f"私钥文件不存在: {key_path}")
        if args.csr:
            csr_path = Path(args.csr).expanduser().resolve()
            if not csr_path.exists():
                raise ClientError(f"CSR 文件不存在: {csr_path}")

        if args.request_id:
            existing_request = get_request(args, args.request_id)
            domain_list = [
                str(value).lower().rstrip(".")
                for value in (existing_request.get("identifiers") or [])
                if value
            ]
            if not domain_list and existing_request.get("primary_domain"):
                domain_list = [str(existing_request["primary_domain"]).lower().rstrip(".")]
            if not domain_list:
                raise ClientError("申请中没有域名信息，请使用 --domains 指定")
            if provided_domains:
                ensure_same_domains(domain_list, provided_domains, "--domains")
            if csr_path is not None:
                ensure_same_domains(domain_list, csr_domains(csr_path), "CSR")
        else:
            if args.dns_account_id is None:
                raise ClientError("创建申请时需要 --dns-account-id")
            if csr_path is not None:
                # CSR 是域名声明的事实来源；用户额外传入 --domains 时只做一致性校验。
                domain_list = csr_domains(csr_path)
                if provided_domains:
                    ensure_same_domains(domain_list, provided_domains, "--domains")
            else:
                domain_list = provided_domains
                if not domain_list:
                    raise ClientError("请使用 --domains 指定申请域名")
                key_path, csr_path = generate_key_and_csr(
                    domain_list, args.key_type, work_root
                )

        base_name = safe_name(args.name or domain_list[0])
        cert_path = out_dir / f"{base_name}.crt"
        chain_path = out_dir / f"{base_name}.fullchain.crt"
        meta_path = out_dir / f"{base_name}.request.json"
        generated_key = not args.request_id and not args.csr
        planned = [cert_path, chain_path, meta_path]
        if generated_key:
            planned.extend([out_dir / f"{base_name}.key", out_dir / f"{base_name}.csr"])

        existing = [str(path) for path in planned if path.exists()]
        if existing and not args.force:
            raise ClientError(
                "以下文件已存在，使用 --force 覆盖或更换 --name/--out-dir:\n  "
                + "\n  ".join(existing)
            )

        # 先把本地生成的私钥和 CSR 落盘，避免申请成功后网络中断导致私钥丢失。
        if generated_key:
            if key_path is None or csr_path is None:
                raise ClientError("本地私钥或 CSR 生成失败")
            key_target = out_dir / f"{base_name}.key"
            csr_target = out_dir / f"{base_name}.csr"
            atomic_write(key_target, read_text(key_path), 0o600)
            atomic_write(csr_target, read_text(csr_path), 0o644)
            key_path = key_target
            csr_path = csr_target
            log(f"已生成本地私钥: {key_target}", args.quiet)
            log(f"已生成本地 CSR: {csr_target}", args.quiet)

        if existing_request is not None:
            request = existing_request
        else:
            if csr_path is None:
                raise ClientError("缺少 CSR")
            idempotency_key = (
                args.idempotency_key
                or f"{base_name}-{int(time.time())}-{secrets.token_hex(4)}"
            )
            request = create_request(args, read_text(csr_path), idempotency_key)
            log(f"已创建申请: {request['id']}", args.quiet)

        request = wait_for_request(args, request)
        request_id = str(request["id"])
        log("签发成功，正在下载证书", args.quiet)

        fullchain_pem = download_certificate(args, request_id, "fullchain")
        cert_pem = download_certificate(args, request_id, "cert")

        # 校验下载内容与本地私钥/请求域名一致。
        leaf_tmp = work_root / "leaf.crt"
        leaf_tmp.write_text(cert_pem, encoding="utf-8")
        sans = certificate_sans(leaf_tmp)
        missing = certificate_covers(sans, domain_list)
        if missing:
            raise ClientError("证书未覆盖请求域名: " + ", ".join(missing))
        if key_path is not None:
            if not private_key_matches_cert(key_path, leaf_tmp):
                raise ClientError("下载证书与本地私钥不匹配，已拒绝保存")
        else:
            log("提示: 未提供本地私钥，跳过证书与私钥匹配校验", args.quiet)

        atomic_write(cert_path, cert_pem, 0o644)
        atomic_write(chain_path, fullchain_pem, 0o644)
        metadata = {
            "request_id": request_id,
            "certificate_id": request.get("certificate_id"),
            "domains": domain_list,
            "ca": request.get("ca") or args.ca,
            "issuer": request.get("issuer"),
            "serial": request.get("serial"),
            "not_before": request.get("not_before"),
            "not_after": request.get("not_after"),
            "created_at": request.get("created_at"),
            "completed_at": request.get("completed_at"),
        }
        atomic_write(meta_path, json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", 0o644)

        print("完成。")
        print(f"  证书:   {cert_path}")
        print(f"  完整链: {chain_path}")
        if key_path is not None and generated_key:
            print(f"  私钥:   {key_path}")
        if csr_path is not None and generated_key:
            print(f"  CSR:    {csr_path}")
        print(f"  元数据: {meta_path}")
        return 0
    finally:
        shutil.rmtree(work_root, ignore_errors=True)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ClientError as exc:
        print(f"错误: {exc}", file=sys.stderr)
        raise SystemExit(1)
    except KeyboardInterrupt:
        print("\n已取消", file=sys.stderr)
        raise SystemExit(130)
