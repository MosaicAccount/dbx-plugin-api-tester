/**
 * JSON 高亮。
 *
 * 输出交给 {@html} 渲染，因此这里必须自己转义：先按词法规约成 token 序列，
 * 再对每段文本做 HTML 转义，绝不把原始输入直接拼进 HTML。
 */

const HIGHLIGHT_LIMIT = 200000;

export function escapeHTML(text) {
  return String(text).replace(
    /[&<>"']/g,
    (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character],
  );
}

/**
 * @param {string} text
 * @returns {string} 已转义的 HTML 片段
 */
export function highlightJSON(text) {
  const source = String(text === undefined || text === null ? "" : text);
  if (source.length > HIGHLIGHT_LIMIT) return escapeHTML(source);

  let html = "";
  let index = 0;
  while (index < source.length) {
    const character = source[index];

    if (character === '"') {
      let end = index + 1;
      while (end < source.length) {
        if (source[end] === "\\") {
          end += 2;
          continue;
        }
        if (source[end] === '"') {
          end += 1;
          break;
        }
        end += 1;
      }
      const token = source.slice(index, end);
      let lookahead = end;
      while (lookahead < source.length && /\s/.test(source[lookahead])) lookahead += 1;
      const isKey = source[lookahead] === ":";
      html += `<span class="tok-${isKey ? "key" : "string"}">${escapeHTML(token)}</span>`;
      index = end;
      continue;
    }

    if (character === "-" || (character >= "0" && character <= "9")) {
      const match = source.slice(index).match(/^-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/);
      if (match) {
        html += `<span class="tok-number">${match[0]}</span>`;
        index += match[0].length;
        continue;
      }
    }

    if (source.startsWith("true", index) || source.startsWith("false", index) || source.startsWith("null", index)) {
      const word = source.startsWith("false", index) ? "false" : source.startsWith("true", index) ? "true" : "null";
      html += `<span class="tok-literal">${word}</span>`;
      index += word.length;
      continue;
    }

    if ("{}[]:,".includes(character)) {
      html += `<span class="tok-punct">${escapeHTML(character)}</span>`;
      index += 1;
      continue;
    }

    html += escapeHTML(character);
    index += 1;
  }
  return html;
}

export function looksLikeJSON(text) {
  const trimmed = String(text || "").trim();
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return false;
  try {
    JSON.parse(trimmed);
    return true;
  } catch {
    return false;
  }
}

export function prettyJSON(text) {
  const trimmed = String(text || "").trim();
  if (!trimmed) return "";
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return text;
  }
}

/**
 * 二进制正文的预览长度，单位是 base64 字符数。
 *
 * 响应体上限是 3 MB，base64 之后是 4 MB 的一整块文本：直接塞进 `<pre>` 会让浏览器
 * 排版和滚动都变慢。二进制本来也只能看个文件头和字节序列，512 个 base64 字符（约
 * 384 字节、六七行）足够确认"这是不是一个 zip / png"，剩下的交给下载按钮。
 */
export const BINARY_PREVIEW_CHARS = 512;

/**
 * 截出二进制正文的预览片段。
 *
 * @returns {{ text: string, truncated: boolean }}
 */
export function binaryPreview(base64) {
  const text = String(base64 === undefined || base64 === null ? "" : base64);
  if (text.length <= BINARY_PREVIEW_CHARS) return { text, truncated: false };
  return { text: text.slice(0, BINARY_PREVIEW_CHARS), truncated: true };
}

/**
 * 响应体渲染决策。
 *
 * 抽成纯函数是为了让"该按 JSON 渲染还是原样显示"这一判断可被测试，
 * 组件只负责把结果画出来。
 * @returns {{ variant: "binary" | "empty" | "code", text: string, json: boolean }}
 */
export function presentBody(data) {
  if (data?.bodyKind === "binary") {
    return { variant: "code", text: binaryPreview(data.bodyBase64).text, json: false };
  }
  if (data?.bodyKind === "empty" || !data?.bodyText) {
    return { variant: "empty", text: "", json: false };
  }
  const contentType = String(data.contentType || "").toLowerCase();
  const json = contentType.includes("json") || looksLikeJSON(data.bodyText);
  return { variant: "code", text: json ? prettyJSON(data.bodyText) : data.bodyText, json };
}

/** 可复制的响应文本。 */
export function responseText(response) {
  if (!response || response.kind !== "done" || !response.data) return "";
  const data = response.data;
  if (response.protocol === "http") {
    return data.bodyKind === "binary" ? data.bodyBase64 || "" : data.bodyText || "";
  }
  if (Array.isArray(data.messages) && data.messages.length) return data.messages.join("\n");
  return data.messageJson || data.status?.message || "";
}
