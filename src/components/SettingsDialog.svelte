<script>
  /**
   * 请求设置弹窗。
   *
   * 只放全局默认值：新建请求会带着这些超时开始，单个请求仍然可以在「选项」里
   * 覆盖。把它与请求表单分开，避免"改了一个请求的超时"被误解成全局设置。
   */
  import { untrack } from "svelte";
  import { HTTP_TIMEOUT_DEFAULT, GRPC_TIMEOUT_DEFAULT, TIMEOUT_MAX, clampNumber } from "../lib/model.js";
  import { updateSettings } from "../lib/store.js";

  let { t, settings, onclose } = $props();

  // 弹窗每次打开都是新实例，初值只取一次；之后的编辑不能被父组件重渲染覆盖。
  let httpTimeout = $state(untrack(() => settings.httpTimeoutMs));
  let grpcTimeout = $state(untrack(() => settings.grpcTimeoutMs));
  let dialog = $state(null);
  let firstInput = $state(null);

  $effect(() => {
    dialog?.showModal();
    firstInput?.focus();
    firstInput?.select();
  });

  function save() {
    updateSettings({
      httpTimeoutMs: clampNumber(httpTimeout, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
      grpcTimeoutMs: clampNumber(grpcTimeout, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
    });
    onclose?.();
  }

  function restoreDefaults() {
    httpTimeout = HTTP_TIMEOUT_DEFAULT;
    grpcTimeout = GRPC_TIMEOUT_DEFAULT;
  }

  function close() {
    onclose?.();
  }

  function onKeydown(event) {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== "Enter" || event.target.tagName === "TEXTAREA") return;
    event.preventDefault();
    save();
  }
</script>

<dialog
  class="modal-backdrop"
  bind:this={dialog}
  oncancel={(event) => {
    event.preventDefault();
    close();
  }}
  onclick={(event) => {
    if (event.target === dialog) close();
  }}
>
  <div class="modal" role="dialog" tabindex="-1" aria-modal="true" aria-labelledby="settings-title" onkeydown={onKeydown}>
    <h2 id="settings-title">{t("settingsTitle")}</h2>
    <p>{t("settingsHint")}</p>

    <div class="modal-field">
      <span class="modal-label">{t("defaultTimeoutHttp")}</span>
      <input
        class="input"
        type="number"
        min="1"
        max={TIMEOUT_MAX}
        step="1000"
        bind:value={httpTimeout}
        bind:this={firstInput}
        aria-label={t("defaultTimeoutHttp")}
      />
    </div>

    <div class="modal-field">
      <span class="modal-label">{t("defaultTimeoutGrpc")}</span>
      <input
        class="input"
        type="number"
        min="1"
        max={TIMEOUT_MAX}
        step="1000"
        bind:value={grpcTimeout}
        aria-label={t("defaultTimeoutGrpc")}
      />
    </div>

    <p class="hint">{t("timeoutRangeHint", { max: TIMEOUT_MAX })}</p>

    <div class="modal-actions">
      <button class="btn btn-ghost" type="button" onclick={restoreDefaults}>{t("resetDefaults")}</button>
      <span class="grow"></span>
      <button class="btn btn-ghost" type="button" onclick={close}>{t("cancel")}</button>
      <button class="btn btn-primary" type="button" onclick={save}>{t("save")}</button>
    </div>
  </div>
</dialog>

<style>
  .grow {
    flex: 1;
  }

  .hint {
    margin: 0;
    text-align: left;
  }
</style>
