<script>
  /**
   * HTTP 请求行（方法 / URL / 发送）。
   *
   * 单独拆出来是为了让它占满整行：下面的请求体、响应体才做左右分栏，
   * 方法、URL 这类"接口信息"不应该被响应栏挤窄。
   *
   * 下载入口不在这里：只有拿到文件响应之后才知道该不该给下载，见 ResponsePanel。
   */
  import SelectMenu from "./SelectMenu.svelte";
  import { HTTP_METHODS } from "../lib/model.js";
  import { cancelCurrentRequest, cancelDownload, patchHttp, sendHttp } from "../lib/store.js";

  let { t, http, sending = false, canceling = false, download = null } = $props();

  /** 下载中也算 running（sending 同样为 true），但它有自己的取消通道。 */
  const downloading = $derived(Boolean(download));

  const methodItems = $derived(
    [...new Set([...HTTP_METHODS, http.method])].map((method) => ({ value: method, label: method })),
  );
</script>

<div class="request-bar">
  <SelectMenu
    items={methodItems}
    value={http.method}
    ariaLabel="HTTP method"
    onchange={(value) => patchHttp({ method: value })}
  />
  <input
    class="input url-input"
    type="text"
    spellcheck="false"
    placeholder={t("urlPlaceholder")}
    aria-label="URL"
    value={http.url}
    oninput={(event) => patchHttp({ url: event.currentTarget.value })}
    onkeydown={(event) => {
      if (event.key !== "Enter") return;
      event.preventDefault();
      if (!sending) void sendHttp();
    }}
  />
  <!-- 下载时 sending 同样为 true，必须先判 download，否则点的是"终止"而不是"取消下载"。 -->
  {#if downloading}
    <button
      class="btn btn-danger"
      type="button"
      title={t("stopDownload")}
      disabled={download.canceling}
      onclick={() => void cancelDownload()}
    >
      {download.canceling ? t("stopping") : t("stopDownload")}
    </button>
  {:else if sending}
    <!-- 请求可能长时间没有响应：发送中的按钮位置换成终止，避免误点重复发送。 -->
    <button
      class="btn btn-danger"
      type="button"
      title={t("stop")}
      disabled={canceling}
      onclick={() => void cancelCurrentRequest()}
    >
      {canceling ? t("stopping") : t("stop")}
    </button>
  {:else}
    <button class="btn btn-primary" type="button" onclick={() => void sendHttp()}>
      {t("send")}
    </button>
  {/if}
</div>
