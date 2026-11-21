#!/usr/bin/env python3
"""校验 scripts/csr_client.py 的多语言资源。

检查项：
1. 所有支持语言的键集合与 zh-CN 完全一致；
2. 同一键的占位符集合一致；
3. 不存在空文案；
4. 代码中通过 t("...") 静态引用的键都存在；
5. MESSAGES 的语言列表与 SUPPORTED_LANGUAGES 一致。
"""

from __future__ import annotations

import importlib.util
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "scripts" / "csr_client.py"
PLACEHOLDER = re.compile(r"\{([A-Za-z_][A-Za-z0-9_]*)")


def load_module():
    spec = importlib.util.spec_from_file_location("csr_client_i18n_check", MODULE_PATH)
    if spec is None or spec.loader is None:
        raise SystemExit(f"无法加载 {MODULE_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def main() -> int:
    module = load_module()
    catalogs = module.MESSAGES
    languages = list(module.SUPPORTED_LANGUAGES)
    reference_language = "zh-CN"
    reference = catalogs[reference_language]
    errors: list[str] = []

    if set(catalogs) != set(languages):
        errors.append(
            f"语言列表不一致: MESSAGES={sorted(catalogs)} SUPPORTED_LANGUAGES={sorted(languages)}"
        )

    for language in languages:
        catalog = catalogs.get(language, {})
        missing = sorted(set(reference) - set(catalog))
        extra = sorted(set(catalog) - set(reference))
        if missing:
            errors.append(f"{language}: 缺少键 {', '.join(missing)}")
        if extra:
            errors.append(f"{language}: 多余键 {', '.join(extra)}")
        for key, value in catalog.items():
            if not str(value).strip():
                errors.append(f"{language}:{key}: 文案为空")
            expected = set(PLACEHOLDER.findall(reference.get(key, "")))
            actual = set(PLACEHOLDER.findall(str(value)))
            if expected != actual:
                errors.append(
                    f"{language}:{key}: 占位符不一致 {sorted(expected)} != {sorted(actual)}"
                )

    source = MODULE_PATH.read_text(encoding="utf-8")
    used = set(re.findall(r'(?<![A-Za-z0-9_])t\(\s*["\']([^"\']+)["\']', source))
    undefined = sorted(used - set(reference))
    if undefined:
        errors.append(f"代码引用了未定义的文案键: {', '.join(undefined)}")

    if errors:
        for error in errors:
            print(f"✗ {error}", file=sys.stderr)
        return 1
    print(
        f"✓ CSR 客户端文案检查通过：{len(languages)} 种语言，{len(reference)} 个键，"
        f"静态引用 {len(used)} 个"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
