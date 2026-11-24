<script>
  /**
   * 通用模态框。
   *
   * 支持三种用途，由传参组合决定：
   * - 纯确认（只给 title / message，返回 true 或 false）
   * - 输入名称（input: true，返回 `{ name }`）
   * - 选择目录（folder: true，返回 `{ folder }`），可与输入组合用于保存/另存为
   *
   * 校验函数返回错误文案时留在框内提示，不关闭。
   */
  import { untrack } from "svelte";
  import SelectMenu from "./SelectMenu.svelte";
  import { NEW_FOLDER, normalizeFolder } from "../lib/model.js";

  let {
    t,
    title = "",
    message = "",
    input = false,
    inputLabel = "",
    inputValue = "",
    placeholder = "",
    folder = false,
    folderLabel = "",
    folderValue = "",
    folders = [],
    confirmLabel = "",
    danger = false,
    validate = null,
    onresolve,
  } = $props();

  // 初始值只在挂载时取一次（untrack 明确这一意图）：模态框每次打开都是新实例，
  // 之后的编辑不应该被父组件传入的初值覆盖。
  let name = $state(untrack(() => inputValue) || "");
  let folderSelection = $state(untrack(() => folderValue) || "");
  let newFolderName = $state("");
  let error = $state("");
  let dialog = $state(null);
  let inputEl = $state(null);
  let folderTrigger = $state(null);
  let confirmEl = $state(null);

  const creatingFolder = $derived(folderSelection === NEW_FOLDER);
  const folderItems = $derived([
    { value: "", label: t("ungrouped") },
    ...folders.map((item) => ({ value: item, label: item })),
    { value: NEW_FOLDER, label: t("newFolderOption") },
  ]);

  $effect(() => {
    dialog?.showModal();
    // 依用途决定初始焦点：输入框优先，其次目录触发器，最后确认按钮。
    if (input) {
      inputEl?.focus();
      inputEl?.select();
    } else if (folder) {
      folderTrigger?.focus();
    } else {
      confirmEl?.focus();
    }
  });

  function value() {
    if (!input && !folder) return true;
    const result = {};
    if (input) result.name = name.trim();
    if (folder) {
      result.folder = normalizeFolder(creatingFolder ? newFolderName : folderSelection);
    }
    return result;
  }

  function confirm() {
    const result = value();
    const failure = validate ? validate(result, { creatingFolder }) : "";
    if (failure) {
      error = failure;
      return;
    }
    onresolve?.(result);
  }

  function cancel() {
    onresolve?.(input || folder ? null : false);
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

    {#if folder}
      <div class="modal-field">
        {#if folderLabel}
          <span class="modal-label">{folderLabel}</span>
        {/if}
        <div bind:this={folderTrigger}>
          <SelectMenu
            items={folderItems}
            value={folderSelection}
            ariaLabel={folderLabel || t("saveLocationLabel")}
            onchange={(next) => {
              folderSelection = next;
              error = "";
              if (next !== NEW_FOLDER) newFolderName = "";
            }}
          />
        </div>
        {#if creatingFolder}
          <input
            class="input"
            type="text"
            bind:value={newFolderName}
            placeholder={t("newFolderPlaceholder")}
            aria-label={t("newFolderLabel")}
            oninput={() => (error = "")}
          />
        {/if}
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
