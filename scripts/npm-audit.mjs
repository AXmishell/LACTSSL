#!/usr/bin/env node
// 前端依赖安全审计门禁。
// 默认拦截 high / critical 漏洞；仅对“上游暂无修复版本”且已登记豁免的漏洞放行。
// 新增豁免必须写明原因和复查日期（expires），到期后自动恢复为失败。
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../web");

// key 为 GitHub Advisory ID（GHSA）。
const ALLOWLIST = new Map([
  [
    "ghsa-fc5q-39xc-vjpf",
    {
      package: "picomatch",
      cve: "CVE-2026-112366",
      reason:
        "仅经 vite / vue-tsc 等构建期工具链引入，不进入前端产物；上游 picomatch <= 4.0.7 暂无修复版本。",
      expires: "2026-12-31",
    },
  ],
]);

const GATED_SEVERITIES = new Set(["high", "critical"]);

function runAudit() {
  try {
    return execFileSync("npm", ["audit", "--json"], {
      cwd: webDir,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
  } catch (err) {
    // npm audit 发现漏洞时以非零码退出，但 JSON 仍然有效。
    if (err.stdout) return err.stdout;
    throw err;
  }
}

function ghsaFromUrl(url) {
  const match = /GHSA-[a-z0-9-]+/i.exec(url ?? "");
  return match ? match[0].toLowerCase() : null;
}

const raw = runAudit();
let report;
try {
  report = JSON.parse(raw);
} catch {
  console.error("✗ 无法解析 npm audit 输出：");
  console.error(raw);
  process.exit(1);
}

if (report.error) {
  console.error(`✗ npm audit 执行失败：${report.error.summary ?? JSON.stringify(report.error)}`);
  process.exit(1);
}

// 按 advisory 去重：同一个漏洞会通过多个依赖链重复出现。
const advisories = new Map();
for (const [pkgName, vuln] of Object.entries(report.vulnerabilities ?? {})) {
  for (const via of vuln.via ?? []) {
    if (typeof via !== "object") continue;
    const ghsa = ghsaFromUrl(via.url) ?? `source-${via.source}`;
    const entry = advisories.get(ghsa) ?? {
      ghsa,
      title: via.title,
      url: via.url,
      severity: via.severity,
      packages: new Set(),
    };
    entry.packages.add(pkgName);
    advisories.set(ghsa, entry);
  }
}

const today = new Date().toISOString().slice(0, 10);
const blocking = [];
const waived = [];
const expired = [];

for (const advisory of advisories.values()) {
  if (!GATED_SEVERITIES.has(advisory.severity)) continue;
  const waiver = ALLOWLIST.get(advisory.ghsa);
  if (!waiver) {
    blocking.push(advisory);
    continue;
  }
  if (waiver.expires < today) {
    expired.push({ ...advisory, waiver });
    continue;
  }
  waived.push({ ...advisory, waiver });
}

const packageList = (advisory) => [...advisory.packages].sort().join(", ");

if (waived.length > 0) {
  console.log("已豁免的高危漏洞（需在上游修复后移除）：");
  for (const { ghsa, title, waiver } of waived) {
    console.log(`  - ${ghsa} (${waiver.cve}, ${waiver.package}): ${title}`);
    console.log(`    原因: ${waiver.reason}`);
    console.log(`    复查期限: ${waiver.expires}`);
  }
}

if (expired.length > 0) {
  console.error("✗ 以下豁免已过期，请确认上游是否已修复并重新评估：");
  for (const { ghsa, waiver } of expired) {
    console.error(`  - ${ghsa} (${waiver.cve}, ${waiver.package})，已于 ${waiver.expires} 过期`);
  }
}

if (blocking.length > 0) {
  console.error("✗ 发现未豁免的高危/严重漏洞：");
  for (const { ghsa, title, url, severity, packages } of blocking) {
    console.error(`  - [${severity}] ${ghsa} ${title}`);
    console.error(`    依赖: ${packageList({ packages })}`);
    console.error(`    详情: ${url}`);
  }
}

if (expired.length > 0 || blocking.length > 0) {
  process.exit(1);
}

console.log("✓ 前端依赖审计通过（无未豁免的 high / critical 漏洞）");
