/**
 * 请求体注册表 / Content-Type 推导 / 序列化 / 旧快照迁移的单元测试。
 *
 * 只依赖 src/lib/model.js 里的纯函数（不碰 DOM、不碰宿主），所以直接用 node --test 跑，
 * 不需要浏览器或打包步骤。
 */
import assert from "node:assert/strict";
import test from "node:test";

import {
  BODY_TYPES,
  BODY_TYPE_IDS,
  applySnapshot,
  bodyTypeSpec,
  buildBodyParams,
  buildFormBody,
  buildHeaders,
  contentTypeForBody,
  httpContentType,
  jsonIssue,
  normalizeBodyType,
  parseFormBody,
} from "../src/lib/model.js";

// ---------------------------------------------------------------- 注册表

test("注册表：id 唯一且顺序稳定（决定下拉顺序）", () => {
  assert.deepEqual(BODY_TYPE_IDS, ["form", "multipart", "json", "text", "xml", "none"]);
  assert.equal(new Set(BODY_TYPE_IDS).size, BODY_TYPE_IDS.length);
});

test("注册表：每种类型都有编辑器与分组，multipart 标记为生成", () => {
  for (const spec of BODY_TYPES) {
    assert.ok(["none", "raw", "kv", "multipart"].includes(spec.editor), `${spec.id} 的 editor 非法`);
    assert.ok(spec.group, `${spec.id} 缺分组`);
    assert.equal(typeof spec.custom, "boolean", `${spec.id} 缺 custom 标记`);
  }
  assert.equal(bodyTypeSpec("multipart").generated, true);
  assert.equal(bodyTypeSpec("multipart").custom, false);
  assert.equal(bodyTypeSpec("form").generated, undefined);
});

test("注册表：未知类型统一回退到 none，不抛错", () => {
  assert.equal(bodyTypeSpec("graphql").id, "none");
  assert.equal(normalizeBodyType("graphql"), "none");
  assert.equal(normalizeBodyType(undefined), "none");
  assert.equal(normalizeBodyType("json"), "json");
});

// ---------------------------------------------------------------- Content-Type

test("Content-Type：各类型的自动值", () => {
  assert.equal(contentTypeForBody("json"), "application/json");
  assert.equal(contentTypeForBody("xml"), "application/xml");
  assert.equal(contentTypeForBody("form"), "application/x-www-form-urlencoded");
  // 纯文本显式声明 charset，否则部分服务端按 US-ASCII 解释会把中文弄坏。
  assert.equal(contentTypeForBody("text"), "text/plain; charset=utf-8");
  // multipart 的 boundary 只有 sidecar 知道；none 没有正文。
  assert.equal(contentTypeForBody("multipart"), "");
  assert.equal(contentTypeForBody("none"), "");
});

test("Content-Type：httpContentType 与注册表一致", () => {
  assert.equal(httpContentType({ bodyType: "json" }), "application/json");
  assert.equal(httpContentType({ bodyType: "text" }), "text/plain; charset=utf-8");
  assert.equal(httpContentType({ bodyType: "multipart" }), "");
  assert.equal(httpContentType({ bodyType: "none" }), "");
  assert.equal(httpContentType({ bodyType: "未知类型" }), "");
});

// ---------------------------------------------------------------- 请求头组装

test("请求头：手动 Content-Type 不会被自动值覆盖", () => {
  const headers = buildHeaders(
    [{ name: "Content-Type", value: "application/vnd.api+json", enabled: true }],
    "application/json",
  );
  assert.deepEqual(headers, [{ name: "Content-Type", value: "application/vnd.api+json" }]);
});

test("请求头：没填或禁用时补自动值，且只补一次", () => {
  assert.deepEqual(buildHeaders([], "application/json"), [{ name: "Content-Type", value: "application/json" }]);
  assert.deepEqual(
    buildHeaders([{ name: "Content-Type", value: "text/csv", enabled: false }], "application/json"),
    [
      { name: "Content-Type", value: "application/json" },
    ],
  );
});

// ---------------------------------------------------------------- 序列化

test("序列化：none 不发正文，raw 类型共用 bodyText", () => {
  assert.deepEqual(buildBodyParams({ bodyType: "none", bodyText: "被忽略" }), {
    bodyEncoding: "none",
    bodyText: "",
  });
  assert.deepEqual(buildBodyParams({ bodyType: "json", bodyText: '{"a":1}' }), {
    bodyEncoding: "text",
    bodyText: '{"a":1}',
  });
  assert.deepEqual(buildBodyParams({ bodyType: "xml", bodyText: "<a/>" }), {
    bodyEncoding: "text",
    bodyText: "<a/>",
  });
});

test("序列化：form 按 urlencoded 编码中文、空格、& 与 =", () => {
  const params = buildBodyParams({
    bodyType: "form",
    formRows: [
      { kind: "text", name: "q", value: "hello world", enabled: true },
      { kind: "text", name: "tag", value: "a&b=c", enabled: true },
      { kind: "text", name: "cn", value: "中文", enabled: true },
      { kind: "text", name: "off", value: "x", enabled: false },
    ],
  });
  assert.equal(params.bodyEncoding, "text");
  assert.equal(params.bodyText, "q=hello+world&tag=a%26b%3Dc&cn=%E4%B8%AD%E6%96%87");
});

test("序列化：form 忽略文件行；multipart 只给字段摘要与 uploadId", () => {
  const rows = [
    { kind: "text", name: "note", value: "hi", enabled: true },
    {
      kind: "file",
      name: "upload",
      enabled: true,
      files: [{ uploadId: "up-1", fileName: "a.png", contentType: "image/png" }],
    },
  ];
  const form = buildBodyParams({ bodyType: "form", formRows: rows });
  assert.equal(form.bodyText, "note=hi");

  const multipart = buildBodyParams({ bodyType: "multipart", formRows: rows });
  assert.equal(multipart.bodyEncoding, "multipart");
  assert.deepEqual(multipart.multipart.fields, [{ name: "note", value: "hi" }]);
  assert.equal(multipart.multipart.files.length, 1);
  assert.equal(multipart.multipart.files[0].uploadId, "up-1");
  // 文件内容不随参数传递，只给 sidecar 的暂存 id。
  assert.equal("base64" in multipart.multipart.files[0], false);
});

test("序列化：multipart 空字段名按 file 发送，缺 uploadId 的文件被跳过", () => {
  const multipart = buildBodyParams({
    bodyType: "multipart",
    formRows: [
      { kind: "file", name: "", enabled: true, files: [{ uploadId: "up-1", fileName: "a.txt" }] },
      { kind: "file", name: "skipped", enabled: true, files: [{ fileName: "b.txt" }] },
    ],
  });
  assert.equal(multipart.multipart.files.length, 1);
  assert.equal(multipart.multipart.files[0].name, "file");
});

test("表单正文解析：空输入与缺等号的片段不炸", () => {
  assert.deepEqual(parseFormBody(""), []);
  assert.deepEqual(buildFormBody([]), "");
});

// ---------------------------------------------------------------- 旧快照迁移

test("旧快照：bodyType=form 且只有 urlencoded 正文时迁移成键值行", () => {
  const next = applySnapshot({
    protocol: "http",
    http: { method: "POST", url: "http://x/y", bodyType: "form", bodyText: "a=1&b=hello+world" },
  });
  assert.equal(next.http.bodyType, "form");
  assert.deepEqual(
    next.http.formRows.filter((row) => row.name).map(({ kind, name, value, enabled }) => ({ kind, name, value, enabled })),
    [
      { kind: "text", name: "a", value: "1", enabled: true },
      { kind: "text", name: "b", value: "hello world", enabled: true },
    ],
  );
});

test("旧快照：表单快照里带文件行时收敛到 multipart", () => {
  const next = applySnapshot({
    protocol: "http",
    http: {
      bodyType: "form",
      forceMultipart: true,
      formRows: [{ kind: "text", name: "a", value: "1" }],
    },
  });
  assert.equal(next.http.bodyType, "multipart");
});

test("旧快照：未知类型回退 none，其他字段照旧可用", () => {
  const next = applySnapshot({
    protocol: "http",
    http: { method: "post", url: "http://x/y", bodyType: "graphql" },
  });
  assert.equal(next.http.bodyType, "none");
  assert.equal(next.http.method, "POST");
  assert.equal(next.http.url, "http://x/y");
});

// ---------------------------------------------------------------- JSON 校验

test("JSON 校验：合法与空正文都通过", () => {
  assert.equal(jsonIssue('{"a":1}'), null);
  assert.equal(jsonIssue("  \n "), null);
  assert.equal(jsonIssue(""), null);
});

test("JSON 校验：语法错误给出可定位的行列", () => {
  const issue = jsonIssue('{\n  "a": 1,\n}');
  assert.ok(issue);
  assert.equal(issue.line, 3);
  assert.equal(issue.column, 1);
  assert.match(issue.message, /JSON/i);
});
