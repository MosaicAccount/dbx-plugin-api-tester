#!/usr/bin/env node
/**
 * 为已发布的 Release 补齐缺失的 `.artifact.json` 资产。
 *
 * 上游复用 workflow（`t8y2/dbx/.../plugin-release-reusable.yml`）只上传 `.dbxp` 与
 * `release-candidates.json`，`.artifact.json` 仅作为合并输入读取，因此 Release 上会缺
 * 每个候选包对应的元数据。这个脚本在发布 workflow 结束后补传缺失项：
 *
 * - 以 `release-candidates.json` 为准，逐项核对包是否在场、大小与 SHA-256 是否与
 *   已上传的 `.dbxp` 字节一致；不一致直接失败，绝不凭元数据"信任"写入。
 * - 已有的 `.artifact.json` 会被读取校验（四个字段），不覆盖、不重写。
 * - 只上传缺失文件，且不使用 `--clobber`；上传后回读资产列表复核大小与 digest。
 * - 读操作与上传遇到 EOF/5xx 等瞬时错误会自动重试，上传重试前先回读，避免重复上传。
 *
 * 用法：
 *
 *   node scripts/backfill-release-sidecars.mjs --tag v0.1.0 [--repo owner/name] [--dry-run]
 *
 * 需要已登录的 `gh`（CI 里用 `GH_TOKEN`），且 token 有 `contents: write`。
 */

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const SIDECAR_SUFFIX = ".artifact.json";
const CANDIDATES_NAME = "release-candidates.json";
const FIELDS = ["target", "url", "sha256", "size"];

class BackfillError extends Error {}

function usage() {
  return "usage: backfill-release-sidecars.mjs --tag <release-tag> [--repo <owner/name>] [--dry-run]";
}

/**
 * 生成 `.artifact.json` 的字节内容。
 *
 * 字段顺序与 `dbx-plugin package` 的产物一致（target/url/sha256/size，两空格缩进、
 * 结尾换行），这样补传的文件与构建产物完全相同，diff 时不会出现无意义的格式差异。
 */
export function sidecarBody(artifact) {
  const body = { target: artifact.target, url: artifact.url, sha256: artifact.sha256, size: artifact.size };
  return `${JSON.stringify(body, null, 2)}\n`;
}

/** 校验已有 `.artifact.json` 的内容是否与候选元数据一致。 */
export function verifySidecar(text, artifact) {
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch (error) {
    return `不是合法 JSON（${error.message}）`;
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return "顶层不是对象";
  for (const field of FIELDS) {
    if (parsed[field] !== artifact[field]) {
      return `${field} 期望 ${JSON.stringify(artifact[field])}，实际 ${JSON.stringify(parsed[field])}`;
    }
  }
  return "";
}

function parseArgs(argv) {
  const options = { tag: process.env.TAG ?? "", repo: process.env.GH_REPO ?? "", dryRun: false };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--tag") {
      options.tag = argv[++index] ?? "";
    } else if (arg === "--repo") {
      options.repo = argv[++index] ?? "";
    } else if (arg === "--dry-run") {
      options.dryRun = true;
    } else if (arg === "-h" || arg === "--help") {
      console.log(usage());
      process.exit(0);
    } else {
      throw new BackfillError(`未知参数 ${arg}\n${usage()}`);
    }
  }
  if (!options.tag) throw new BackfillError(`缺少 --tag\n${usage()}`);
  return options;
}

/** 同步 sleep，避免为一次重试引入异步流程。 */
function sleepSync(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

/** 网络抖动/网关错误：值得重试；权限、404 之类不重试。 */
function isTransient(error) {
  const text = `${error.stderr ?? ""} ${error.message ?? ""}`;
  return /EOF|ETIMEDOUT|ECONNRESET|EAI_AGAIN|socket hang up|TLS handshake timeout|\b(502|503|504)\b|rate limit|secondary rate/i.test(
    text,
  );
}

function summarize(error) {
  // 不把带签名的下载 URL 打进 CI 日志，只留错误主干。
  const detail = (`${error.stderr ?? ""}`.trim() || error.message).replace(/https?:\/\/\S+/g, "<url>");
  return detail.length > 400 ? `${detail.slice(0, 400)}…` : detail;
}

/**
 * 跑 gh，stdout 按需返回文本或 Buffer。
 *
 * 读操作遇到网络抖动会自动重试；写操作（上传）由调用方自己处理，避免重复上传。
 */
function gh(args, encoding = "utf8", attempts = 3) {
  for (let attempt = 1; ; attempt += 1) {
    try {
      return execFileSync("gh", args, {
        encoding,
        stdio: ["ignore", "pipe", "pipe"],
        maxBuffer: 64 * 1024 * 1024,
      });
    } catch (error) {
      if (attempt < attempts && isTransient(error)) {
        console.log(`gh ${args.slice(0, 2).join(" ")} 第 ${attempt} 次失败，重试：${summarize(error)}`);
        sleepSync(2000 * attempt);
        continue;
      }
      throw new BackfillError(`gh ${args.slice(0, 2).join(" ")} 失败：${summarize(error)}`);
    }
  }
}

function fail(message) {
  throw new BackfillError(message);
}

function loadAssets(repo, tag) {
  const release = JSON.parse(gh(["api", `repos/${repo}/releases/tags/${tag}`]));
  return release.assets ?? [];
}

function loadCandidates(repo, assets) {
  const asset = assets.find((item) => item.name === CANDIDATES_NAME);
  if (!asset) fail(`Release 资产里没有 ${CANDIDATES_NAME}，无法确认候选包清单`);
  const raw = gh(["api", "-H", "Accept: application/octet-stream", `repos/${repo}/releases/assets/${asset.id}`]);
  let candidates;
  try {
    candidates = JSON.parse(raw);
  } catch (error) {
    fail(`${CANDIDATES_NAME} 不是合法 JSON（${error.message}）`);
  }
  const artifacts = candidates?.artifacts;
  if (!Array.isArray(artifacts) || artifacts.length === 0) fail(`${CANDIDATES_NAME} 里没有 artifacts`);
  const seen = new Set();
  for (const artifact of artifacts) {
    for (const field of FIELDS) {
      if (artifact?.[field] === undefined || artifact[field] === null) {
        fail(`${CANDIDATES_NAME} 的条目缺字段 ${field}：${JSON.stringify(artifact)}`);
      }
    }
    if (!/^[0-9a-f]{64}$/.test(String(artifact.sha256).toLowerCase())) {
      fail(`${CANDIDATES_NAME} 的 sha256 格式不对：${artifact.sha256}`);
    }
    if (!Number.isInteger(artifact.size) || artifact.size <= 0) {
      fail(`${CANDIDATES_NAME} 的 size 不是正整数：${JSON.stringify(artifact.size)}`);
    }
    if (seen.has(artifact.url)) fail(`${CANDIDATES_NAME} 里 url 重复：${artifact.url}`);
    seen.add(artifact.url);
  }
  return artifacts;
}

/** 资产字节的 SHA-256：优先用 API 的 digest，缺失时下载后本地计算。 */
function assetSha256(repo, tag, asset, workdir) {
  const digest = typeof asset.digest === "string" ? asset.digest : "";
  if (digest.startsWith("sha256:")) return digest.slice("sha256:".length).toLowerCase();
  const dir = mkdtempSync(join(workdir, "hash-"));
  gh(["release", "download", tag, "--repo", repo, "--pattern", asset.name, "--dir", dir, "--clobber"], "buffer");
  return createHash("sha256").update(readFileSync(join(dir, asset.name))).digest("hex");
}

function readAssetText(repo, asset) {
  return gh(["api", "-H", "Accept: application/octet-stream", `repos/${repo}/releases/assets/${asset.id}`]);
}

function uploadSidecars(repo, tag, workdir, pending) {
  const dir = mkdtempSync(join(workdir, "upload-"));
  const paths = [];
  for (const item of pending) {
    const path = join(dir, item.name);
    writeFileSync(path, item.body);
    paths.push(path);
  }

  // 上传不是幂等操作，失败后先回读资产列表，只重传仍然缺失的那些。
  for (let attempt = 1; ; attempt += 1) {
    try {
      gh(["release", "upload", tag, "--repo", repo, ...paths], "buffer", 1);
      return;
    } catch (error) {
      const uploaded = new Set(loadAssets(repo, tag).map((item) => item.name));
      const stillMissing = pending.filter((item) => !uploaded.has(item.name));
      if (stillMissing.length === 0) return;
      if (attempt >= 3 || !isTransient(error)) throw error;
      console.log(`上传第 ${attempt} 次失败，${stillMissing.length} 个仍缺失，重试：${summarize(error)}`);
      sleepSync(3000 * attempt);
    }
  }
}

function main() {
  const options = parseArgs(process.argv.slice(2));
  const repo = options.repo || JSON.parse(gh(["repo", "view", "--json", "nameWithOwner"])).nameWithOwner;
  const tag = options.tag;
  const workdir = mkdtempSync(join(tmpdir(), "dbx-sidecars-"));

  const assets = loadAssets(repo, tag);
  const candidates = loadCandidates(repo, assets);
  const byName = new Map(assets.map((item) => [item.name, item]));

  const missing = [];
  const present = [];
  for (const artifact of candidates) {
    const pkg = byName.get(artifact.url);
    if (!pkg) fail(`候选包不在 Release 资产里：${artifact.url}`);
    if (pkg.size !== artifact.size) fail(`${artifact.url} 大小不符：Release ${pkg.size}，候选元数据 ${artifact.size}`);
    const sha = assetSha256(repo, tag, pkg, workdir);
    if (sha !== String(artifact.sha256).toLowerCase()) {
      fail(`${artifact.url} 的 SHA-256 不符：Release ${sha}，候选元数据 ${artifact.sha256}`);
    }

    const name = `${artifact.url}${SIDECAR_SUFFIX}`;
    const existing = byName.get(name);
    if (!existing) {
      missing.push({ name, body: sidecarBody(artifact) });
      continue;
    }
    const problem = verifySidecar(readAssetText(repo, existing), artifact);
    if (problem) {
      fail(
        `已存在的 ${name} 与候选元数据不一致：${problem}\n` +
          `如果是重跑发布导致包字节变了，先执行 gh release delete-asset ${tag} ${name} 再重跑。`,
      );
    }
    present.push(name);
  }

  console.log(`Release ${tag}：候选包 ${candidates.length} 个，已有 sidecar ${present.length} 个，缺 ${missing.length} 个`);
  for (const name of present) console.log(`  跳过 ${name}（已存在且校验通过）`);
  for (const item of missing) console.log(`  ${options.dryRun ? "待补传" : "补传"} ${item.name}（${Buffer.byteLength(item.body)} 字节）`);

  if (missing.length === 0) {
    console.log("无需补传");
    return;
  }
  if (options.dryRun) {
    console.log("dry-run：未做任何写入");
    return;
  }

  uploadSidecars(repo, tag, workdir, missing);

  const after = new Map(loadAssets(repo, tag).map((item) => [item.name, item]));
  for (const item of missing) {
    const asset = after.get(item.name);
    if (!asset) fail(`补传后回读不到 ${item.name}`);
    if (asset.size !== Buffer.byteLength(item.body)) fail(`补传后 ${item.name} 大小不符`);
    const digest = typeof asset.digest === "string" ? asset.digest : "";
    const local = createHash("sha256").update(item.body).digest("hex");
    if (digest && digest !== `sha256:${local}`) fail(`补传后 ${item.name} digest 不符`);
  }
  console.log(`已补传 ${missing.length} 个 .artifact.json`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    if (error instanceof BackfillError) {
      console.error(`backfill-release-sidecars: ${error.message}`);
      process.exit(1);
    }
    throw error;
  }
}
