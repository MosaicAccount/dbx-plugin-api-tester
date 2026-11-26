import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { sidecarBody, verifySidecar } from "./backfill-release-sidecars.mjs";

// 取自 v0.1.0 Release 上由 `dbx-plugin package` 生成的真实产物，用来锁住字节格式。
const LINUX_X64 = {
  target: "linux-x64",
  url: "com.jlian.apitester-0.1.0-linux-x64.dbxp",
  sha256: "7bc48a63a54b01ee3b5a833110c7097118f251d3481a29eee0ca1794a59425d3",
  size: 8709470,
};

const EXPECTED = `{
  "target": "linux-x64",
  "url": "com.jlian.apitester-0.1.0-linux-x64.dbxp",
  "sha256": "7bc48a63a54b01ee3b5a833110c7097118f251d3481a29eee0ca1794a59425d3",
  "size": 8709470
}
`;

test("sidecarBody 与构建产物字节一致", () => {
  assert.equal(sidecarBody(LINUX_X64), EXPECTED);
  assert.equal(Buffer.byteLength(sidecarBody(LINUX_X64)), 180);
});

test("sidecarBody 忽略多余字段", () => {
  const body = sidecarBody({ ...LINUX_X64, signingKeyId: "should-not-appear" });
  assert.equal(body, EXPECTED);
});

test("verifySidecar 接受完全一致的元数据", () => {
  assert.equal(verifySidecar(EXPECTED, LINUX_X64), "");
});

test("verifySidecar 报出不一致的字段", () => {
  const wrongSha = JSON.parse(EXPECTED);
  wrongSha.sha256 = "0".repeat(64);
  assert.match(verifySidecar(JSON.stringify(wrongSha), LINUX_X64), /sha256/);

  const wrongSize = JSON.parse(EXPECTED);
  wrongSize.size = 1;
  assert.match(verifySidecar(JSON.stringify(wrongSize), LINUX_X64), /size/);

  const wrongTarget = JSON.parse(EXPECTED);
  wrongTarget.target = "darwin-arm64";
  assert.match(verifySidecar(JSON.stringify(wrongTarget), LINUX_X64), /target/);

  const wrongUrl = JSON.parse(EXPECTED);
  wrongUrl.url = "other.dbxp";
  assert.match(verifySidecar(JSON.stringify(wrongUrl), LINUX_X64), /url/);
});

test("verifySidecar 拒绝非法 JSON 与非对象", () => {
  assert.match(verifySidecar("{", LINUX_X64), /JSON/);
  assert.match(verifySidecar("[]", LINUX_X64), /对象/);
  assert.match(verifySidecar("null", LINUX_X64), /对象/);
});

test("脚本自身可被解析（防止语法错误进 CI）", () => {
  const source = readFileSync(new URL("./backfill-release-sidecars.mjs", import.meta.url), "utf8");
  assert.ok(source.includes("sidecarBody"));
});
