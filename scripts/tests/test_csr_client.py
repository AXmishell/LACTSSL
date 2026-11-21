#!/usr/bin/env python3
"""csr_client.py 的单元与集成测试（仅标准库）。"""

from __future__ import annotations

import contextlib
import http.server
import importlib.util
import io
import json
import os
import pathlib
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import unittest
import unittest.mock
import urllib.parse

ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE_PATH = ROOT / "scripts" / "csr_client.py"


def load_module():
    spec = importlib.util.spec_from_file_location("csr_client_under_test", MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load csr_client.py")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


cc = load_module()


def has_openssl() -> bool:
    return shutil.which("openssl") is not None


def quiet_run(func, *args, **kwargs):
    stderr = io.StringIO()
    stdout = io.StringIO()
    with contextlib.redirect_stderr(stderr), contextlib.redirect_stdout(stdout):
        result = func(*args, **kwargs)
    return result, stdout.getvalue(), stderr.getvalue()


@unittest.skipUnless(has_openssl(), "openssl is required")
class MockCAServer:
    """最小的假 LACTSSL 服务端，按 CSR 现场签发证书。"""

    def __init__(self) -> None:
        self.root = pathlib.Path(tempfile.mkdtemp(prefix="lactssl-test-ca-"))
        self.ca_key = self.root / "ca.key"
        self.ca_cert = self.root / "ca.crt"
        subprocess.run(
            [
                "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                "-keyout", str(self.ca_key), "-out", str(self.ca_cert), "-days", "3",
                "-subj", "/CN=LACTSSL Test CA",
            ],
            check=True,
            capture_output=True,
        )
        self.calls: list[tuple[str, str]] = []
        self.requests: dict[str, dict] = {}
        self.fullchains: dict[str, str] = {}
        self.counter = 0
        self.create_fail = 0
        self.cert_fail = 0
        self.redirect = False
        self.status_failed_once = False
        self.retry_calls = 0
        self.retry_available = False
        self.last_payload: dict | None = None
        self.lock = threading.Lock()
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), self._handler())
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.server.server_port}"

    def close(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        shutil.rmtree(self.root, ignore_errors=True)

    def sign(self, csr_text: str, index: int) -> tuple[str, list[str]]:
        csr_path = self.root / f"req-{index}.csr"
        leaf_path = self.root / f"leaf-{index}.crt"
        csr_path.write_text(csr_text, encoding="utf-8")
        subprocess.run(
            [
                "openssl", "x509", "-req", "-in", str(csr_path),
                "-CA", str(self.ca_cert), "-CAkey", str(self.ca_key),
                "-CAcreateserial", "-days", "2", "-copy_extensions", "copyall",
                "-out", str(leaf_path),
            ],
            check=True,
            capture_output=True,
        )
        fullchain = (
            leaf_path.read_text(encoding="utf-8").strip()
            + "\n"
            + self.ca_cert.read_text(encoding="utf-8").strip()
            + "\n"
        )
        info = cc.csr_info(csr_path)
        return fullchain, list(info.dns_names)

    def request_view(self, request_id: str) -> dict:
        item = self.requests[request_id]
        view = {
            "id": request_id,
            "status": item["status"],
            "identifiers": item["identifiers"],
            "primary_domain": item["identifiers"][0],
            "ca": "letsencrypt",
            "challenge": "dns01",
            "attempts": item.get("attempts", 0),
            "retryable": item.get("retryable", False),
            "issuer": "LACTSSL Test CA",
            "serial": "01",
            "not_after": "2099-01-01T00:00:00Z",
            "completed_at": "2026-01-01T00:00:00Z",
            "expires_at": "2099-01-01T00:00:00Z",
            "csr_sha256": item.get("csr_sha256", "x"),
        }
        if item.get("error"):
            view["error"] = item["error"]
        return view

    def _handler(self):
        outer = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):  # noqa: ANN002, ANN003
                return

            def _send(self, code, body, content_type="application/json"):
                data = body.encode("utf-8") if isinstance(body, str) else body
                self.send_response(code)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def do_POST(self):  # noqa: N802
                if outer.redirect:
                    self.send_response(302)
                    self.send_header("Location", "http://127.0.0.1:1/elsewhere")
                    self.end_headers()
                    return
                length = int(self.headers.get("Content-Length") or 0)
                raw = self.rfile.read(length)
                with outer.lock:
                    outer.calls.append(("POST", self.path))
                    if self.path.endswith("/cancel"):
                        request_id = self.path.split("/")[-2]
                        outer.requests[request_id]["status"] = "cancelled"
                        self._send(200, json.dumps({"success": True, "data": outer.request_view(request_id)}))
                        return
                    if self.path.endswith("/retry"):
                        outer.retry_calls += 1
                        request_id = self.path.split("/")[-2]
                        outer.requests[request_id]["status"] = "issued"
                        self._send(202, json.dumps({"success": True, "data": outer.request_view(request_id)}))
                        return
                    if outer.create_fail:
                        self._send(outer.create_fail, json.dumps({"message": "create failed", "code": "test"}))
                        return
                    payload = json.loads(raw.decode("utf-8"))
                    outer.last_payload = payload
                    outer.counter += 1
                    request_id = f"csr_test_{outer.counter}"
                    fullchain, domains = outer.sign(payload["csr"], outer.counter)
                    outer.fullchains[request_id] = fullchain
                    outer.requests[request_id] = {
                        "status": "failed" if outer.retry_available else "issued",
                        "identifiers": domains,
                        "retryable": outer.retry_available,
                        "attempts": 0,
                    }
                    if outer.retry_available:
                        outer.requests[request_id]["error"] = {"code": "temporary", "message": "retry"}
                    self._send(
                        202,
                        json.dumps({"success": True, "data": outer.request_view(request_id)}),
                    )

            def do_GET(self):  # noqa: N802
                if outer.redirect:
                    self.send_response(302)
                    self.send_header("Location", "http://127.0.0.1:1/elsewhere")
                    self.end_headers()
                    return
                with outer.lock:
                    outer.calls.append(("GET", self.path))
                parsed = urllib.parse.urlparse(self.path)
                if parsed.path.endswith("/certificate"):
                    request_id = parsed.path.split("/")[-2]
                    if outer.cert_fail:
                        self._send(outer.cert_fail, "busy", "text/plain")
                        return
                    self._send(200, outer.fullchains[request_id], "application/x-pem-file")
                    return
                if parsed.path.rstrip("/").endswith("/certificate-requests"):
                    self._send(
                        200,
                        json.dumps(
                            {
                                "success": True,
                                "data": [
                                    outer.request_view(request_id)
                                    for request_id in outer.requests
                                ],
                            }
                        ),
                    )
                    return
                request_id = parsed.path.rstrip("/").split("/")[-1]
                if request_id not in outer.requests:
                    self._send(404, json.dumps({"message": "not found", "code": "not_found"}))
                    return
                item = outer.requests[request_id]
                if outer.status_failed_once and item["status"] == "failed":
                    outer.status_failed_once = False
                self._send(200, json.dumps({"success": True, "data": outer.request_view(request_id)}))

        return Handler


class BaseTestCase(unittest.TestCase):
    def setUp(self) -> None:
        cc.set_language("en-US")
        self.tmp = pathlib.Path(tempfile.mkdtemp(prefix="lactssl-test-"))
        self.server = MockCAServer()
        self.addCleanup(self.server.close)
        self.addCleanup(shutil.rmtree, self.tmp, True)

    def args(self, *extra: str):
        return cc.parse_args(
            [
                "--base-url", self.server.url,
                "--token", "test-token",
                "--lang", "en-US",
                "--poll-interval", "0.5",
                "--http-timeout", "5",
                "--retries", "0",
                *extra,
            ]
        )


class LanguageAndDomainTests(unittest.TestCase):
    def test_language_normalization(self) -> None:
        self.assertEqual(cc.normalize_language("en_GB.UTF-8"), "en-US")
        self.assertEqual(cc.normalize_language("zh_Hant_TW"), "zh-TW")
        self.assertIsNone(cc.normalize_language("fr-FR"))
        with self.assertRaises(cc.ConfigError):
            cc.resolve_language("fr-FR")

    def test_help_all_languages(self) -> None:
        for language in cc.SUPPORTED_LANGUAGES:
            completed = subprocess.run(
                [sys.executable, str(MODULE_PATH), "--lang", language, "--help"],
                capture_output=True,
                text=True,
                timeout=30,
            )
            self.assertEqual(completed.returncode, 0, completed.stderr)

    def test_domains_and_names(self) -> None:
        self.assertEqual(
            cc.normalize_domains(["Example.COM., *.Example.com", "example.com"]),
            ["example.com", "*.example.com"],
        )
        self.assertEqual(cc.normalize_domains(["例子.中国"]), ["xn--fsqu00a.xn--fiqs8s"])
        self.assertIsNone(cc.domain_problem("example.com"))
        self.assertIsNotNone(cc.domain_problem("bad_name.example.com"))
        self.assertIsNotNone(cc.domain_problem("-bad.example.com"))
        self.assertIsNotNone(cc.domain_problem("127.0.0.1"))
        name = cc.safe_name("*." + "a" * 400 + ".com")
        self.assertLessEqual(len(name), cc.MAX_SAFE_NAME)
        self.assertTrue(name.startswith("wildcard."))

    def test_certificate_coverage(self) -> None:
        self.assertEqual(
            cc.certificate_covers(["*.example.com"], ["example.com", "a.example.com"]),
            ["example.com"],
        )
        self.assertEqual(cc.certificate_covers(["example.com"], ["example.com"]), [])
        self.assertEqual(
            cc.certificate_covers(["*.example.com"], ["a.b.example.com"]),
            ["a.b.example.com"],
        )


@unittest.skipUnless(has_openssl(), "openssl is required")
class CSRPreflightTests(unittest.TestCase):
    def setUp(self) -> None:
        cc.set_language("en-US")
        self.tmp = pathlib.Path(tempfile.mkdtemp(prefix="lactssl-csr-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)

    def test_long_common_name_is_omitted(self) -> None:
        domain = "a" * 60 + ".example.com"
        key, csr = cc.generate_key_and_csr([domain], "ecdsa", self.tmp)
        info = cc.csr_info(csr)
        self.assertEqual(info.common_name, "")
        self.assertEqual(info.dns_names, [domain])
        self.assertTrue(cc.key_matches_csr(key, csr))

    def test_key_csr_mismatch(self) -> None:
        (self.tmp / "a").mkdir()
        (self.tmp / "b").mkdir()
        key_a, csr_a = cc.generate_key_and_csr(["a.example.com"], "ecdsa", self.tmp / "a")
        key_b, _ = cc.generate_key_and_csr(["b.example.com"], "rsa2048", self.tmp / "b")
        self.assertTrue(cc.key_matches_csr(key_a, csr_a))
        self.assertFalse(cc.key_matches_csr(key_b, csr_a))

    def test_noncanonical_and_extra_identifiers_are_rejected(self) -> None:
        key = self.tmp / "k.pem"
        csr = self.tmp / "c.csr"
        subprocess.run(
            [
                "openssl", "req", "-new", "-nodes", "-subj", "/CN=Example.COM",
                "-addext", "subjectAltName=DNS:Example.COM", "-keyout", str(key),
                "-out", str(csr), "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
            ],
            check=True,
            capture_output=True,
        )
        with self.assertRaises(cc.ConfigError):
            cc.validate_csr_info(cc.csr_info(csr))

        subprocess.run(
            [
                "openssl", "req", "-new", "-nodes", "-subj", "/CN=example.com",
                "-addext", "subjectAltName=DNS:example.com,IP:127.0.0.1",
                "-keyout", str(self.tmp / "k2.pem"), "-out", str(self.tmp / "c2.csr"),
                "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
            ],
            check=True,
            capture_output=True,
        )
        with self.assertRaises(cc.ConfigError):
            cc.validate_csr_info(cc.csr_info(self.tmp / "c2.csr"))

    def test_key_strength_and_curve(self) -> None:
        key = self.tmp / "weak.key"
        csr = self.tmp / "weak.csr"
        subprocess.run(
            [
                "openssl", "req", "-new", "-nodes", "-subj", "/CN=weak.example.com",
                "-addext", "subjectAltName=DNS:weak.example.com",
                "-keyout", str(key), "-out", str(csr), "-newkey", "rsa:1024",
            ],
            check=True,
            capture_output=True,
        )
        with self.assertRaises(cc.ConfigError):
            cc.csr_info(csr)

    def test_pem_validation(self) -> None:
        with self.assertRaises(cc.VerifyError):
            cc.parse_pem_certificates("<html>BEGIN CERTIFICATE</html>", "fullchain")
        with self.assertRaises(cc.VerifyError):
            cc.parse_pem_certificates("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----", "cert")


class ApiBehaviorTests(BaseTestCase):
    def test_issue_flow(self) -> None:
        out = self.tmp / "certs"
        args = self.args("--domains", "example.com,www.example.com", "--out-dir", str(out))
        result, stdout, stderr = quiet_run(cc.execute, args)
        self.assertEqual(result["status"], "issued")
        self.assertEqual(result["request_id"], "csr_test_1")
        self.assertTrue((out / "example.com.crt").is_file())
        self.assertTrue((out / "example.com.fullchain.crt").is_file())
        self.assertTrue((out / "example.com.key").is_file())
        self.assertTrue((out / "example.com.csr").is_file())
        self.assertFalse((out / "example.com.key.new").exists())
        self.assertFalse((out / "example.com.csr.new").exists())
        self.assertEqual((out / "example.com.key").stat().st_mode & 0o777, 0o600)
        state = json.loads((out / "example.com.request.json").read_text(encoding="utf-8"))
        self.assertEqual(state["status"], "issued")
        self.assertEqual(state["request_id"], "csr_test_1")
        self.assertIn("Request state saved", stderr)
        self.assertEqual(stdout, "")

    def test_create_failure_keeps_old_files(self) -> None:
        out = self.tmp / "certs"
        args = self.args("--domains", "example.com", "--out-dir", str(out))
        quiet_run(cc.execute, args)
        old_key = (out / "example.com.key").read_bytes()
        old_cert = (out / "example.com.crt").read_bytes()
        self.server.create_fail = 500
        args = self.args("--domains", "example.com", "--out-dir", str(out), "--force")
        with self.assertRaises(cc.ApiError):
            quiet_run(cc.execute, args)
        self.assertEqual((out / "example.com.key").read_bytes(), old_key)
        self.assertEqual((out / "example.com.crt").read_bytes(), old_cert)
        self.assertTrue((out / "example.com.key.new").is_file())

    def test_resume_promotes_staged_files(self) -> None:
        out = self.tmp / "certs"
        self.server.cert_fail = 503
        args = self.args("--domains", "example.com", "--out-dir", str(out))
        with self.assertRaises(cc.ApiError) as raised:
            quiet_run(cc.execute, args)
        self.assertEqual(raised.exception.request_id, "csr_test_1")
        self.assertTrue((out / "example.com.key.new").is_file())
        initial_state = json.loads((out / "example.com.request.json").read_text(encoding="utf-8"))
        self.server.cert_fail = 0
        resume = self.args("--request-id", "csr_test_1", "--out-dir", str(out))
        result, _stdout, stderr = quiet_run(cc.execute, resume)
        self.assertEqual(result["status"], "issued")
        self.assertTrue((out / "example.com.key").is_file())
        self.assertTrue((out / "example.com.csr").is_file())
        self.assertFalse((out / "example.com.key.new").exists())
        self.assertIn("Using the private key", stderr)
        final_state = json.loads((out / "example.com.request.json").read_text(encoding="utf-8"))
        self.assertEqual(final_state["idempotency_key"], initial_state["idempotency_key"])

    def test_auto_retry(self) -> None:
        out = self.tmp / "certs"
        self.server.retry_available = True
        args = self.args("--domains", "example.com", "--out-dir", str(out), "--auto-retry", "1")
        result, _stdout, _stderr = quiet_run(cc.execute, args)
        self.assertEqual(result["status"], "issued")
        self.assertEqual(self.server.retry_calls, 1)

    def test_redirect_is_rejected_without_leaking_token(self) -> None:
        self.server.redirect = True
        args = self.args("--domains", "example.com", "--out-dir", str(self.tmp / "certs"))
        with self.assertRaises(cc.ClientError) as raised:
            quiet_run(cc.execute, args)
        self.assertIn("redirect", str(raised.exception).lower())
        self.assertFalse(any(call[1].startswith("/elsewhere") for call in self.server.calls))

    def test_plain_get_error_does_not_crash(self) -> None:
        args = self.args("--domains", "example.com", "--out-dir", str(self.tmp / "certs"))
        args.base_url = "http://127.0.0.1:1"
        with self.assertRaises(cc.NetworkError):
            cc.api_request(args, "GET", "/status")

    def test_batch_manifest(self) -> None:
        manifest = self.tmp / "batch.json"
        manifest.write_text(
            json.dumps(
                {
                    "defaults": {"out_dir": str(self.tmp / "certs"), "key_type": "ecdsa"},
                    "certificates": [
                        {"domains": ["a.example.com"]},
                        {"domains": ["b.example.com"], "name": "b"},
                    ],
                }
            ),
            encoding="utf-8",
        )
        args = self.args("--manifest", str(manifest), "--json")
        result, stdout, _stderr = quiet_run(cc.run_batch, args)
        self.assertEqual(result, 0)
        payload = json.loads(stdout)
        self.assertTrue(payload["success"])
        self.assertEqual(payload["succeeded"], 2)
        self.assertTrue((self.tmp / "certs" / "a.example.com.crt").is_file())
        self.assertTrue((self.tmp / "certs" / "b.crt").is_file())

    def test_batch_manifest_continue_on_error(self) -> None:
        manifest = self.tmp / "batch.json"
        manifest.write_text(
            json.dumps(
                {
                    "defaults": {"out_dir": str(self.tmp / "certs")},
                    "certificates": [
                        {"domains": ["bad_name.example.com"]},
                        {"domains": ["good.example.com"]},
                    ],
                }
            ),
            encoding="utf-8",
        )
        args = self.args("--manifest", str(manifest), "--on-error", "continue", "--json")
        code, stdout, _stderr = quiet_run(cc.run_batch, args)
        self.assertEqual(code, 1)
        payload = json.loads(stdout)
        self.assertEqual(payload["succeeded"], 1)
        self.assertEqual(payload["failed"], 1)
        self.assertTrue((self.tmp / "certs" / "good.example.com.crt").is_file())

    def test_status_and_cancel_commands(self) -> None:
        out = self.tmp / "certs"
        args = self.args("--domains", "example.com", "--out-dir", str(out))
        quiet_run(cc.execute, args)
        status = self.args("--status", "csr_test_1")
        code, stdout, _stderr = quiet_run(cc.command_status, status)
        self.assertEqual(code, 0)
        self.assertIn("csr_test_1", stdout)
        cancel = self.args("--cancel", "csr_test_1", "--yes")
        code, stdout, _stderr = quiet_run(cc.command_cancel, cancel)
        self.assertEqual(code, 0)
        self.assertIn("cancelled", stdout.lower())
        listing = self.args("--list", "--json")
        code, stdout, _stderr = quiet_run(cc.command_list, listing)
        self.assertEqual(code, 0)
        payload = json.loads(stdout)
        self.assertEqual(payload["requests"][0]["id"], "csr_test_1")

    def test_renew_and_reuse_key(self) -> None:
        out = self.tmp / "certs"
        first, _stdout, _stderr = quiet_run(
            cc.execute, self.args("--domains", "example.com", "--name", "demo", "--out-dir", str(out))
        )
        old_key = pathlib.Path(first["paths"]["key"]).read_bytes()
        renewed, _stdout, _stderr = quiet_run(
            cc.execute, self.args("--renew-from", first["paths"]["metadata"])
        )
        self.assertFalse(renewed["generated_key"])
        self.assertTrue(renewed["generated_csr"])
        self.assertEqual(pathlib.Path(renewed["paths"]["key"]).read_bytes(), old_key)
        reused, _stdout, _stderr = quiet_run(
            cc.execute,
            self.args(
                "--reuse-key",
                "--key", renewed["paths"]["key"],
                "--domains", "example.com",
                "--name", "demo2",
                "--out-dir", str(out),
            ),
        )
        self.assertEqual(reused["status"], "issued")
        self.assertEqual(pathlib.Path(reused["paths"]["key"]).read_bytes(), old_key)

    def test_existing_csr_flow_and_mismatch(self) -> None:
        csr_dir = self.tmp / "csr"
        csr_dir.mkdir()
        key, csr = cc.generate_key_and_csr(["example.com"], "ecdsa", csr_dir)
        out = self.tmp / "certs"
        args = self.args(
            "--csr", str(csr), "--key", str(key), "--domains", "example.com",
            "--out-dir", str(out),
        )
        result, _stdout, _stderr = quiet_run(cc.execute, args)
        self.assertEqual(result["status"], "issued")
        self.assertTrue((out / "example.com.crt").is_file())
        self.assertEqual(result["paths"]["key"], str(key))
        self.assertEqual(result["paths"]["csr"], str(csr))

        (self.tmp / "other").mkdir()
        other_key, _other_csr = cc.generate_key_and_csr(
            ["other.example.com"], "ecdsa", self.tmp / "other"
        )
        mismatch = self.args(
            "--csr", str(csr), "--key", str(other_key), "--out-dir", str(self.tmp / "other-out")
        )
        with self.assertRaises(cc.ConfigError):
            quiet_run(cc.execute, mismatch)

    def test_private_ca_payload(self) -> None:
        out = self.tmp / "certs"
        args = self.args(
            "--domains", "app.internal", "--private-ca-id", "2", "--out-dir", str(out)
        )
        result, _stdout, _stderr = quiet_run(cc.execute, args)
        self.assertEqual(result["status"], "issued")
        self.assertEqual(self.server.last_payload["private_ca_id"], 2)
        self.assertEqual(self.server.last_payload["ca"], "private-ca:2")
        self.assertEqual(self.server.last_payload["challenge"], "private")
        self.assertNotIn("dns_account_id", self.server.last_payload)

    def test_renew_from_legacy_state(self) -> None:
        out = self.tmp / "certs"
        first, _stdout, _stderr = quiet_run(
            cc.execute, self.args("--domains", "example.com", "--out-dir", str(out))
        )
        meta_path = pathlib.Path(first["paths"]["metadata"])
        old_key = (out / "example.com.key").read_bytes()
        legacy = {
            "request_id": first["request_id"],
            "domains": ["example.com"],
            "ca": "letsencrypt",
            "private_ca_id": None,
        }
        meta_path.write_text(json.dumps(legacy), encoding="utf-8")
        renewed, _stdout, _stderr = quiet_run(
            cc.execute, self.args("--renew-from", str(meta_path))
        )
        self.assertEqual(renewed["status"], "issued")
        self.assertEqual(
            pathlib.Path(renewed["paths"]["key"]).read_bytes(),
            old_key,
        )

    def test_http_retry_and_retry_after(self) -> None:
        responses = [
            (429, {"Retry-After": "0"}, b'{"message":"slow down"}'),
            (502, {}, b"bad gateway"),
            (200, {}, b'{"success": true}'),
        ]
        seen: list[str] = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                return

            def do_GET(self):
                seen.append(self.headers.get("Authorization") or "")
                code, headers, body = responses[min(len(seen) - 1, len(responses) - 1)]
                self.send_response(code)
                for key, value in headers.items():
                    self.send_header(key, value)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        args = cc.parse_args(
            [
                "--base-url", f"http://127.0.0.1:{server.server_port}",
                "--token", "secret", "--lang", "en-US", "--http-timeout", "5",
                "--retries", "2", "--poll-interval", "0.5",
            ]
        )
        result, _stdout, _stderr = quiet_run(cc.api_request, args, "GET", "/status")
        self.assertEqual(result.status, 200)
        self.assertEqual(len(seen), 3)
        self.assertTrue(all(value == "Bearer secret" for value in seen))

    def test_incomplete_read_becomes_network_error(self) -> None:
        listener = socket.socket()
        listener.bind(("127.0.0.1", 0))
        listener.listen(1)
        self.addCleanup(listener.close)

        def serve():
            connection, _ = listener.accept()
            connection.recv(65536)
            connection.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nhello")
            connection.close()

        threading.Thread(target=serve, daemon=True).start()
        args = cc.parse_args(
            [
                "--base-url", f"http://127.0.0.1:{listener.getsockname()[1]}",
                "--token", "t", "--lang", "en-US", "--http-timeout", "5", "--retries", "0",
            ]
        )
        with self.assertRaises(cc.NetworkError):
            cc.api_request(args, "GET", "/x")


@unittest.skipUnless(has_openssl(), "openssl is required")
class CliJsonTests(BaseTestCase):
    def test_json_output_is_clean(self) -> None:
        out = self.tmp / "certs"
        completed = subprocess.run(
            [
                sys.executable, str(MODULE_PATH),
                "--base-url", self.server.url, "--token", "t", "--lang", "en-US",
                "--domains", "example.com", "--out-dir", str(out), "--json",
                "--poll-interval", "0.5", "--http-timeout", "5", "--retries", "0",
            ],
            capture_output=True,
            text=True,
            timeout=60,
        )
        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        self.assertTrue(payload["success"])
        self.assertEqual(payload["status"], "issued")
        self.assertIn("Request state saved", completed.stderr)

    def test_interactive_wizard(self) -> None:
        out = self.tmp / "certs"
        args = self.args()
        answers = [
            "",                     # language: keep en-US
            self.server.url,        # base URL
            "",                     # reuse token
            "1",                    # mode: public CA
            "example.com",          # domains
            "",                     # DNS account: auto
            "",                     # public CA: letsencrypt
            "",                     # key type: ecdsa
            str(out),               # output directory
            "",                     # output name: first domain
            "",                     # force: no
            "",                     # confirm
        ]
        with unittest.mock.patch("builtins.input", side_effect=answers), \
                unittest.mock.patch("sys.stdin.isatty", return_value=True):
            code, stdout, stderr = quiet_run(cc.interactive_main, args)
        self.assertEqual(code, 0, stderr)
        self.assertTrue((out / "example.com.crt").is_file())
        self.assertIn("Done.", stdout)


class I18nTests(unittest.TestCase):
    def test_catalog_parity(self) -> None:
        base = set(cc.MESSAGES["zh-CN"])
        self.assertGreater(len(base), 200)
        for language in cc.SUPPORTED_LANGUAGES:
            self.assertEqual(base, set(cc.MESSAGES[language]), language)
            for key in base:
                placeholders = set(re.findall(r"\{([A-Za-z_][A-Za-z0-9_]*)", cc.MESSAGES[language][key]))
                reference = set(re.findall(r"\{([A-Za-z_][A-Za-z0-9_]*)", cc.MESSAGES["zh-CN"][key]))
                self.assertEqual(reference, placeholders, f"{language}:{key}")


if __name__ == "__main__":
    unittest.main()
