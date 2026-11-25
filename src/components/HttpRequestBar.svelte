<script>
  /**
   * HTTP 请求行（方法 / URL / 发送）。
   *
   * 单独拆出来是为了让它占满整行：下面的请求体、响应体才做左右分栏，
   * 方法、URL 这类"接口信息"不应该被响应栏挤窄。
   */
  import SelectMenu from "./SelectMenu.svelte";
  import { HTTP_METHODS } from "../lib/model.js";
  import { cancelCurrentRequest, patchHttp, sendHttp } from "../lib/store.js";

  let { t, http, sending = false, canceling = false } = $props();

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
  {#if sending}
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
