(() => {
  "use strict";

  const HTTP_TIMEOUT_DEFAULT = 30000;
  const GRPC_TIMEOUT_DEFAULT = 20000;
  const TIMEOUT_MAX = 110000;
  const HISTORY_LIMIT = 30;
  const DRAFT_LIMIT = 200000;
  const SAVE_LIMIT = 240000;
  const BRIDGE_BODY_LIMIT = 1800000;

  const BODY_CONTENT_TYPES = {
    json: "application/json",
    text: "text/plain",
    xml: "application/xml",
    form: "application/x-www-form-urlencoded",
  };

  const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

  const messages = {
    en: {
      appName: "API Tester",
      tagline: "HTTP & gRPC",
      saved: "Saved",
      history: "History",
      clear: "Clear",
      newRequest: "New request",
      save: "Save",
      saveAs: "Save as",
      send: "Send",
      sending: "Sending…",
      discover: "Discover",
      discovering: "Discovering…",
      invoke: "Invoke",
      params: "Params",
      headers: "Headers",
      body: "Body",
      options: "Options",
      metadata: "Metadata",
      message: "Message",
      response: "Response",
      copy: "Copy",
      addParam: "+ Add parameter",
      addHeader: "+ Add header",
      addMetadata: "+ Add metadata",
      none: "None",
      text: "Text",
      form: "Form",
      format: "Format",
      timeout: "Timeout (ms)",
      followRedirects: "Follow redirects",
      skipTls: "Skip TLS verification",
      tls: "TLS",
      service: "Service",
      method: "Method",
      effectiveUrl: "URL",
      headerHint: "Content-Type is added automatically for the selected body type.",
      tlsHint: "Skip TLS verification only for trusted development servers.",
      metadataHint: "Metadata keys are sent as lowercase gRPC headers, for example authorization.",
      messageHint: "JSON request body, encoded with the method's protobuf schema.",
      grpcHint: "Discovery uses gRPC server reflection; the method request type is resolved dynamically.",
      urlPlaceholder: "https://api.example.com/users",
      targetPlaceholder: "localhost:50051",
      untitled: "Untitled request",
      key: "key",
      value: "value",
      enabled: "Include this row",
      remove: "Remove row",
      delete: "Delete",
      savedEmpty: "Nothing saved yet.",
      historyEmpty: "Requests you send show up here.",
      storageUnavailable: "Storage is unavailable; saved items only last for this session.",
      loading: "Sending…",
      emptyResponse: "No response yet",
      emptyResponseHint: "Send a request to see the response. Cmd/Ctrl + Enter works too.",
      needUrl: "Enter a request URL first.",
      invalidUrl: "The URL must be an absolute http:// or https:// URL.",
      needTarget: "Enter a gRPC target such as localhost:50051.",
      needService: "Select a gRPC service first.",
      needMethod: "Select a gRPC method first.",
      invalidJson: "The message body is not valid JSON.",
      savePromptTitle: "Save request",
      savePromptPlaceholder: "Request name",
      savedToast: "Saved \"{name}\".",
      saveFailed: "Could not save the request.",
      requestTooLarge: "This request is too large to save (storage limit is about 256 KB).",
      bodyTooLarge: "The request body is too large to send through the plugin host (limit is about 2 MB).",
      loadFailed: "Could not load the saved request.",
      deleteTitle: "Delete request",
      deleteMessage: "Delete \"{name}\"? This cannot be undone.",
      clearHistoryTitle: "Clear history",
      clearHistoryMessage: "Remove all history entries?",
      deletedToast: "Deleted \"{name}\".",
      historyCleared: "History cleared.",
      discovered: "Found {count} service(s).",
      noServices: "No services found. Is server reflection enabled?",
      cancel: "Cancel",
      confirm: "Confirm",
      requestFailed: "Request failed",
      hintTimeout: "The request timed out. Increase the timeout or check the server.",
      hintTls: "TLS verification failed. For a development server you can enable Skip TLS verification.",
      hintConnect: "Could not connect. Check the host, port, and that the server is running.",
      hintDns: "The host name could not be resolved.",
      hintReflection: "gRPC reflection is unavailable. Enable server reflection to discover and invoke methods.",
      hintAuth: "The server rejected the request credentials or metadata.",
      responseHeaders: "Response headers",
      trailers: "Trailers",
      redirects: "Redirects",
      binaryBody: "Binary body (base64)",
      emptyBody: "(empty body)",
      truncated: "The response body was truncated at {size}.",
      streamTruncated: "The stream was truncated at the display limit.",
      copyDone: "Copied to the clipboard.",
      copyFailed: "Could not copy to the clipboard.",
      nothingToCopy: "There is nothing to copy.",
      contentTypeHint: "Content-Type: {type}",
      serverStreaming: "server streaming",
      clientStreaming: "client streaming",
      unary: "unary",
      chooseService: "Discover services first",
      discoverFirst: "Discover services to list methods",
      codeLabel: "code {code}",
      messages: "Messages",
    },
    zh: {
      appName: "API 调试台",
      tagline: "HTTP 与 gRPC",
      saved: "已保存",
      history: "历史",
      clear: "清空",
      newRequest: "新建请求",
      save: "保存",
      saveAs: "另存为",
      send: "发送",
      sending: "发送中…",
      discover: "发现服务",
      discovering: "发现中…",
      invoke: "调用",
      params: "参数",
      headers: "请求头",
      body: "请求体",
      options: "选项",
      metadata: "元数据",
      message: "消息",
      response: "响应",
      copy: "复制",
      addParam: "+ 添加参数",
      addHeader: "+ 添加请求头",
      addMetadata: "+ 添加元数据",
      none: "无",
      text: "文本",
      form: "表单",
      format: "格式化",
      timeout: "超时（毫秒）",
      followRedirects: "跟随重定向",
      skipTls: "跳过 TLS 校验",
      tls: "TLS",
      service: "服务",
      method: "方法",
      effectiveUrl: "URL",
      headerHint: "按请求体类型自动补充 Content-Type。",
      tlsHint: "仅在可信的开发服务器上跳过 TLS 校验。",
      metadataHint: "元数据会以小写 gRPC Header 发送，例如 authorization。",
      messageHint: "JSON 请求体，按方法的 protobuf 结构编码。",
      grpcHint: "服务发现依赖 gRPC Server Reflection，请求类型会自动解析。",
      urlPlaceholder: "https://api.example.com/users",
      targetPlaceholder: "localhost:50051",
      untitled: "未命名请求",
      key: "键",
      value: "值",
      enabled: "启用该行",
      remove: "删除该行",
      delete: "删除",
      savedEmpty: "还没有保存的请求。",
      historyEmpty: "发送过的请求会出现在这里。",
      storageUnavailable: "存储不可用，保存内容只在本次会话中有效。",
      loading: "发送中…",
      emptyResponse: "还没有响应",
      emptyResponseHint: "发送请求后在这里查看结果，也可以按 Cmd/Ctrl + Enter。",
      needUrl: "请先填写请求 URL。",
      invalidUrl: "URL 必须是完整的 http:// 或 https:// 地址。",
      needTarget: "请填写 gRPC 目标，例如 localhost:50051。",
      needService: "请先选择 gRPC 服务。",
      needMethod: "请先选择 gRPC 方法。",
      invalidJson: "消息体不是合法的 JSON。",
      savePromptTitle: "保存请求",
      savePromptPlaceholder: "请求名称",
      savedToast: "已保存“{name}”。",
      saveFailed: "保存请求失败。",
      requestTooLarge: "请求内容过大，无法保存（存储单值上限约 256 KB）。",
      bodyTooLarge: "请求体过大，无法通过插件宿主发送（上限约 2 MB）。",
      loadFailed: "加载已保存请求失败。",
      deleteTitle: "删除请求",
      deleteMessage: "确定删除“{name}”？此操作无法撤销。",
      clearHistoryTitle: "清空历史",
      clearHistoryMessage: "确定移除所有历史记录？",
      deletedToast: "已删除“{name}”。",
      historyCleared: "历史已清空。",
      discovered: "发现 {count} 个服务。",
      noServices: "没有发现服务，请确认服务器启用了 Reflection。",
      cancel: "取消",
      confirm: "确定",
      requestFailed: "请求失败",
      hintTimeout: "请求超时。可以增大超时时间，或检查服务器状态。",
      hintTls: "TLS 校验失败。开发服务器可以尝试勾选“跳过 TLS 校验”。",
      hintConnect: "无法建立连接。检查主机、端口以及服务器是否已启动。",
      hintDns: "无法解析主机名。",
      hintReflection: "gRPC Reflection 不可用。需要服务端开启 Reflection 才能发现和调用方法。",
      hintAuth: "服务器拒绝了请求的凭据或元数据。",
      responseHeaders: "响应头",
      trailers: "Trailers",
      redirects: "重定向",
      binaryBody: "二进制响应（base64）",
      emptyBody: "（空响应体）",
      truncated: "响应体在 {size} 处被截断。",
      streamTruncated: "流式响应达到展示上限，已截断。",
      copyDone: "已复制到剪贴板。",
      copyFailed: "复制到剪贴板失败。",
      nothingToCopy: "没有可复制的内容。",
      contentTypeHint: "Content-Type：{type}",
      serverStreaming: "服务端流",
      clientStreaming: "客户端流",
      unary: "一元调用",
      chooseService: "请先发现服务",
      discoverFirst: "发现服务后可选择方法",
      codeLabel: "code {code}",
      messages: "消息",
    },
  };

  let locale = "en";

  function t(key, variables) {
    const dictionary = messages[locale] || messages.en;
    let value = dictionary[key];
    if (value === undefined) value = messages.en[key];
    if (value === undefined) return key;
    if (variables) {
      for (const [name, replacement] of Object.entries(variables)) {
        value = value.split(`{${name}}`).join(String(replacement));
      }
    }
    return value;
  }

  function detectLocale(value) {
    return String(value || "").toLowerCase().startsWith("zh") ? "zh" : "en";
  }

  function blankKV() {
    return { name: "", value: "", enabled: true };
  }

  function defaultHttp() {
    return {
      method: "GET",
      url: "",
      params: [blankKV()],
      headers: [blankKV()],
      bodyType: "none",
      bodyText: "",
      timeoutMs: HTTP_TIMEOUT_DEFAULT,
      followRedirects: true,
      skipTlsVerify: false,
    };
  }

  function defaultGrpc() {
    return {
      target: "",
      tls: false,
      skipTlsVerify: false,
      service: "",
      method: "",
      metadata: [blankKV()],
      message: "{\n  \n}",
      timeoutMs: GRPC_TIMEOUT_DEFAULT,
      services: [],
      methods: [],
    };
  }

  const state = {
    protocol: "http",
    activeSavedId: null,
    activeName: "",
    http: defaultHttp(),
    grpc: defaultGrpc(),
    savedIndex: [],
    historyIndex: [],
    response: null,
    responseTab: "body",
    sending: false,
    discovering: false,
    hydrating: false,
  };

  const plugin = window.dbxPlugin;

  const els = {
    editorTitle: document.getElementById("editor-title"),
    protocolTabs: document.getElementById("protocol-tabs"),
    httpEditor: document.getElementById("http-editor"),
    grpcEditor: document.getElementById("grpc-editor"),
    httpMethodMenu: document.getElementById("http-method-menu"),
    httpMethodTrigger: document.getElementById("http-method-trigger"),
    httpMethodLabel: document.getElementById("http-method-label"),
    httpMethodPopup: document.getElementById("http-method-popup"),
    httpUrl: document.getElementById("http-url"),
    httpSend: document.getElementById("http-send"),
    httpTabs: document.getElementById("http-tabs"),
    httpParamsEditor: document.getElementById("http-params-editor"),
    httpHeadersEditor: document.getElementById("http-headers-editor"),
    httpBodyEditor: document.getElementById("http-body-editor"),
    httpBodyText: document.getElementById("http-body-text"),
    httpBodyHighlight: document.getElementById("http-body-highlight"),
    bodyTypes: document.getElementById("body-types"),
    formatBody: document.getElementById("format-body"),
    bodyHint: document.getElementById("body-hint"),
    httpTimeout: document.getElementById("http-timeout"),
    httpFollow: document.getElementById("http-follow"),
    httpSkipTls: document.getElementById("http-skip-tls"),
    effectiveUrl: document.getElementById("effective-url"),
    addParam: document.getElementById("add-param"),
    addHeader: document.getElementById("add-header"),
    grpcTarget: document.getElementById("grpc-target"),
    grpcTls: document.getElementById("grpc-tls"),
    grpcSkipTls: document.getElementById("grpc-skip-tls"),
    grpcDiscover: document.getElementById("grpc-discover"),
    grpcInvoke: document.getElementById("grpc-invoke"),
    grpcServiceMenu: document.getElementById("grpc-service-menu"),
    grpcServiceTrigger: document.getElementById("grpc-service-trigger"),
    grpcServiceLabel: document.getElementById("grpc-service-label"),
    grpcServicePopup: document.getElementById("grpc-service-popup"),
    grpcMethodMenu: document.getElementById("grpc-method-menu"),
    grpcMethodTrigger: document.getElementById("grpc-method-trigger"),
    grpcMethodLabel: document.getElementById("grpc-method-label"),
    grpcMethodPopup: document.getElementById("grpc-method-popup"),
    grpcSignature: document.getElementById("grpc-signature"),
    grpcTabs: document.getElementById("grpc-tabs"),
    grpcMetadataEditor: document.getElementById("grpc-metadata-editor"),
    addMetadata: document.getElementById("add-metadata"),
    grpcMessage: document.getElementById("grpc-message"),
    grpcMessageHighlight: document.getElementById("grpc-message-highlight"),
    formatMessage: document.getElementById("format-message"),
    grpcTimeout: document.getElementById("grpc-timeout"),
    responseTabs: document.getElementById("response-tabs"),
    responseBody: document.getElementById("response-body"),
    responseBadges: document.getElementById("response-badges"),
    copyResponse: document.getElementById("copy-response"),
    savedList: document.getElementById("saved-list"),
    historyList: document.getElementById("history-list"),
    clearHistory: document.getElementById("clear-history"),
    storageHint: document.getElementById("storage-hint"),
    newButton: document.getElementById("new-btn"),
    saveButton: document.getElementById("save-btn"),
    saveAsButton: document.getElementById("save-as-btn"),
    modalBackdrop: document.getElementById("modal-backdrop"),
    modalTitle: document.getElementById("modal-title"),
    modalMessage: document.getElementById("modal-message"),
    modalInput: document.getElementById("modal-input"),
    modalCancel: document.getElementById("modal-cancel"),
    modalConfirm: document.getElementById("modal-confirm"),
    toast: document.getElementById("toast"),
  };

  const memory = new Map();
  let storageAvailable = false;
  let storageWarningShown = false;

  const storage = {
    async get(key) {
      if (storageAvailable) {
        try {
          return await plugin.storage.get(key);
        } catch {
          return null;
        }
      }
      return memory.has(key) ? memory.get(key) : null;
    },
    async set(key, value) {
      if (storageAvailable) {
        try {
          await plugin.storage.set(key, value);
          return true;
        } catch (error) {
          if (!storageWarningShown) {
            storageWarningShown = true;
            toast(t("storageUnavailable"), "warn");
          }
          return false;
        }
      }
      memory.set(key, value);
      return true;
    },
    async remove(key) {
      if (storageAvailable) {
        try {
          await plugin.storage.delete(key);
        } catch {
          void 0;
        }
        return;
      }
      memory.delete(key);
    },
  };

  function create(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function clear(node) {
    while (node && node.firstChild) node.removeChild(node.firstChild);
  }

  const HIGHLIGHT_LIMIT = 200000;

  function escapeHTML(text) {
    return String(text).replace(/[&<>"']/g, (character) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;",
    })[character]);
  }

  function highlightJSON(text) {
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
        const key = source[lookahead] === ":";
        html += `<span class="tok-${key ? "key" : "string"}">${escapeHTML(token)}</span>`;
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

  function looksLikeJSON(text) {
    const trimmed = String(text || "").trim();
    if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return false;
    try {
      JSON.parse(trimmed);
      return true;
    } catch {
      return false;
    }
  }

  function syncEditorScroll(textarea, highlight) {
    if (!textarea || !highlight) return;
    highlight.scrollTop = textarea.scrollTop;
    highlight.scrollLeft = textarea.scrollLeft;
  }

  function updateHttpBodyHighlight() {
    if (!els.httpBodyHighlight || !els.httpBodyText) return;
    const value = els.httpBodyText.value;
    els.httpBodyHighlight.innerHTML = `${state.http.bodyType === "json" ? highlightJSON(value) : escapeHTML(value)} `;
    syncEditorScroll(els.httpBodyText, els.httpBodyHighlight);
  }

  function updateGrpcMessageHighlight() {
    if (!els.grpcMessageHighlight || !els.grpcMessage) return;
    els.grpcMessageHighlight.innerHTML = `${highlightJSON(els.grpcMessage.value)} `;
    syncEditorScroll(els.grpcMessage, els.grpcMessageHighlight);
  }

  function createSelectMenu({ root, trigger, label, popup, onChange }) {
    let items = [];
    let value = "";
    let disabled = false;
    let open = false;

    function itemFor(nextValue) {
      return items.find((item) => item.value === nextValue);
    }

    function sync() {
      const selected = itemFor(value) || items[0];
      label.textContent = selected ? selected.label : "";
      trigger.disabled = disabled;
      trigger.classList.toggle("is-disabled", disabled);
    }

    function render() {
      clear(popup);
      for (const item of items) {
        const button = create("button", "select-option", item.label);
        button.type = "button";
        button.setAttribute("role", "option");
        button.dataset.value = item.value;
        if (item.value === value) button.classList.add("is-selected");
        if (item.disabled) button.disabled = true;
        button.addEventListener("click", () => {
          if (item.disabled) return;
          setValue(item.value);
          close();
          onChange(item.value);
        });
        popup.append(button);
      }
    }

    function setItems(next) {
      items = next.map((item) => (typeof item === "string" ? { value: item, label: item } : item));
      if (!itemFor(value)) value = items[0] ? items[0].value : "";
      sync();
      if (open) render();
    }

    function setValue(next) {
      value = next;
      sync();
    }

    function getValue() {
      return value;
    }

    function setDisabled(next) {
      disabled = next;
      sync();
      if (disabled) close();
    }

    function openMenu() {
      if (disabled) return;
      render();
      popup.classList.remove("is-hidden");
      root.classList.add("is-open");
      trigger.setAttribute("aria-expanded", "true");
      open = true;
      const selected = popup.querySelector(".is-selected");
      if (selected) selected.scrollIntoView({ block: "nearest" });
    }

    function close() {
      popup.classList.add("is-hidden");
      root.classList.remove("is-open");
      trigger.setAttribute("aria-expanded", "false");
      open = false;
    }

    trigger.addEventListener("click", (event) => {
      event.stopPropagation();
      if (open) close();
      else openMenu();
    });
    trigger.addEventListener("keydown", (event) => {
      if (event.key === "Escape") close();
    });
    popup.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        close();
        trigger.focus();
      }
    });
    document.addEventListener("click", (event) => {
      if (open && !root.contains(event.target)) close();
    });

    return { setItems, setValue, getValue, setDisabled, close };
  }

  const httpMethodMenu = createSelectMenu({
    root: els.httpMethodMenu,
    trigger: els.httpMethodTrigger,
    label: els.httpMethodLabel,
    popup: els.httpMethodPopup,
    onChange: (value) => {
      state.http.method = value;
      persistDraft();
    },
  });
  httpMethodMenu.setItems(HTTP_METHODS);
  httpMethodMenu.setValue("GET");

  const grpcServiceMenu = createSelectMenu({
    root: els.grpcServiceMenu,
    trigger: els.grpcServiceTrigger,
    label: els.grpcServiceLabel,
    popup: els.grpcServicePopup,
    onChange: (value) => {
      state.grpc.service = value;
      state.grpc.method = "";
      state.grpc.methods = [];
      renderGrpcMethods();
      renderGrpcSignature();
      persistDraft();
      void loadGrpcMethods();
    },
  });
  grpcServiceMenu.setDisabled(true);

  const grpcMethodMenu = createSelectMenu({
    root: els.grpcMethodMenu,
    trigger: els.grpcMethodTrigger,
    label: els.grpcMethodLabel,
    popup: els.grpcMethodPopup,
    onChange: (value) => {
      state.grpc.method = value;
      renderGrpcSignature();
      persistDraft();
    },
  });
  grpcMethodMenu.setDisabled(true);

  function clampNumber(value, fallback, minimum, maximum) {
    const parsed = Number(value);
    if (!Number.isFinite(parsed) || parsed <= 0) return fallback;
    return Math.max(minimum, Math.min(maximum, Math.round(parsed)));
  }

  function normalizeKV(value) {
    if (!Array.isArray(value)) return [blankKV()];
    const rows = value
      .filter((item) => item && typeof item === "object")
      .map((item) => ({
        name: item.name === undefined || item.name === null ? "" : String(item.name),
        value: item.value === undefined || item.value === null ? "" : String(item.value),
        enabled: item.enabled !== false,
      }));
    return rows.length ? rows : [blankKV()];
  }

  function uid() {
    if (window.crypto && typeof window.crypto.randomUUID === "function") return window.crypto.randomUUID();
    return `id-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
  }

  function truncate(text, limit) {
    const value = String(text || "");
    return value.length > limit ? `${value.slice(0, limit - 1)}…` : value;
  }

  function formatBytes(bytes) {
    if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  }

  let toastTimer = null;

  function toast(message, tone) {
    if (!message) return;
    els.toast.textContent = message;
    els.toast.className = `toast${tone ? ` toast--${tone}` : ""}`;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => els.toast.classList.add("is-hidden"), 3200);
  }

  let modal = null;

  function openModal(options) {
    return new Promise((resolve) => {
      modal = { resolve, withInput: Boolean(options.input) };
      els.modalTitle.textContent = options.title || "";
      els.modalMessage.textContent = options.message || "";
      els.modalMessage.classList.toggle("is-hidden", !options.message);
      els.modalInput.classList.toggle("is-hidden", !options.input);
      els.modalInput.value = options.inputValue || "";
      els.modalInput.placeholder = options.placeholder || "";
      els.modalConfirm.textContent = options.confirmLabel || t("confirm");
      els.modalConfirm.className = `btn ${options.danger ? "btn-danger" : "btn-primary"}`;
      els.modalBackdrop.classList.remove("is-hidden");
      setTimeout(() => {
        if (options.input) {
          els.modalInput.focus();
          els.modalInput.select();
        } else {
          els.modalConfirm.focus();
        }
      }, 0);
    });
  }

  function closeModal(value) {
    if (!modal) return;
    const resolve = modal.resolve;
    modal = null;
    els.modalBackdrop.classList.add("is-hidden");
    resolve(value);
  }

  function askName(defaultValue) {
    return openModal({
      title: t("savePromptTitle"),
      input: true,
      inputValue: defaultValue || "",
      placeholder: t("savePromptPlaceholder"),
      confirmLabel: t("save"),
    }).then((value) => (typeof value === "string" && value.trim() ? value.trim() : null));
  }

  function askConfirm(title, message, confirmLabel) {
    return openModal({
      title,
      message,
      confirmLabel: confirmLabel || t("confirm"),
      danger: true,
    });
  }

  function datasetKey(attribute) {
    return attribute.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
  }

  function bindTabs(container, attribute, onSelect) {
    container.addEventListener("click", (event) => {
      const button = event.target.closest(`[data-${attribute}]`);
      if (!button || !container.contains(button)) return;
      container.querySelectorAll(`[data-${attribute}]`).forEach((node) => node.classList.toggle("is-active", node === button));
      onSelect(button.dataset[datasetKey(attribute)]);
    });
  }

  function showPanel(group, value) {
    document.querySelectorAll(`[data-${group}-panel]`).forEach((panel) => {
      panel.classList.toggle("is-active", panel.dataset[datasetKey(`${group}-panel`)] === value);
    });
  }

  function renderKV(container, list, options) {
    clear(container);
    list.forEach((item, index) => {
      const row = create("div", "kv-row");
      const enabled = document.createElement("input");
      enabled.type = "checkbox";
      enabled.checked = item.enabled !== false;
      enabled.title = t("enabled");
      enabled.addEventListener("change", () => {
        item.enabled = enabled.checked;
        options.onChange();
      });

      const name = document.createElement("input");
      name.className = "input";
      name.value = item.name;
      name.placeholder = options.namePlaceholder || t("key");
      name.spellcheck = false;
      name.addEventListener("input", () => {
        item.name = name.value;
        options.onChange();
      });

      const value = document.createElement("input");
      value.className = "input";
      value.value = item.value;
      value.placeholder = options.valuePlaceholder || t("value");
      value.spellcheck = false;
      value.addEventListener("input", () => {
        item.value = value.value;
        options.onChange();
      });

      const remove = create("button", "kv-remove", "×");
      remove.type = "button";
      remove.title = t("remove");
      remove.addEventListener("click", () => {
        list.splice(index, 1);
        if (list.length === 0) list.push(blankKV());
        renderKV(container, list, options);
        options.onChange();
      });

      const addOnEnter = (event) => {
        if (event.key !== "Enter" || index !== list.length - 1) return;
        event.preventDefault();
        list.push(blankKV());
        renderKV(container, list, options);
        options.onChange();
        const rows = container.querySelectorAll(".kv-row");
        const lastRow = rows[rows.length - 1];
        if (lastRow) {
          const inputs = lastRow.querySelectorAll("input");
          if (inputs[1]) inputs[1].focus();
        }
      };
      name.addEventListener("keydown", addOnEnter);
      value.addEventListener("keydown", addOnEnter);

      row.append(enabled, name, value, remove);
      container.append(row);
    });
  }

  function renderEffectiveUrl() {
    const value = computeEffectiveUrl();
    els.effectiveUrl.textContent = value || "—";
    els.effectiveUrl.title = value;
  }

  function computeEffectiveUrl() {
    const raw = state.http.url.trim();
    if (!raw) return "";
    let parsed;
    try {
      parsed = new URL(raw);
    } catch {
      return raw;
    }
    for (const row of state.http.params) {
      if (row.enabled === false || !row.name.trim()) continue;
      parsed.searchParams.append(row.name.trim(), row.value);
    }
    return parsed.toString();
  }

  function contentTypeForBody(bodyType) {
    return BODY_CONTENT_TYPES[bodyType] || "";
  }

  function renderBodyType() {
    els.bodyTypes.querySelectorAll("[data-body]").forEach((button) => {
      button.classList.toggle("is-active", button.dataset.body === state.http.bodyType);
    });
    const empty = state.http.bodyType === "none";
    els.httpBodyText.disabled = empty;
    els.httpBodyEditor.classList.toggle("is-hidden", empty);
    els.formatBody.disabled = empty;
    els.bodyHint.textContent = empty ? "" : t("contentTypeHint", { type: contentTypeForBody(state.http.bodyType) });
    updateHttpBodyHighlight();
  }

  function applySnapshot(snapshot) {
    if (!snapshot || typeof snapshot !== "object") return;
    state.hydrating = true;
    if (snapshot.protocol === "http" || snapshot.protocol === "grpc") state.protocol = snapshot.protocol;
    const http = snapshot.http && typeof snapshot.http === "object" ? snapshot.http : {};
    state.http = {
      ...defaultHttp(),
      ...http,
      method: typeof http.method === "string" && http.method.trim() ? http.method.trim().toUpperCase() : "GET",
      url: typeof http.url === "string" ? http.url : "",
      bodyText: typeof http.bodyText === "string" ? http.bodyText : "",
      params: normalizeKV(http.params),
      headers: normalizeKV(http.headers),
      timeoutMs: clampNumber(http.timeoutMs, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
      followRedirects: http.followRedirects !== false,
      skipTlsVerify: Boolean(http.skipTlsVerify),
    };
    if (!BODY_CONTENT_TYPES[state.http.bodyType] && state.http.bodyType !== "none") state.http.bodyType = "none";
    const grpc = snapshot.grpc && typeof snapshot.grpc === "object" ? snapshot.grpc : {};
    state.grpc = {
      ...defaultGrpc(),
      ...grpc,
      target: typeof grpc.target === "string" ? grpc.target : "",
      service: typeof grpc.service === "string" ? grpc.service : "",
      method: typeof grpc.method === "string" ? grpc.method : "",
      message: typeof grpc.message === "string" ? grpc.message : "{\n  \n}",
      metadata: normalizeKV(grpc.metadata),
      timeoutMs: clampNumber(grpc.timeoutMs, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
      tls: Boolean(grpc.tls),
      skipTlsVerify: Boolean(grpc.skipTlsVerify),
      services: [],
      methods: [],
    };
    state.responseTab = snapshot.responseTab === "headers" ? "headers" : "body";
    state.hydrating = false;
    renderAll();
  }

  function snapshot() {
    return {
      protocol: state.protocol,
      http: {
        method: state.http.method,
        url: state.http.url,
        params: state.http.params.map((row) => ({ ...row })),
        headers: state.http.headers.map((row) => ({ ...row })),
        bodyType: state.http.bodyType,
        bodyText: state.http.bodyText,
        timeoutMs: state.http.timeoutMs,
        followRedirects: state.http.followRedirects,
        skipTlsVerify: state.http.skipTlsVerify,
      },
      grpc: {
        target: state.grpc.target,
        tls: state.grpc.tls,
        skipTlsVerify: state.grpc.skipTlsVerify,
        service: state.grpc.service,
        method: state.grpc.method,
        metadata: state.grpc.metadata.map((row) => ({ ...row })),
        message: state.grpc.message,
        timeoutMs: state.grpc.timeoutMs,
      },
      responseTab: state.responseTab,
    };
  }

  function summarize(payload) {
    if (payload.protocol === "grpc") {
      const grpc = payload.grpc || {};
      if (grpc.service && grpc.method) return truncate(`${grpc.service}/${grpc.method}`, 54);
      return truncate(grpc.method || grpc.target || t("untitled"), 54);
    }
    const http = payload.http || {};
    let path = http.url || "";
    try {
      const parsed = new URL(path);
      path = `${parsed.pathname}${parsed.search}`;
    } catch {
      void 0;
    }
    return truncate(`${(http.method || "GET").toUpperCase()} ${path}`.trim(), 54);
  }

  function suggestName(payload) {
    if (payload.protocol === "grpc") {
      const grpc = payload.grpc || {};
      if (grpc.service && grpc.method) return `${grpc.service}/${grpc.method}`;
      return grpc.target || t("untitled");
    }
    const http = payload.http || {};
    try {
      const parsed = new URL(http.url);
      return `${http.method || "GET"} ${parsed.pathname || "/"}`;
    } catch {
      return truncate(http.url || `${http.method || "GET"} request`, 48);
    }
  }

  let draftTimer = null;

  function persistDraft() {
    if (state.hydrating) return;
    clearTimeout(draftTimer);
    draftTimer = setTimeout(async () => {
      const payload = snapshot();
      try {
        if (JSON.stringify(payload).length > DRAFT_LIMIT) return;
      } catch {
        return;
      }
      await storage.set("draft", payload);
    }, 450);
  }

  function savedKey(id) {
    return `saved.entry.${id}`;
  }

  function historyKey(id) {
    return `history.entry.${id}`;
  }

  async function readIndex(key) {
    const value = await storage.get(key);
    return Array.isArray(value) ? value.filter((item) => item && typeof item.id === "string") : [];
  }

  function renderSidebar() {
    renderSideList(els.savedList, state.savedIndex, "saved");
    renderSideList(els.historyList, state.historyIndex, "history");
    els.storageHint.textContent = storageAvailable ? "" : t("storageUnavailable");
  }

  function renderSideList(container, index, kind) {
    clear(container);
    if (!index.length) {
      container.append(create("div", "side-empty", t(kind === "saved" ? "savedEmpty" : "historyEmpty")));
      return;
    }
    for (const item of index) {
      const row = create("div", "side-item");
      row.dataset.id = item.id;
      row.dataset.kind = kind;
      row.tabIndex = 0;
      row.setAttribute("role", "button");
      if (kind === "saved" && item.id === state.activeSavedId) row.classList.add("is-active");
      const top = create("div", "side-item-top");
      top.append(create("span", "proto-badge", item.protocol === "grpc" ? "GRPC" : "HTTP"));
      top.append(create("span", "side-name", item.name || t("untitled")));
      if (kind === "saved") {
        const remove = create("button", "icon-btn side-delete", "×");
        remove.type = "button";
        remove.title = t("delete");
        remove.dataset.delete = item.id;
        top.append(remove);
      }
      row.append(top);
      row.append(create("div", "side-sub", item.summary || ""));
      container.append(row);
    }
  }

  function renderProtocol() {
    els.protocolTabs.querySelectorAll("[data-protocol]").forEach((button) => {
      button.classList.toggle("is-active", button.dataset.protocol === state.protocol);
    });
    els.httpEditor.classList.toggle("is-hidden", state.protocol !== "http");
    els.grpcEditor.classList.toggle("is-hidden", state.protocol !== "grpc");
    els.editorTitle.textContent = state.activeName || t("untitled");
  }

  function renderHttpForm() {
    httpMethodMenu.setItems([...new Set([...HTTP_METHODS, state.http.method])]);
    httpMethodMenu.setValue(state.http.method);
    els.httpUrl.value = state.http.url;
    els.httpTimeout.value = state.http.timeoutMs;
    els.httpFollow.checked = state.http.followRedirects !== false;
    els.httpSkipTls.checked = Boolean(state.http.skipTlsVerify);
    els.httpBodyText.value = state.http.bodyText;
    renderKV(els.httpParamsEditor, state.http.params, {
      namePlaceholder: t("key"),
      valuePlaceholder: t("value"),
      onChange: handleHttpChange,
    });
    renderKV(els.httpHeadersEditor, state.http.headers, {
      namePlaceholder: t("key"),
      valuePlaceholder: t("value"),
      onChange: handleHttpChange,
    });
    renderBodyType();
    renderEffectiveUrl();
  }

  function renderGrpcForm() {
    els.grpcTarget.value = state.grpc.target;
    els.grpcTls.checked = Boolean(state.grpc.tls);
    els.grpcSkipTls.checked = Boolean(state.grpc.skipTlsVerify);
    els.grpcSkipTls.disabled = !state.grpc.tls;
    els.grpcTimeout.value = state.grpc.timeoutMs;
    els.grpcMessage.value = state.grpc.message;
    updateGrpcMessageHighlight();
    renderKV(els.grpcMetadataEditor, state.grpc.metadata, {
      namePlaceholder: t("key"),
      valuePlaceholder: t("value"),
      onChange: handleGrpcChange,
    });
    renderGrpcServices();
    renderGrpcMethods();
    renderGrpcSignature();
  }

  function renderGrpcServices() {
    const services = state.grpc.services.slice();
    if (state.grpc.service && !services.includes(state.grpc.service)) services.unshift(state.grpc.service);
    if (!services.length) {
      grpcServiceMenu.setItems([{ value: "", label: t("chooseService"), disabled: true }]);
      grpcServiceMenu.setValue("");
      grpcServiceMenu.setDisabled(true);
      return;
    }
    grpcServiceMenu.setDisabled(false);
    grpcServiceMenu.setItems(services.map((name) => ({ value: name, label: name })));
    if (!state.grpc.service) state.grpc.service = services[0];
    grpcServiceMenu.setValue(state.grpc.service);
  }

  function renderGrpcMethods() {
    let names = state.grpc.methods.map((method) => method.name);
    if (state.grpc.method && !names.includes(state.grpc.method)) names = [state.grpc.method, ...names];
    if (!names.length) {
      grpcMethodMenu.setItems([{ value: "", label: t("discoverFirst"), disabled: true }]);
      grpcMethodMenu.setValue("");
      grpcMethodMenu.setDisabled(true);
      return;
    }
    grpcMethodMenu.setDisabled(false);
    grpcMethodMenu.setItems(names.map((name) => ({ value: name, label: name })));
    if (!state.grpc.method) state.grpc.method = names[0];
    grpcMethodMenu.setValue(state.grpc.method);
  }

  function renderGrpcSignature() {
    if (!state.grpc.method) {
      els.grpcSignature.textContent = "";
      return;
    }
    const info = state.grpc.methods.find((method) => method.name === state.grpc.method);
    if (!info) {
      els.grpcSignature.textContent = state.grpc.service ? `${state.grpc.service}/${state.grpc.method}` : state.grpc.method;
      return;
    }
    const kind = info.clientStreaming ? t("clientStreaming") : info.serverStreaming ? t("serverStreaming") : t("unary");
    els.grpcSignature.textContent = `${info.inputType} → ${info.outputType} · ${kind}`;
  }

  function renderAll() {
    renderProtocol();
    renderHttpForm();
    renderGrpcForm();
    renderSidebar();
    renderResponse();
  }

  function handleHttpChange() {
    renderEffectiveUrl();
    persistDraft();
  }

  function handleGrpcChange() {
    renderGrpcSignature();
    persistDraft();
  }

  function statusTone(value) {
    if (value >= 500) return "err";
    if (value >= 400) return "warn";
    if (value >= 300) return "info";
    return "ok";
  }

  function addBadge(text, tone, title) {
    const badge = create("span", `pill${tone ? ` pill--${tone}` : ""}`, text);
    if (title) badge.title = title;
    els.responseBadges.append(badge);
  }

  function addMeta(text) {
    els.responseBadges.append(create("span", "meta", text));
  }

  function renderResponse() {
    clear(els.responseBadges);
    clear(els.responseBody);
    els.responseTabs.querySelectorAll("[data-rtab]").forEach((button) => {
      button.classList.toggle("is-active", button.dataset.rtab === state.responseTab);
    });
    const response = state.response;
    const headersTab = els.responseTabs.querySelector('[data-rtab="headers"]');
    headersTab.textContent = response && response.protocol === "grpc" ? t("metadata") : t("headers");

    if (!response) {
      const empty = create("div", "response-empty");
      empty.append(create("strong", null, t("emptyResponse")));
      empty.append(create("span", null, t("emptyResponseHint")));
      els.responseBody.append(empty);
      return;
    }

    if (response.kind === "loading") {
      addBadge(t("loading"), "info");
      const loading = create("div", "response-loading");
      loading.append(create("span", "spinner"));
      loading.append(create("span", null, t("loading")));
      els.responseBody.append(loading);
      return;
    }

    if (response.kind === "error") {
      addBadge(t("requestFailed"), "err");
      if (Number.isFinite(response.elapsedMs)) addMeta(`${response.elapsedMs} ms`);
      const box = create("div", "response-error");
      box.append(create("div", "response-error-title", t("requestFailed")));
      box.append(create("pre", "response-error-code", response.error || ""));
      const hint = errorHint(response.error);
      if (hint) box.append(create("div", "hint", hint));
      els.responseBody.append(box);
      return;
    }

    if (response.protocol === "http") {
      renderHttpResult(response.data);
    } else {
      renderGrpcResult(response.data);
    }
  }

  function errorHint(message) {
    const text = String(message || "").toLowerCase();
    if (!text) return "";
    if (text.includes("timed out") || text.includes("timeout") || text.includes("deadline")) return t("hintTimeout");
    if (text.includes("certificate") || text.includes("x509") || text.includes("tls") || text.includes("handshake")) return t("hintTls");
    if (text.includes("reflection")) return t("hintReflection");
    if (text.includes("no such host") || text.includes("dns") || text.includes("resolve host")) return t("hintDns");
    if (text.includes("connection refused") || text.includes("unavailable") || text.includes("cannot connect") || text.includes("connection")) return t("hintConnect");
    if (text.includes("unauthenticated") || text.includes("permission")) return t("hintAuth");
    return "";
  }

  function prettyJSON(text) {
    const trimmed = String(text || "").trim();
    if (!trimmed) return "";
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2);
    } catch {
      return text;
    }
  }

  function codeBlock(text, json) {
    const node = create("pre", "code-block");
    if (json) node.innerHTML = highlightJSON(text);
    else node.textContent = text;
    return node;
  }

  function renderHeaderTable(entries) {
    if (!Array.isArray(entries) || entries.length === 0) {
      els.responseBody.append(create("div", "side-empty", t("none")));
      return;
    }
    const table = create("table", "data-table");
    const head = document.createElement("thead");
    const headRow = document.createElement("tr");
    headRow.append(create("th", null, t("key")), create("th", null, t("value")));
    head.append(headRow);
    table.append(head);
    const body = document.createElement("tbody");
    for (const entry of entries) {
      const row = document.createElement("tr");
      row.append(create("td", null, entry.name || ""), create("td", null, entry.value || ""));
      body.append(row);
    }
    table.append(body);
    els.responseBody.append(table);
  }

  function renderHttpResult(data) {
    if (!data) return;
    addBadge(`${data.status} ${data.statusText || ""}`.trim(), statusTone(data.status));
    if (Number.isFinite(data.elapsedMs)) addMeta(`${data.elapsedMs} ms`);
    addMeta(formatBytes(data.sizeBytes));
    if (data.truncated) addBadge("truncated", "warn");
    if (Array.isArray(data.redirects) && data.redirects.length) {
      addBadge(`${data.redirects.length} redirect${data.redirects.length > 1 ? "s" : ""}`, "info", data.redirects.map((hop) => `${hop.status} ${hop.url}`).join("\n"));
    }

    if (state.responseTab === "headers") {
      if (Array.isArray(data.redirects) && data.redirects.length) {
        els.responseBody.append(create("div", "section-title", t("redirects")));
        renderHeaderTable(data.redirects.map((hop) => ({ name: String(hop.status), value: hop.url })));
      }
      els.responseBody.append(create("div", "section-title", t("responseHeaders")));
      renderHeaderTable(data.headers);
      return;
    }

    if (data.truncated) {
      els.responseBody.append(create("p", "response-note", t("truncated", { size: formatBytes(data.sizeBytes) })));
    }
    if (data.bodyKind === "binary") {
      els.responseBody.append(create("div", "section-title", t("binaryBody")));
      els.responseBody.append(codeBlock(data.bodyBase64 || ""));
      return;
    }
    if (data.bodyKind === "empty" || !data.bodyText) {
      els.responseBody.append(create("div", "side-empty", t("emptyBody")));
      return;
    }
    const contentType = String(data.contentType || "").toLowerCase();
    const json = contentType.includes("json") || looksLikeJSON(data.bodyText);
    const body = json ? prettyJSON(data.bodyText) : data.bodyText;
    els.responseBody.append(codeBlock(body, json));
  }

  function renderGrpcResult(data) {
    if (!data) return;
    const status = data.status || { name: data.ok ? "OK" : "ERROR", code: data.ok ? 0 : -1 };
    addBadge(status.name || "OK", data.ok ? "ok" : "err");
    if (Number.isFinite(status.code) && status.code !== 0) addMeta(t("codeLabel", { code: status.code }));
    if (Number.isFinite(data.elapsedMs)) addMeta(`${data.elapsedMs} ms`);
    if (Number.isFinite(data.sizeBytes)) addMeta(formatBytes(data.sizeBytes));
    if (data.streaming) addBadge(t("serverStreaming"), "info");
    if (data.truncated) addBadge("truncated", "warn");

    if (state.responseTab === "headers") {
      els.responseBody.append(create("div", "section-title", t("metadata")));
      renderHeaderTable(data.metadata);
      els.responseBody.append(create("div", "section-title", t("trailers")));
      renderHeaderTable(data.trailers);
      return;
    }

    if (!data.ok && status.message) {
      const box = create("div", "response-error");
      box.append(create("div", "response-error-title", `${status.name || t("requestFailed")}${Number.isFinite(status.code) ? ` (${status.code})` : ""}`));
      box.append(create("pre", "response-error-code", status.message));
      const hint = errorHint(status.message);
      if (hint) box.append(create("div", "hint", hint));
      els.responseBody.append(box);
    }

    if (data.truncated) {
      els.responseBody.append(create("p", "response-note", t("streamTruncated")));
    }

    if (Array.isArray(data.messages) && data.messages.length) {
      els.responseBody.append(create("div", "section-title", t("messages")));
      data.messages.forEach((message, index) => {
        const block = create("div", "stream-block");
        block.append(create("div", "stream-index", `#${index + 1}`));
        block.append(codeBlock(prettyJSON(message), true));
        els.responseBody.append(block);
      });
      return;
    }

    if (data.messageJson) {
      els.responseBody.append(codeBlock(prettyJSON(data.messageJson), true));
      return;
    }

    if (!data.ok) return;
    els.responseBody.append(create("div", "side-empty", t("emptyBody")));
  }

  function responseText() {
    const response = state.response;
    if (!response || response.kind !== "done" || !response.data) return "";
    if (response.protocol === "http") {
      const data = response.data;
      return data.bodyKind === "binary" ? data.bodyBase64 || "" : data.bodyText || "";
    }
    const data = response.data;
    if (Array.isArray(data.messages) && data.messages.length) return data.messages.join("\n");
    return data.messageJson || data.status?.message || "";
  }

  async function copyText(text) {
    if (typeof plugin.copy === "function") return plugin.copy(text);
    if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(text);
    throw new Error("Clipboard unavailable");
  }

  async function copyResponse() {
    const text = responseText();
    if (!text) {
      toast(t("nothingToCopy"), "warn");
      return;
    }
    try {
      await copyText(text);
      toast(t("copyDone"), "ok");
    } catch {
      toast(t("copyFailed"), "error");
    }
  }

  function errorMessage(error) {
    if (!error) return t("requestFailed");
    if (typeof error === "string") return error;
    return error.message || String(error);
  }

  async function addHistory(payload) {
    const signature = JSON.stringify({ protocol: payload.protocol, http: payload.http, grpc: payload.grpc });
    if (state.historyIndex[0] && state.historyIndex[0].signature === signature) return;
    const id = uid();
    const stored = await storage.set(historyKey(id), payload);
    if (!stored) return;
    state.historyIndex.unshift({
      id,
      protocol: payload.protocol,
      summary: summarize(payload),
      signature,
      at: Date.now(),
    });
    while (state.historyIndex.length > HISTORY_LIMIT) {
      const removed = state.historyIndex.pop();
      await storage.remove(historyKey(removed.id));
    }
    await storage.set("history.index", state.historyIndex);
    renderSidebar();
  }

  async function saveCurrent(asNew) {
    if (!plugin) return;
    const payload = snapshot();
    let name = state.activeName;
    let id = state.activeSavedId;
    if (asNew || !id || !name) {
      const input = await askName(suggestName(payload));
      if (!input) return;
      name = input;
      id = asNew || !id ? uid() : id;
    }
    const record = { ...payload, name };
    if (new TextEncoder().encode(JSON.stringify(record)).length > SAVE_LIMIT) {
      toast(t("requestTooLarge"), "error");
      return;
    }
    const stored = await storage.set(savedKey(id), record);
    if (!stored) {
      toast(t("saveFailed"), "error");
      return;
    }
    const entry = state.savedIndex.find((item) => item.id === id);
    const nextEntry = {
      id,
      name,
      protocol: payload.protocol,
      summary: summarize(payload),
      updatedAt: Date.now(),
    };
    if (entry) Object.assign(entry, nextEntry);
    else state.savedIndex.unshift(nextEntry);
    await storage.set("saved.index", state.savedIndex);
    state.activeSavedId = id;
    state.activeName = name;
    renderProtocol();
    renderSidebar();
    toast(t("savedToast", { name }), "ok");
    persistDraft();
  }

  async function openSaved(id) {
    const payload = await storage.get(savedKey(id));
    if (!payload) {
      toast(t("loadFailed"), "error");
      return;
    }
    const entry = state.savedIndex.find((item) => item.id === id);
    applySnapshot(payload);
    state.activeSavedId = id;
    state.activeName = entry?.name || payload.name || t("untitled");
    state.response = null;
    renderProtocol();
    renderSidebar();
    renderResponse();
    persistDraft();
  }

  async function openHistory(id) {
    const payload = await storage.get(historyKey(id));
    if (!payload) {
      toast(t("loadFailed"), "error");
      return;
    }
    applySnapshot(payload);
    state.activeSavedId = null;
    state.activeName = "";
    state.response = null;
    renderProtocol();
    renderSidebar();
    renderResponse();
    persistDraft();
  }

  async function deleteSaved(id) {
    const entry = state.savedIndex.find((item) => item.id === id);
    const name = entry?.name || t("untitled");
    const confirmed = await askConfirm(t("deleteTitle"), t("deleteMessage", { name }), t("delete"));
    if (!confirmed) return;
    await storage.remove(savedKey(id));
    state.savedIndex = state.savedIndex.filter((item) => item.id !== id);
    await storage.set("saved.index", state.savedIndex);
    if (state.activeSavedId === id) {
      state.activeSavedId = null;
      state.activeName = "";
      renderProtocol();
    }
    renderSidebar();
    toast(t("deletedToast", { name }), "ok");
  }

  async function clearHistory() {
    if (!state.historyIndex.length) return;
    const confirmed = await askConfirm(t("clearHistoryTitle"), t("clearHistoryMessage"), t("clear"));
    if (!confirmed) return;
    for (const entry of state.historyIndex) await storage.remove(historyKey(entry.id));
    state.historyIndex = [];
    await storage.set("history.index", state.historyIndex);
    renderSidebar();
    toast(t("historyCleared"), "ok");
  }

  function newRequest() {
    state.activeSavedId = null;
    state.activeName = "";
    state.http = defaultHttp();
    state.grpc = defaultGrpc();
    state.response = null;
    state.responseTab = "body";
    renderAll();
    persistDraft();
  }

  function setProtocol(protocol) {
    if (protocol !== "http" && protocol !== "grpc") return;
    state.protocol = protocol;
    renderProtocol();
    persistDraft();
  }

  function updateBusy() {
    els.httpSend.disabled = state.sending;
    els.grpcInvoke.disabled = state.sending || state.discovering;
    els.grpcDiscover.disabled = state.sending || state.discovering;
    els.httpSend.textContent = state.sending ? t("sending") : t("send");
    els.grpcDiscover.textContent = state.discovering ? t("discovering") : t("discover");
  }

  async function sendHttp() {
    if (state.sending || !plugin) return;
    const rawUrl = state.http.url.trim();
    if (!rawUrl) {
      toast(t("needUrl"), "warn");
      els.httpUrl.focus();
      return;
    }
    let url;
    try {
      const parsed = new URL(rawUrl);
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("invalid protocol");
      for (const row of state.http.params) {
        if (row.enabled === false || !row.name.trim()) continue;
        parsed.searchParams.append(row.name.trim(), row.value);
      }
      url = parsed.toString();
    } catch {
      toast(t("invalidUrl"), "error");
      els.httpUrl.focus();
      return;
    }

    const headers = [];
    for (const row of state.http.headers) {
      if (row.enabled === false || !row.name.trim()) continue;
      headers.push({ name: row.name.trim(), value: row.value });
    }
    const contentType = contentTypeForBody(state.http.bodyType);
    if (contentType && !headers.some((header) => header.name.toLowerCase() === "content-type")) {
      headers.push({ name: "Content-Type", value: contentType });
    }

    const payload = snapshot();
    const timeoutMs = clampNumber(state.http.timeoutMs, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX);
    const params = {
      method: state.http.method,
      url,
      headers,
      bodyEncoding: state.http.bodyType === "none" ? "none" : "text",
      bodyText: state.http.bodyType === "none" ? "" : state.http.bodyText,
      timeoutMs,
      followRedirects: state.http.followRedirects !== false,
      skipTlsVerify: Boolean(state.http.skipTlsVerify),
    };
    if (new TextEncoder().encode(params.bodyText).length > BRIDGE_BODY_LIMIT) {
      toast(t("bodyTooLarge"), "error");
      return;
    }

    state.sending = true;
    state.response = { protocol: "http", kind: "loading" };
    state.responseTab = "body";
    renderResponse();
    updateBusy();
    const started = performance.now();
    try {
      const data = await plugin.invoke("http/send", params, { timeoutMs: Math.min(118000, timeoutMs + 5000) });
      state.response = {
        protocol: "http",
        kind: "done",
        data,
        elapsedMs: Math.round(performance.now() - started),
      };
      await addHistory(payload);
    } catch (error) {
      state.response = {
        protocol: "http",
        kind: "error",
        error: errorMessage(error),
        elapsedMs: Math.round(performance.now() - started),
      };
      await addHistory(payload);
    } finally {
      state.sending = false;
      updateBusy();
      renderResponse();
      persistDraft();
    }
  }

  function grpcBaseParams() {
    const metadata = [];
    for (const row of state.grpc.metadata) {
      if (row.enabled === false || !row.name.trim()) continue;
      metadata.push({ name: row.name.trim(), value: row.value });
    }
    return {
      target: state.grpc.target.trim(),
      tls: Boolean(state.grpc.tls),
      skipTlsVerify: Boolean(state.grpc.skipTlsVerify),
      timeoutMs: clampNumber(state.grpc.timeoutMs, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
      metadata,
    };
  }

  async function discoverGrpc() {
    if (state.sending || state.discovering || !plugin) return;
    const base = grpcBaseParams();
    if (!base.target) {
      toast(t("needTarget"), "warn");
      els.grpcTarget.focus();
      return;
    }
    state.discovering = true;
    state.response = { protocol: "grpc", kind: "loading" };
    state.responseTab = "body";
    renderResponse();
    updateBusy();
    try {
      const data = await plugin.invoke("grpc/services", base, { timeoutMs: Math.min(118000, base.timeoutMs + 5000) });
      state.grpc.services = Array.isArray(data?.services) ? data.services : [];
      if (!state.grpc.services.includes(state.grpc.service)) state.grpc.service = state.grpc.services[0] || "";
      renderGrpcServices();
      state.grpc.methods = [];
      renderGrpcMethods();
      renderGrpcSignature();
      if (state.grpc.service) await loadGrpcMethods();
      state.response = null;
      renderResponse();
      toast(state.grpc.services.length ? t("discovered", { count: state.grpc.services.length }) : t("noServices"), state.grpc.services.length ? "ok" : "warn");
    } catch (error) {
      state.response = { protocol: "grpc", kind: "error", error: errorMessage(error) };
      renderResponse();
    } finally {
      state.discovering = false;
      updateBusy();
      persistDraft();
    }
  }

  async function loadGrpcMethods() {
    if (!state.grpc.service || !plugin) return;
    const base = grpcBaseParams();
    if (!base.target) return;
    try {
      const data = await plugin.invoke(
        "grpc/methods",
        { ...base, service: state.grpc.service },
        { timeoutMs: Math.min(118000, base.timeoutMs + 5000) },
      );
      state.grpc.methods = Array.isArray(data?.methods) ? data.methods : [];
      if (!state.grpc.methods.some((method) => method.name === state.grpc.method)) {
        state.grpc.method = state.grpc.methods[0]?.name || "";
      }
      renderGrpcMethods();
      renderGrpcSignature();
    } catch (error) {
      state.grpc.methods = [];
      renderGrpcMethods();
      renderGrpcSignature();
      toast(errorMessage(error), "error");
    }
  }

  async function invokeGrpc() {
    if (state.sending || state.discovering || !plugin) return;
    const base = grpcBaseParams();
    if (!base.target) {
      toast(t("needTarget"), "warn");
      els.grpcTarget.focus();
      return;
    }
    if (!state.grpc.service) {
      toast(t("needService"), "warn");
      return;
    }
    if (!state.grpc.method) {
      toast(t("needMethod"), "warn");
      return;
    }
    let message = {};
    const raw = state.grpc.message.trim();
    if (raw) {
      try {
        message = JSON.parse(raw);
      } catch {
        toast(t("invalidJson"), "error");
        return;
      }
    }

    const payload = snapshot();
    if (new TextEncoder().encode(JSON.stringify(message)).length > BRIDGE_BODY_LIMIT) {
      toast(t("bodyTooLarge"), "error");
      return;
    }
    state.sending = true;
    state.response = { protocol: "grpc", kind: "loading" };
    state.responseTab = "body";
    renderResponse();
    updateBusy();
    const started = performance.now();
    try {
      const data = await plugin.invoke(
        "grpc/invoke",
        { ...base, service: state.grpc.service, method: state.grpc.method, message },
        { timeoutMs: Math.min(118000, base.timeoutMs + 5000) },
      );
      state.response = {
        protocol: "grpc",
        kind: "done",
        data,
        elapsedMs: Math.round(performance.now() - started),
      };
      await addHistory(payload);
    } catch (error) {
      state.response = {
        protocol: "grpc",
        kind: "error",
        error: errorMessage(error),
        elapsedMs: Math.round(performance.now() - started),
      };
      await addHistory(payload);
    } finally {
      state.sending = false;
      updateBusy();
      renderResponse();
      persistDraft();
    }
  }

  function applyI18n() {
    document.documentElement.lang = locale === "zh" ? "zh-CN" : "en";
    document.title = t("appName");
    document.querySelectorAll("[data-i18n]").forEach((node) => {
      node.textContent = t(node.dataset.i18n);
    });
    document.querySelectorAll("[data-i18n-ph]").forEach((node) => {
      node.placeholder = t(node.dataset.i18nPh);
    });
    document.querySelectorAll("[data-i18n-title]").forEach((node) => {
      node.title = t(node.dataset.i18nTitle);
    });
    els.storageHint.textContent = storageAvailable ? "" : t("storageUnavailable");
    els.editorTitle.textContent = state.activeName || t("untitled");
    renderBodyType();
  }

  function renderKVAll() {
    renderKV(els.httpParamsEditor, state.http.params, {
      namePlaceholder: t("key"),
      valuePlaceholder: t("value"),
      onChange: handleHttpChange,
    });
    renderKV(els.httpHeadersEditor, state.http.headers, {
      namePlaceholder: t("key"),
      valuePlaceholder: t("value"),
      onChange: handleHttpChange,
    });
    renderKV(els.grpcMetadataEditor, state.grpc.metadata, {
      namePlaceholder: t("key"),
      valuePlaceholder: t("value"),
      onChange: handleGrpcChange,
    });
  }

  function bindEvents() {
    bindTabs(els.protocolTabs, "protocol", (value) => setProtocol(value));
    bindTabs(els.httpTabs, "http-tab", (value) => showPanel("http", value));
    bindTabs(els.grpcTabs, "grpc-tab", (value) => showPanel("grpc", value));
    bindTabs(els.responseTabs, "rtab", (value) => {
      state.responseTab = value;
      renderResponse();
      persistDraft();
    });

    els.httpUrl.addEventListener("input", () => {
      state.http.url = els.httpUrl.value;
      handleHttpChange();
    });
    els.httpTimeout.addEventListener("input", () => {
      state.http.timeoutMs = clampNumber(els.httpTimeout.value, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX);
      persistDraft();
    });
    els.httpFollow.addEventListener("change", () => {
      state.http.followRedirects = els.httpFollow.checked;
      persistDraft();
    });
    els.httpSkipTls.addEventListener("change", () => {
      state.http.skipTlsVerify = els.httpSkipTls.checked;
      persistDraft();
    });
    els.httpBodyText.addEventListener("input", () => {
      state.http.bodyText = els.httpBodyText.value;
      updateHttpBodyHighlight();
      persistDraft();
    });
    els.httpBodyText.addEventListener("scroll", () => syncEditorScroll(els.httpBodyText, els.httpBodyHighlight));
    els.bodyTypes.addEventListener("click", (event) => {
      const button = event.target.closest("[data-body]");
      if (!button) return;
      state.http.bodyType = button.dataset.body;
      renderBodyType();
      persistDraft();
    });
    els.formatBody.addEventListener("click", () => {
      if (state.http.bodyType !== "json") return;
      const raw = els.httpBodyText.value;
      if (!raw.trim()) return;
      let parsed;
      try {
        parsed = JSON.parse(raw);
      } catch {
        toast(t("invalidJson"), "error");
        return;
      }
      const formatted = JSON.stringify(parsed, null, 2);
      els.httpBodyText.value = formatted;
      state.http.bodyText = formatted;
      updateHttpBodyHighlight();
      persistDraft();
    });
    els.addParam.addEventListener("click", () => {
      state.http.params.push(blankKV());
      renderKV(els.httpParamsEditor, state.http.params, {
        namePlaceholder: t("key"),
        valuePlaceholder: t("value"),
        onChange: handleHttpChange,
      });
      persistDraft();
    });
    els.addHeader.addEventListener("click", () => {
      state.http.headers.push(blankKV());
      renderKV(els.httpHeadersEditor, state.http.headers, {
        namePlaceholder: t("key"),
        valuePlaceholder: t("value"),
        onChange: handleHttpChange,
      });
      persistDraft();
    });
    els.httpSend.addEventListener("click", sendHttp);

    els.grpcTarget.addEventListener("input", () => {
      state.grpc.target = els.grpcTarget.value;
      if (/^(https|grpcs):\/\//i.test(els.grpcTarget.value) && !state.grpc.tls) {
        state.grpc.tls = true;
        els.grpcTls.checked = true;
        els.grpcSkipTls.disabled = false;
      }
      persistDraft();
    });
    els.grpcTls.addEventListener("change", () => {
      state.grpc.tls = els.grpcTls.checked;
      els.grpcSkipTls.disabled = !state.grpc.tls;
      if (!state.grpc.tls) {
        state.grpc.skipTlsVerify = false;
        els.grpcSkipTls.checked = false;
      }
      persistDraft();
    });
    els.grpcSkipTls.addEventListener("change", () => {
      state.grpc.skipTlsVerify = els.grpcSkipTls.checked;
      persistDraft();
    });
    els.grpcTimeout.addEventListener("input", () => {
      state.grpc.timeoutMs = clampNumber(els.grpcTimeout.value, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX);
      persistDraft();
    });
    els.grpcMessage.addEventListener("input", () => {
      state.grpc.message = els.grpcMessage.value;
      updateGrpcMessageHighlight();
      persistDraft();
    });
    els.grpcMessage.addEventListener("scroll", () => syncEditorScroll(els.grpcMessage, els.grpcMessageHighlight));
    els.formatMessage.addEventListener("click", () => {
      const raw = els.grpcMessage.value;
      if (!raw.trim()) return;
      let parsed;
      try {
        parsed = JSON.parse(raw);
      } catch {
        toast(t("invalidJson"), "error");
        return;
      }
      const formatted = JSON.stringify(parsed, null, 2);
      els.grpcMessage.value = formatted;
      state.grpc.message = formatted;
      updateGrpcMessageHighlight();
      persistDraft();
    });
    els.addMetadata.addEventListener("click", () => {
      state.grpc.metadata.push(blankKV());
      renderKV(els.grpcMetadataEditor, state.grpc.metadata, {
        namePlaceholder: t("key"),
        valuePlaceholder: t("value"),
        onChange: handleGrpcChange,
      });
      persistDraft();
    });
    els.grpcDiscover.addEventListener("click", discoverGrpc);
    els.grpcInvoke.addEventListener("click", invokeGrpc);

    els.newButton.addEventListener("click", newRequest);
    els.saveButton.addEventListener("click", () => void saveCurrent(false));
    els.saveAsButton.addEventListener("click", () => void saveCurrent(true));
    els.clearHistory.addEventListener("click", () => void clearHistory());
    els.copyResponse.addEventListener("click", copyResponse);

    els.savedList.addEventListener("click", (event) => {
      const remove = event.target.closest("[data-delete]");
      if (remove) {
        event.stopPropagation();
        void deleteSaved(remove.dataset.delete);
        return;
      }
      const row = event.target.closest(".side-item");
      if (row) void openSaved(row.dataset.id);
    });
    els.savedList.addEventListener("keydown", (event) => {
      if (event.key !== "Enter") return;
      const row = event.target.closest(".side-item");
      if (row) void openSaved(row.dataset.id);
    });
    els.historyList.addEventListener("click", (event) => {
      const row = event.target.closest(".side-item");
      if (row) void openHistory(row.dataset.id);
    });
    els.historyList.addEventListener("keydown", (event) => {
      if (event.key !== "Enter") return;
      const row = event.target.closest(".side-item");
      if (row) void openHistory(row.dataset.id);
    });

    els.modalCancel.addEventListener("click", () => closeModal(modal && modal.withInput ? null : false));
    els.modalConfirm.addEventListener("click", () => {
      if (!modal) return;
      if (modal.withInput) {
        const value = els.modalInput.value.trim();
        closeModal(value || null);
      } else {
        closeModal(true);
      }
    });
    els.modalInput.addEventListener("keydown", (event) => {
      if (event.key !== "Enter") return;
      event.preventDefault();
      const value = els.modalInput.value.trim();
      closeModal(value || null);
    });
    els.modalBackdrop.addEventListener("click", (event) => {
      if (event.target === els.modalBackdrop) closeModal(modal && modal.withInput ? null : false);
    });

    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && modal) {
        event.preventDefault();
        closeModal(modal.withInput ? null : false);
        return;
      }
      if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
        event.preventDefault();
        if (state.protocol === "http") void sendHttp();
        else void invokeGrpc();
      }
    });

    window.addEventListener("dbx-plugin-env", (event) => {
      const next = detectLocale(event.detail?.locale || locale);
      if (next === locale) return;
      locale = next;
      applyI18n();
      renderKVAll();
      renderSidebar();
      renderGrpcForm();
      renderResponse();
    });
  }

  async function init() {
    if (!plugin || !plugin.ready) {
      els.responseBody.append(create("div", "response-empty", "This workbench must run inside DBX."));
      return;
    }
    await plugin.ready;
    locale = detectLocale(plugin.locale);
    storageAvailable = Boolean(
      plugin.storage &&
        typeof plugin.storage.get === "function" &&
        typeof plugin.storage.set === "function" &&
        typeof plugin.storage.delete === "function" &&
        plugin.capabilities?.storage !== false,
    );
    state.savedIndex = await readIndex("saved.index");
    state.historyIndex = await readIndex("history.index");
    bindEvents();
    applyI18n();
    const draft = await storage.get("draft");
    if (draft && typeof draft === "object") {
      applySnapshot(draft);
    } else {
      renderAll();
    }
  }

  init().catch((error) => {
    console.error(error);
    toast(errorMessage(error), "error");
  });
})();
