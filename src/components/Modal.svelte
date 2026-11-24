<script>
  /**
   * 通用模态框。
   *
   * 支持两种用途，由传参组合决定：
   * - 纯确认（只给 title / message，返回 true 或 false）
   * - 输入名称（input: true，返回 `{ name }`）
   *
   * 校验函数返回错误文案时留在框内提示，不关闭。
   */
  import { untrack } from "svelte";

  let {
    t,
    title = "",
    message = "",
    input = false,
    inputLabel = "",
    inputValue = "",
    placeholder = "",
    confirmLabel = "",
    danger = false,
    validate = null,
    onresolve,
  } = $props();

  // 初始值只在挂载时取一次（untrack 明确这一意图）：模态框每次打开都是新实例，
  // 之后的编辑不应该被父组件传入的初值覆盖。
  let name = $state(untrack(() => inputValue) || "");
  let error = $state("");
  let dialog = $state(null);
  let inputEl = $state(null);
  let confirmEl = $state(null);

  $effect(() => {
    dialog?.showModal();
    // 依用途决定初始焦点：输入框优先，其次目录触发器，最后确认按钮。
    if (input) {
      inputEl?.focus();
      inputEl?.select();
    } else {
      confirmEl?.focus();
    }
  });

  function value() {
    if (!input) return true;
    return { name: name.trim() };
  }

  function confirm() {
    const result = value();
    const failure = validate ? validate(result) : "";
    if (failure) {
      error = failure;
      return;
    }
    onresolve?.(result);
  }

  function cancel() {
    onresolve?.(input ? null : false);
  }

  function onKeydown(event) {
    if (event.key === "Escape") {
      event.preventDefault();
      cancel();
      return;
    }
    if (event.key !== "Enter") return;
    // textarea 里的 Enter 要换行，不能触发确认。
    if (event.target.tagName === "TEXTAREA") return;
    event.preventDefault();
    confirm();
  }
</script>

<dialog
  class="modal-backdrop"
  bind:this={dialog}
  oncancel={(event) => {
    event.preventDefault();
    cancel();
  }}
  onclick={(event) => {
    if (event.target === dialog) cancel();
  }}
>
  <div
    class="modal"
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-labelledby="modal-title"
    onkeydown={onKeydown}
  >
    <h2 id="modal-title">{title}</h2>
    {#if message}
      <p>{message}</p>
    {/if}

    {#if input}
      <div class="modal-field">
        {#if inputLabel}
          <span class="modal-label">{inputLabel}</span>
        {/if}
        <input
          class="input"
          type="text"
          bind:value={name}
          {placeholder}
          aria-label={inputLabel || title}
          bind:this={inputEl}
          oninput={() => (error = "")}
        />
      </div>
    {/if}

    {#if error}
      <p class="modal-error">{error}</p>
    {/if}

    <div class="modal-actions">
      <button class="btn btn-ghost" type="button" onclick={cancel}>{t("cancel")}</button>
      <button
        class="btn {danger ? 'btn-danger' : 'btn-primary'}"
        type="button"
        onclick={confirm}
        bind:this={confirmEl}
      >
        {confirmLabel || t("confirm")}
      </button>
    </div>
  </div>
</dialog>
