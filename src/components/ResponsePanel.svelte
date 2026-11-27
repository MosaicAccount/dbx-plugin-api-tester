<script>
  /**
   * 响应面板。
   *
   * 四种状态：空 / 加载中 / 出错 / 完成。完成态再按协议分 HTTP 与 gRPC 两套渲染。
   * 徽标（状态码、耗时、大小、截断等）由这里统一生成，避免各分支各写一遍。
   */
  import { highlightJSON, presentBody, prettyJSON } from "../lib/format.js";
  import {
    ERROR_HINT_KEYS,
    dispositionFileName,
    fileNameFromUrl,
    formatBytes,
    headerValue,
    isDownloadResponse,
    responseFileSizeBytes,
    statusTone,
  } from "../lib/model.js";
  import { cancelCurrentRequest, cancelDownload, copyResponse, downloadResponse, setResponseTab } from "../lib/store.js";

  let {
    t,
    response = null,
    responseTab = "body",
    protocol = "http",
    stream = null,
    canceling = false,
    download = null,
    canDownload = false,
  } = $props();

  /**
   * 交互式会话期间，右栏显示的是「时间线」而不是一次性响应：
   * 发出与收到按到达顺序排列，服务端的回复不挂在某条消息下面。
   */
  const timeline = $derived(Array.isArray(stream?.timeline) ? stream.timeline : []);
  /** 会话还在（连接中/已连接/半关闭）时即使还没有消息也要显示时间线，避免看起来像"没发起请求"。 */
  const streamActive = $derived(["connecting", "open", "halfClosed"].includes(stream?.state));
  const showTimeline = $derived(protocol === "grpc" && (timeline.length > 0 || streamActive));
  const streamStateLabel = $derived.by(() => {
    switch (stream?.state) {
      case "connecting":
        return t("connecting");
      case "open":
        return stream?.kind === "server" ? t("streamReceiving") : t("streamOpen");
      case "halfClosed":
        // 客户端流是"等那一条响应"，双向流是"继续收"。
        return stream?.kind === "client" ? t("streamWaitingReply") : t("streamHalfClosed");
      case "ended":
        return t("streamEnded");
      case "error":
        return t("streamError");
      default:
        return "";
    }
  });

  // 等待回复时每秒刷新一次，让用户看到"已经等了多久"，而不是干等着转圈。
  let now = $state(Date.now());
  $effect(() => {
    if (stream?.state !== "halfClosed") return;
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(timer);
  });
  const waitedSeconds = $derived(
    stream?.waitingSince ? Math.max(0, Math.round((now - stream.waitingSince) / 1000)) : 0,
  );

  /** 顶部计数：发送 / 收到。服务端流没有"已发送"的概念，只显示收到的条数。 */
  const counts = $derived.by(() => {
    const sent = Number(stream?.outSeq) || 0;
    const received = Number(stream?.inSeq) || 0;
    // 服务端流的请求在 open 时随建流发出，不存在"已发送 N 条"的概念。
    if (stream?.kind === "server") return received ? t("streamReceivedCount", { count: received }) : "";
    if (sent && received) return t("streamCounts", { sent, received });
    if (sent) return t("streamSentCount", { count: sent });
    if (received) return t("streamReceivedCount", { count: received });
    return "";
  });

  /** 时间线正文的语法高亮缓存：条目对象引用稳定，按对象缓存即可，避免每次重渲染都重新高亮。 */
  const bodyCache = new Map();
  /** 超过这个长度的正文不做高亮：逐字符扫描太贵，长消息直接按纯文本展示。 */
  const HIGHLIGHT_LIMIT = 200000;

  function bodyHTML(entry) {
    if (bodyCache.has(entry)) return bodyCache.get(entry);
    const text = String(entry.text ?? "");
    let html = null;
    if (text && text.length <= HIGHLIGHT_LIMIT) {
      try {
        html = highlightJSON(prettyJSON(text));
      } catch {
        html = null;
      }
    }
    if (bodyCache.size > 800) bodyCache.clear();
    bodyCache.set(entry, html);
    return html;
  }

  /** 相对会话开始的时间：1 秒以内用毫秒，之后用秒并保留一位小数。 */
  function formatAt(ms) {
    const value = Number(ms) || 0;
    if (value <= 0) return "";
    if (value < 1000) return `${value} ms`;
    return `${(value / 1000).toFixed(value < 10000 ? 1 : 0)} s`;
  }

  /** 方向标签：发送用「发送」，收到用「收到」。 */
  function entryTag(entry) {
    return entry.type === "out" ? t("sendMessage") : t("streamReceived");
  }

  // 响应面板必须用"响应"语义的标签：请求面板已经占用了「请求体 / 请求头」，
  // 这里沿用同一组文案会让人以为看到的是刚刚填的请求。
  // 还没有响应时用当前请求协议判断，gRPC 下也能显示「响应元数据」。
  const activeProtocol = $derived(response?.protocol || protocol);
  const headersTabLabel = $derived(activeProtocol === "grpc" ? t("responseMetadata") : t("responseHeadersTab"));

  const hint = $derived.by(() => {
    if (response?.kind !== "error") return "";
    const key = ERROR_HINT_KEYS[response.errorKind];
    return key ? t(key) : "";
  });

  /**
   * 解析 / 发现描述信息失败时不要写成「请求失败」：用户点的是「解析文件」，
   * 面板却报告请求失败会让人以为调用已经发出去了。
   */
  const failureLabel = $derived(
    t(
      response?.action === "parse"
        ? "parseFailed"
        : response?.action === "discover"
          ? "discoverFailed"
          : "requestFailed",
    ),
  );

  /**
   * 文件响应：正文是二进制，或服务器用 Content-Disposition: attachment 声明了附件。
   *
   * 对文件响应不显示「已截断」：被展示上限截断的只是预览，完整内容由下载重新取回。
   * 顶着一个「已截断」徽标，用户会以为文件本身坏了或下载不完整。
   */
  const fileResponse = $derived(isDownloadResponse(response));

  const httpBadges = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "http" || !data) return [];
    const badges = [{ text: `${data.status} ${data.statusText || ""}`.trim(), tone: statusTone(data.status) }];
    if (data.truncated && !fileResponse) badges.push({ text: t("truncatedBadge"), tone: "warn" });
    if (Array.isArray(data.redirects) && data.redirects.length) {
      badges.push({
        text: t("redirectBadge", { count: data.redirects.length }),
        tone: "info",
        title: data.redirects.map((hop) => `${hop.status} ${hop.url}`).join("\n"),
      });
    }
    return badges;
  });

  const httpMeta = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "http" || !data) return [];
    const items = [];
    if (Number.isFinite(data.elapsedMs)) items.push(`${data.elapsedMs} ms`);
    // 文件响应标的是整份文件的大小，和文件卡片、下载按钮保持一致；普通响应标实际收到的
    // 正文大小。两边各标一个数（12 MB 的卡片配 3 MB 的顶部）只会让人怀疑哪个是真的。
    const fileBytes = fileResponse ? responseFileSizeBytes(data) : 0;
    items.push(formatBytes(fileBytes > 0 ? fileBytes : data.sizeBytes));
    return items;
  });

  const grpcBadges = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "grpc" || !data) return [];
    const status = data.status || { name: data.ok ? "OK" : "ERROR", code: data.ok ? 0 : -1 };
    // 主动终止时状态码一定是 Canceled，用中性的「已终止」比红色的 Canceled 更准确。
    const badges = [
      data.canceled ? { text: t("canceledBadge"), tone: "warn" } : { text: status.name || "OK", tone: data.ok ? "ok" : "err" },
    ];
    if (data.streaming) {
      // 旧响应没有 streamKind，按服务端流显示，避免回看历史时徽标消失。
      const label =
        data.streamKind === "client"
          ? t("clientStreaming")
          : data.streamKind === "bidi"
            ? t("bidiStreaming")
            : t("serverStreaming");
      badges.push({ text: label, tone: "info" });
    }
    if (data.truncated) badges.push({ text: t("truncatedBadge"), tone: "warn" });
    return badges;
  });

  const grpcMeta = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "grpc" || !data) return [];
    const items = [];
    const status = data.status || {};
    if (!data.canceled && Number.isFinite(status.code) && status.code !== 0) {
      items.push(t("codeLabel", { code: status.code }));
    }
    if (Number.isFinite(data.elapsedMs)) items.push(`${data.elapsedMs} ms`);
    if (Number.isFinite(data.sizeBytes)) items.push(formatBytes(data.sizeBytes));
    return items;
  });

  const body = $derived(response?.kind === "done" && response.protocol === "http" ? presentBody(response.data) : null);

  /**
   * 二进制响应当成一份文件来展示：名字、大小 + 下载，比一整屏 base64 好懂。
   * 文件名优先取 Content-Disposition，其次退回 URL 的最后一段。
   */
  const binaryFile = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "http" || !data || data.bodyKind !== "binary") return null;
    const name =
      dispositionFileName(headerValue(data.headers, "content-disposition")) ||
      fileNameFromUrl(data.url || data.request?.url);
    const declared = responseFileSizeBytes(data);
    return {
      name,
      sizeText: declared > 0 ? formatBytes(declared) : "",
    };
  });

  /**
   * 下载入口只对「文件响应」出现：二进制正文，或服务器声明了 Attachment。
   *
   * 显示在响应面板而不是发送按钮旁边，是因为在拿到响应之前根本不知道对方返回什么；
   * 正在下载时不显示（进度条自己带取消按钮）。宿主不支持保存文件时保留按钮但禁用，
   * 而不是直接消失：能力位是宿主给的，用户看到灰色图标加提示比"按钮时有时无"好懂。
   */
  const downloadable = $derived(!download && fileResponse);
  /**
   * 下载图标旁的文件大小：整份文件的字节数，不是被展示上限截下的正文大小。
   * 响应头没给 Content-Length 又刚好被截断时算不出来，这时候干脆不显示。
   */
  const downloadFileSize = $derived.by(() => {
    if (!downloadable) return "";
    const bytes = responseFileSizeBytes(response?.data);
    return bytes > 0 ? formatBytes(bytes) : "";
  });

  /**
   * 右上角的下载图标只服务于"没有卡片"的文件响应（比如带 Attachment 的文本）。
   *
   * 二进制响应已经有文件卡片、卡片里就是下载按钮，tab 行再来一个就是同一个动作摆两处。
   */
  const showDownloadChip = $derived(downloadable && !binaryFile);

  /** gRPC 调用失败但拿到了 status 时，同样给一条排错提示。 */
  const grpcHint = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "grpc" || data?.ok || data?.canceled) return "";
    const key = ERROR_HINT_KEYS[data?.errorKind || response?.errorKind];
    return key ? t(key) : "";
  });

  /**
   * 流式保存的进度百分比。
   *
   * 总大小未知（chunked / 压缩后 contentLength 为 -1）时返回 null，进度条改用"来回滑动"
   * 的不确定态——不编一个假进度，免得用户以为下载卡住了。
   */
  const downloadPercent = $derived.by(() => {
    if (!download || !(download.totalBytes > 0)) return null;
    const percent = (Number(download.receivedBytes) / download.totalBytes) * 100;
    return Math.max(0, Math.min(100, percent));
  });

  /** 已收 / 总大小；总大小未知时只显示已收。 */
  const downloadSizeText = $derived.by(() => {
    if (!download) return "";
    const received = formatBytes(Number(download.receivedBytes) || 0);
    if (!(download.totalBytes > 0)) return received;
    return `${received} / ${formatBytes(download.totalBytes)}`;
  });
</script>

<section class="response">
  <div class="response-head">
    <span class="response-title">{t("response")}</span>
    <span class="response-badges">
      {#if response?.kind === "loading"}
        <span class="pill pill--info">{t("loading")}</span>
      {:else if response?.kind === "error"}
        {#if response.errorKind === "canceled"}
          <span class="pill pill--warn">{t("canceledBadge")}</span>
        {:else}
          <span class="pill pill--err">{failureLabel}</span>
        {/if}
        {#if Number.isFinite(response.elapsedMs)}<span class="meta">{response.elapsedMs} ms</span>{/if}
      {:else if response?.protocol === "http"}
        {#each httpBadges as badge (badge.text)}
          <span class="pill pill--{badge.tone}" title={badge.title}>{badge.text}</span>
        {/each}
        {#each httpMeta as item (item)}<span class="meta">{item}</span>{/each}
      {:else if response?.protocol === "grpc"}
        {#each grpcBadges as badge (badge.text)}
          <span class="pill pill--{badge.tone}">{badge.text}</span>
        {/each}
        {#each grpcMeta as item (item)}<span class="meta">{item}</span>{/each}
      {/if}
    </span>
    <span class="grow"></span>
    <button class="btn btn-ghost btn-sm" type="button" onclick={() => void copyResponse()}>{t("copy")}</button>
  </div>

  <div class="tabs tabs--response">
    <button class="tab" class:is-active={responseTab === "body"} type="button" onclick={() => setResponseTab("body")}>
      {t("responseBody")}
    </button>
    <button class="tab" class:is-active={responseTab === "headers"} type="button" onclick={() => setResponseTab("headers")}>
      {headersTabLabel}
    </button>
    <span class="grow"></span>
    {#if showDownloadChip}
      <!-- 文件响应才给下载入口：图标和文件大小合成一个 chip，避免一个图标孤零零地
           挨着一段小字，两者也不用各自猜垂直中心。 -->
      <button
        class="download-chip"
        type="button"
        aria-label={downloadFileSize ? t("downloadFileWithSize", { size: downloadFileSize }) : t("downloadFile")}
        title={downloadFileSize ? t("downloadFileWithSize", { size: downloadFileSize }) : t("downloadFile")}
        disabled={!canDownload}
        onclick={() => void downloadResponse()}
      >
        <svg viewBox="0 0 14 14" width="14" height="14" fill="none" aria-hidden="true">
          <path
            d="M7 2.2v6.1m0 0 2.3-2.3M7 8.3 4.7 6"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
          <path d="M2.4 9.6v1.5c0 .4.3.7.7.7h7.8c.4 0 .7-.3.7-.7V9.6" stroke="currentColor" stroke-width="1.5"
            stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        {#if downloadFileSize}
          <span class="download-chip-size">{downloadFileSize}</span>
        {/if}
      </button>
    {/if}
  </div>

  <div class="response-body">
    {#if download}
      <!-- 清楚地区分「正在写文件」和「已经拿到的响应」：进度放最上面，旧内容照旧在下面。 -->
      <div class="download-progress">
        <div class="download-head">
          <span class="spinner" aria-hidden="true"></span>
          <span class="download-name" title={download.fileName || ""}>{download.fileName || t("downloading")}</span>
          <span class="download-size">{downloadSizeText}</span>
          <span class="grow"></span>
          <button
            class="btn btn-secondary btn-sm"
            type="button"
            disabled={download.canceling}
            onclick={() => void cancelDownload()}
          >
            {download.canceling ? t("stopping") : t("stopDownload")}
          </button>
        </div>
        <div
          class="download-bar"
          class:is-indeterminate={downloadPercent === null}
          role="progressbar"
          aria-label={t("downloading")}
          aria-valuemin={downloadPercent === null ? undefined : 0}
          aria-valuemax={downloadPercent === null ? undefined : download.totalBytes}
          aria-valuenow={downloadPercent === null ? undefined : Number(download.receivedBytes) || 0}
        >
          <span class="download-bar-fill" style:width={downloadPercent === null ? undefined : `${downloadPercent}%`}></span>
        </div>
      </div>
    {/if}

    {#if showTimeline}
      <div class="timeline" role="log" aria-live="polite" aria-label={t("response")}>
        <div class="timeline-head">
          <span class="timeline-dot" data-tone={stream?.state === "error" ? "err" : stream?.state === "ended" ? "done" : "live"} aria-hidden="true"></span>
          <span class="timeline-state" data-tone={stream?.state === "error" ? "err" : stream?.state === "ended" ? "done" : "live"}>
            {streamStateLabel}
          </span>
          {#if stream?.kind}
            <span class="timeline-kind">{stream.kind === "server" ? t("serverStreaming") : stream.kind === "bidi" ? t("bidiStreaming") : t("clientStreaming")}</span>
          {/if}
          {#if counts}
            <span class="timeline-counts">{counts}</span>
          {/if}
          <span class="grow"></span>
          {#if stream?.state === "halfClosed" && waitedSeconds > 0}
            <span class="timeline-wait">{t("streamWaitedSeconds", { seconds: waitedSeconds })}</span>
          {/if}
          {#if stream?.dropped > 0}
            <span class="pill pill--warn">{t("streamDropped", { count: stream.dropped })}</span>
          {/if}
        </div>

        {#if timeline.length === 0}
          <!-- 连接状态已经在上面的头部行里，这里只说明"还没有消息"，不再重复"已连接"。 -->
          <p class="timeline-empty">
            {stream?.kind === "server" ? t("streamWaitingPush") : t("streamNoMessages")}
          </p>
        {:else}
          <ol class="timeline-list">
            {#each timeline as entry, index (index)}
              <li class="tl-item" data-type={entry.type}>
                {#if entry.type === "note"}
                  <div class="tl-note">{entry.text}</div>
                {:else if entry.type === "status"}
                  <!-- 终态不是一条消息：不套消息卡，也不用竖条，只做时间线的收尾行。 -->
                  <div class="tl-final" data-ok={String(Boolean(entry.ok))}>
                    <div class="tl-final-head">
                      <span class="tl-tag" data-type={entry.ok ? "status" : "error"}>
                        {entry.ok ? t("streamStatusOk") : t("streamStatusFailed")}
                      </span>
                      <span class="tl-final-code">{entry.status?.name || "status"}</span>
                      <span class="grow"></span>
                      {#if entry.at}<span class="tl-time">{formatAt(entry.at)}</span>{/if}
                    </div>
                    <!-- 错误原因经常是一整句（transport / connection / desc=…），单独一行换行展示。 -->
                    {#if entry.text}<p class="tl-final-msg">{entry.text}</p>{/if}
                  </div>
                {:else}
                  <div class="tl-card" data-type={entry.type}>
                    <div class="tl-head">
                      <span class="tl-tag" data-type={entry.type}>{entryTag(entry)}</span>
                      <span class="tl-seq">#{entry.seq}</span>
                      <span class="grow"></span>
                      {#if entry.at}<span class="tl-time">{formatAt(entry.at)}</span>{/if}
                    </div>
                    {#if entry.text}
                      {@const html = bodyHTML(entry)}
                      {#if html}
                        <pre class="tl-code">{@html html}</pre>
                      {:else}
                        <pre class="tl-code is-plain">{entry.text}</pre>
                      {/if}
                    {/if}
                  </div>
                {/if}
              </li>
            {/each}
          </ol>
        {/if}
      </div>
    {:else if !response}
      <div class="response-empty">
        <strong>{t("emptyResponse")}</strong>
        <span>{t("emptyResponseHint")}</span>
      </div>
    {:else if response.kind === "loading"}
      <div class="response-loading">
        <span class="spinner"></span>
        <span>{canceling ? t("stopping") : t("loading")}</span>
        <span class="grow"></span>
        <!-- 响应迟迟不回来时，用户不必回到顶部找终止按钮。 -->
        <button
          class="btn btn-secondary btn-sm"
          type="button"
          disabled={canceling}
          onclick={() => void cancelCurrentRequest()}
        >
          {canceling ? t("stopping") : t("stop")}
        </button>
      </div>
    {:else if response.kind === "error"}
      <div class="response-error">
        <div class="response-error-title">
          {response.errorKind === "canceled" ? t("requestCanceled") : failureLabel}
        </div>
        {#if response.errorKind !== "canceled"}
          <pre class="response-error-code">{response.error}</pre>
        {/if}
        {#if hint}<div class="hint">{hint}</div>{/if}
      </div>
    {:else if response.protocol === "http"}
      {@render httpResult(response.data, body)}
    {:else}
      {@render grpcResult(response.data)}
    {/if}
  </div>
</section>

{#snippet headerTable(entries)}
  {#if !Array.isArray(entries) || entries.length === 0}
    <div class="side-empty">{t("none")}</div>
  {:else}
    <table class="data-table">
      <thead>
        <tr><th>{t("key")}</th><th>{t("value")}</th></tr>
      </thead>
      <tbody>
        {#each entries as entry, index (index)}
          <tr><td>{entry.name || ""}</td><td>{entry.value || ""}</td></tr>
        {/each}
      </tbody>
    </table>
  {/if}
{/snippet}

{#snippet httpResult(data, presented)}
  {#if !data}
    <div class="side-empty">{t("emptyBody")}</div>
  {:else if responseTab === "headers"}
    <!-- 这一页就只回答"服务器回了什么头"：不掺请求信息，也不做开发者工具那种「常规」表。 -->
    {#if Array.isArray(data.redirects) && data.redirects.length}
      <div class="section-title">{t("redirects")}</div>
      {@render headerTable(data.redirects.map((hop) => ({ name: String(hop.status), value: hop.url })))}
    {/if}

    <div class="section-title">{t("responseHeaders")}</div>
    {@render headerTable(data.headers)}
  {:else}
    {#if data.bodyKind === "binary"}
      {@const file = binaryFile}
      <!-- 二进制响应当成一份文件来给：图标 + 文件名 + 带大小的下载按钮，
           不铺 base64、不标内容类型，用户在这里只需要决定要不要把文件拿走。 -->
      {#if file}
        <div class="file-card">
          <div class="file-card-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" width="20" height="20" fill="none">
              <path d="M6.5 3.5h7l4 4v13h-11z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
              <path d="M13.5 3.6v3.9h3.9" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
            </svg>
          </div>
          <div class="file-card-main">
            <div class="file-card-name" title={file.name || undefined}>{file.name || t("binaryFile")}</div>
            {#if !canDownload}
              <p class="file-card-hint">{t("downloadUnsupported")}</p>
            {/if}
          </div>
          <div class="file-card-actions">
            <button
              class="file-download"
              type="button"
              disabled={!canDownload || Boolean(download)}
              onclick={() => void downloadResponse()}
            >
              <svg viewBox="0 0 16 16" width="13" height="13" fill="none" aria-hidden="true">
                <path
                  d="M8 2.6v6.9m0 0 2.7-2.7M8 9.5 5.3 6.8"
                  stroke="currentColor"
                  stroke-width="1.6"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
                <path
                  d="M2.9 11v1.2c0 .6.4 1 1 1h8.2c.6 0 1-.4 1-1V11"
                  stroke="currentColor"
                  stroke-width="1.6"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </svg>
              <span>{download ? t("downloading") : t("downloadFile")}</span>
              {#if !download && file.sizeText}
                <span class="file-download-size">{file.sizeText}</span>
              {/if}
            </button>
          </div>
        </div>
      {/if}
    {:else}
      {#if data.truncated}
        <!-- 文件响应不提示「已截断」，只说明正文是预览。 -->
        <p class="response-note">
          {fileResponse
            ? t("previewTruncated", { size: formatBytes(data.sizeBytes) })
            : t("truncated", { size: formatBytes(data.sizeBytes) })}
        </p>
      {/if}
      {#if !presented || presented.variant === "empty"}
        <div class="side-empty">{t("emptyBody")}</div>
      {:else if presented.json}
        <pre class="code-block">{@html highlightJSON(presented.text)}</pre>
      {:else}
        <pre class="code-block">{presented.text}</pre>
      {/if}
    {/if}
  {/if}
{/snippet}

{#snippet grpcResult(data)}
  {#if !data}
    <div class="side-empty">{t("emptyBody")}</div>
  {:else if responseTab === "headers"}
    <div class="section-title">{t("metadata")}</div>
    {@render headerTable(data.metadata)}
    <div class="section-title">{t("trailers")}</div>
    {@render headerTable(data.trailers)}
  {:else}
    {@const status = data.status || { name: data.ok ? "OK" : "ERROR", code: data.ok ? 0 : -1 }}
    {#if data.canceled}
      <!-- 被终止的调用仍然保留已经收到的消息，下面继续渲染。 -->
      <p class="response-note">{t("hintCanceled")}</p>
    {:else if !data.ok && status.message}
      <div class="response-error">
        <div class="response-error-title">
          {status.name || t("requestFailed")}{Number.isFinite(status.code) ? ` (${status.code})` : ""}
        </div>
        <pre class="response-error-code">{status.message}</pre>
        {#if grpcHint}<div class="hint">{grpcHint}</div>{/if}
      </div>
    {/if}
    {#if data.truncated}
      <p class="response-note">{t("streamTruncated")}</p>
    {/if}
    {#if Array.isArray(data.messages) && data.messages.length}
      <div class="section-title">{t("messages")}</div>
      {#each data.messages as message, index (index)}
        <div class="stream-block">
          <div class="stream-index">#{index + 1}</div>
          <pre class="code-block">{@html highlightJSON(prettyJSON(message))}</pre>
        </div>
      {/each}
    {:else if data.messageJson}
      <pre class="code-block">{@html highlightJSON(prettyJSON(data.messageJson))}</pre>
    {:else if data.ok}
      <div class="side-empty">{t("emptyBody")}</div>
    {/if}
  {/if}
{/snippet}

<style>
  /* 会话时间线：一条消息一张卡片，和一元响应的代码块保持同一套观感。 */
  .timeline {
    display: flex;
    flex-direction: column;
    gap: 10px;
    height: 100%;
    padding: 12px 14px 16px;
    overflow: auto;
  }

  .timeline-head {
    position: sticky;
    top: -12px;
    z-index: 1;
    display: flex;
    align-items: center;
    gap: 8px;
    margin: -12px -14px 0;
    padding: 10px 14px;
    background: var(--panel);
    border-bottom: 1px solid var(--border);
    font-size: 11.5px;
    color: var(--muted);
  }

  .timeline-dot {
    width: 7px;
    height: 7px;
    flex: none;
    border-radius: 50%;
    background: var(--muted);
  }

  .timeline-dot[data-tone="live"] {
    background: var(--success);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 18%, transparent);
  }

  .timeline-dot[data-tone="err"] {
    background: var(--danger);
  }

  @media (prefers-reduced-motion: no-preference) {
    .timeline-dot[data-tone="live"] {
      animation: tl-pulse 2.4s ease-in-out infinite;
    }

    @keyframes tl-pulse {
      0%,
      100% {
        opacity: 1;
      }
      50% {
        opacity: 0.4;
      }
    }
  }

  .timeline-state {
    font-weight: 600;
    color: var(--fg);
  }

  .timeline-state[data-tone="err"] {
    color: var(--danger);
  }

  .timeline-kind {
    padding: 1px 6px;
    border: 1px solid var(--border);
    border-radius: 999px;
    font-size: 10.5px;
    color: var(--muted);
  }

  .timeline-counts {
    font-variant-numeric: tabular-nums;
  }

  .timeline-wait {
    font-variant-numeric: tabular-nums;
  }

  .timeline-empty {
    margin: 0;
    padding: 14px 4px;
    color: var(--muted);
    font-size: 12px;
  }

  .timeline-list {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .tl-item {
    margin: 0;
  }

  /* 方向靠左侧细条 + 标签色区分，扫视时不用读文字。 */
  .tl-card {
    border: 1px solid var(--border);
    border-left: 3px solid var(--border);
    border-radius: 8px;
    background: color-mix(in srgb, var(--fg) 3%, var(--panel));
    overflow: hidden;
  }

  .tl-card[data-type="out"] {
    border-left-color: var(--primary);
  }

  .tl-card[data-type="in"] {
    border-left-color: var(--success);
  }

  .tl-card[data-type="error"] {
    border-left-color: var(--danger);
  }

  .tl-head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 10px;
    border-bottom: 1px solid color-mix(in srgb, var(--border) 60%, transparent);
    font-size: 11px;
    color: var(--muted);
  }

  /* 标签/序号/耗时都是单行短内容：禁止被长正文挤窄，否则中文标签会竖排成「状/态」。 */
  .tl-tag {
    flex: none;
    padding: 1px 6px;
    border-radius: 4px;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    white-space: nowrap;
    color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
  }

  .tl-tag[data-type="in"] {
    color: var(--success);
    background: color-mix(in srgb, var(--success) 12%, transparent);
  }

  .tl-tag[data-type="status"] {
    color: var(--success);
    background: color-mix(in srgb, var(--success) 12%, transparent);
  }

  .tl-tag[data-type="error"] {
    color: var(--danger);
    background: color-mix(in srgb, var(--danger) 12%, transparent);
  }

  .tl-seq {
    flex: none;
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
    color: var(--fg);
  }

  .tl-time {
    flex: none;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }

  /* 终态收尾行：成功只是一行灰字，失败才给整块底色，视觉上和"消息"区分开。 */
  .tl-final {
    padding: 8px 10px;
    border: 1px solid color-mix(in srgb, var(--border) 70%, transparent);
    border-radius: 8px;
    background: color-mix(in srgb, var(--fg) 2%, var(--panel));
  }

  .tl-final[data-ok="false"] {
    border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
    border-left: 3px solid var(--danger);
    padding-left: 8px;
    background: color-mix(in srgb, var(--danger) 6%, var(--panel));
  }

  .tl-final-head {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 11px;
    color: var(--muted);
  }

  .tl-final-code {
    flex: none;
    white-space: nowrap;
    font-family: var(--font-mono);
    font-weight: 700;
    color: var(--success);
  }

  .tl-final[data-ok="false"] .tl-final-code {
    color: var(--danger);
  }

  /* 错误原因经常是一整句（transport / desc=…）：独占一行、可换行、超高时内部滚动。 */
  .tl-final-msg {
    margin: 6px 0 0;
    max-height: 180px;
    overflow: auto;
    color: var(--fg);
    font-family: var(--font-mono);
    font-size: 11.2px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .tl-code {
    margin: 0;
    padding: 10px 12px;
    overflow: auto;
    max-height: 320px;
    font-family: var(--font-mono);
    font-size: 11.8px;
    line-height: 1.55;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .tl-code.is-plain {
    color: var(--fg);
  }

  .tl-note {
    padding: 2px 0;
    color: var(--muted);
    font-size: 10.5px;
    letter-spacing: 0.04em;
    text-align: center;
    text-transform: uppercase;
  }

  @media (max-width: 620px) {
    .timeline {
      padding: 10px 10px 14px;
    }

    .timeline-head {
      margin: -10px -10px 0;
      padding: 8px 10px;
    }

    .timeline-kind {
      display: none;
    }
  }

  .grow {
    flex: 1;
  }

  /* 下载入口：图标和文件大小合成一个 chip，和 tab 行共用一套圆角与边框。 */
  .download-chip {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    height: 26px;
    padding: 0 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: color-mix(in srgb, var(--fg) 4%, transparent);
    color: var(--muted);
    cursor: pointer;
    transition:
      color 0.12s ease,
      border-color 0.12s ease,
      background-color 0.12s ease;
  }

  .download-chip:hover {
    border-color: color-mix(in srgb, var(--fg) 24%, transparent);
    background: color-mix(in srgb, var(--fg) 7%, transparent);
    color: var(--fg);
  }

  .download-chip:focus-visible {
    outline: none;
    border-color: var(--primary);
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--primary) 22%, transparent);
  }

  .download-chip:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .download-chip:disabled:hover {
    border-color: var(--border);
    background: color-mix(in srgb, var(--fg) 4%, transparent);
    color: var(--muted);
  }

  .download-chip svg {
    flex: none;
  }

  /* 二进制响应：一行文件卡片，动作只有一个——把文件拿走。 */
  .file-card {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 10px;
    padding: 14px;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: color-mix(in srgb, var(--fg) 3%, var(--panel));
  }

  .file-card-icon {
    display: inline-flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    border-radius: 9px;
    background: color-mix(in srgb, var(--primary) 10%, transparent);
    color: var(--primary);
  }

  .file-card-main {
    flex: 1;
    min-width: 0;
  }

  .file-card-name {
    overflow: hidden;
    font-size: 13px;
    font-weight: 640;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .file-card-hint {
    margin: 4px 0 0;
    color: var(--warning);
    font-size: 11px;
    line-height: 1.5;
  }

  .file-card-actions {
    flex: none;
  }

  /* 文件下载是这一屏唯一的主操作：比其它小按钮高一档，用色块把它和卡片分开。 */
  .file-download {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 30px;
    padding: 0 11px;
    border: none;
    border-radius: 8px;
    background: var(--primary);
    color: var(--primary-fg);
    font-family: var(--font-sans);
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
    box-shadow: 0 1px 2px color-mix(in srgb, var(--primary) 30%, transparent);
    transition:
      background-color 0.12s ease,
      box-shadow 0.12s ease;
  }

  .file-download:hover:not(:disabled) {
    background: color-mix(in srgb, var(--primary) 86%, black);
    box-shadow: 0 2px 6px color-mix(in srgb, var(--primary) 32%, transparent);
  }

  .file-download:focus-visible {
    outline: none;
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--primary) 24%, transparent);
  }

  .file-download:disabled {
    opacity: 0.5;
    cursor: not-allowed;
    box-shadow: none;
  }

  .file-download svg {
    flex: none;
  }

  /* 大小是按钮里的补充信息：用一条分隔线隔开，不用第二个按钮或另起一行。 */
  .file-download-size {
    padding-left: 7px;
    border-left: 1px solid color-mix(in srgb, var(--primary-fg) 32%, transparent);
    font-family: var(--font-mono);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  /* 大小是补充信息：始终比图标弱一档，悬停时也不跟着变亮。 */
  .download-chip-size {
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    line-height: 1;
    white-space: nowrap;
  }
</style>
