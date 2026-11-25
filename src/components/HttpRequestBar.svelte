<script>
  /**
   * HTTP 请求行（方法 / URL / 发送）。
   *
   * 单独拆出来是为了让它占满整行：下面的请求体、响应体才做左右分栏，
   * 方法、URL 这类"接口信息"不应该被响应栏挤窄。
   */
  import SelectMenu from "./SelectMenu.svelte";
  import { HTTP_METHODS } from "../lib/model.js";
  import { patchHttp, sendHttp } from "../lib/store.js";

  let { t, http, sending = false } = $props();

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
      void sendHttp();
    }}
  />
  <button class="btn btn-primary" type="button" disabled={sending} onclick={() => void sendHttp()}>
    {sending ? t("sending") : t("send")}
  </button>
</div>
