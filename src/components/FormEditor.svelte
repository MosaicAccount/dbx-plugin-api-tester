<script>
  /**
   * 表单请求体编辑器（urlencoded / multipart 共用一套行）。
   *
   * `Form` 只发文本字段（application/x-www-form-urlencoded）；`Multipart Form` 下每行
   * 还能在「文本 / 文件」之间切换，一个字段名可以挂多个文件（生成多个同名字段）。
   * 编码方式完全由 bodyType 决定，不再按内容猜测，也没有「强制 multipart」按钮。
   *
   * 文件由宿主分块读出并暂存在 sidecar，快照只保存文件名 / 类型 / 大小；重新打开
   * 请求后需要重新选择。
   */
  import * as host from "../lib/host.js";
  import { formatBytes, formFileCount, multipartTotalBytes } from "../lib/model.js";
  import {
    addFormFiles,
    addFormRow,
    patchFormRow,
    removeFormFile,
    removeFormRow,
    setFormRowKind,
  } from "../lib/store.js";

  let { t, http } = $props();

  let fileInput = $state(null);
  /** 浏览器文件选择器补位时目标行；-1 表示没有待处理选择。 */
  let pendingRowIndex = $state(-1);
  /** 替换目标文件 id；空串表示追加。 */
  let pendingFileId = $state("");
  let pendingMultiple = $state(true);

  const rows = $derived(http.formRows || []);
  const multipart = $derived(http.bodyType === "multipart");
  const fileCount = $derived(formFileCount(rows));
  const fileBytes = $derived(multipartTotalBytes(rows));
  // 空行不进快照（normalizeFormRows 会丢掉没有内容的行），所以每次进入编辑器都要
  // 自己补一行：否则新建请求或重新打开已保存请求时，面板是一片空白，既没有焦点也
  // 看不出该从哪里开始输入。
  $effect(() => {
    if (!rows.length) addFormRow("text");
  });

  /** 选择文件：优先宿主文件对话框，宿主不支持时退回浏览器文件选择器。 */
  async function chooseFiles(rowIndex, { fileId = "", multiple = true } = {}) {
    if (host.canPickFiles()) {
      try {
        const sources = await host.pickUploadSources({ multiple });
        if (sources) {
          await addFormFiles(rowIndex, sources, { fileId });
          return;
        }
      } catch {
        // 宿主文件对话框不可用时继续走浏览器选择器。
      }
    }
    pendingRowIndex = rowIndex;
    pendingFileId = fileId;
    pendingMultiple = multiple;
    if (fileInput) fileInput.value = "";
    fileInput?.click();
  }

  async function onFileInput(event) {
    const input = event.currentTarget;
    const selected = [...(input.files || [])];
    input.value = "";
    const rowIndex = pendingRowIndex;
    const fileId = pendingFileId;
    const multiple = pendingMultiple;
    pendingRowIndex = -1;
    pendingFileId = "";
    pendingMultiple = true;
    if (rowIndex < 0 || !selected.length) return;
    const list = multiple ? selected : selected.slice(0, 1);
    await addFormFiles(rowIndex, list.map(host.browserUploadSource), { fileId });
  }

  /** 文本框里按 Enter 追加新行，和参数 / 请求头的键值编辑器保持一致。 */
  function onFieldKeydown(event, index) {
    if (event.key !== "Enter" || index !== rows.length - 1) return;
    event.preventDefault();
    addFormRow("text");
    queueMicrotask(() => {
      const inputs = event.currentTarget.closest(".kv-editor")?.querySelectorAll(".kv-row:last-child input");
      if (inputs && inputs[1]) inputs[1].focus();
    });
  }
</script>

{#if multipart}
  {#if fileCount > 0}
    <div class="form-files-summary">
      <svg class="form-files-summary-icon" viewBox="0 0 12 12" width="12" height="12" fill="none" aria-hidden="true">
        <path
          d="M7.2 3.2 4.1 6.3a1.5 1.5 0 0 0 2.1 2.1l3.2-3.2a2.5 2.5 0 0 0-3.6-3.6L2.5 5a3.5 3.5 0 0 0 5 5l2.4-2.4"
          stroke="currentColor"
          stroke-width="1.2"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
      <span class="form-files-count">{t("formFilesCount", { count: fileCount })}</span>
      <span class="form-files-size">{formatBytes(fileBytes)}</span>
    </div>
  {/if}
{/if}

<div class="kv-editor form-editor">
  {#each rows as row, index (index)}
    <div class="kv-row form-row" class:is-multipart={multipart} class:has-files={multipart && row.kind === "file"}>
      <input
        type="checkbox"
        checked={row.enabled !== false}
        title={t("enabled")}
        aria-label={t("enabled")}
        onchange={(event) => patchFormRow(index, { enabled: event.currentTarget.checked })}
      />
      {#if multipart}
        <button
          class="form-kind"
          class:is-file={row.kind === "file"}
          type="button"
          title={row.kind === "file" ? t("formSwitchToText") : t("formSwitchToFile")}
          aria-label={row.kind === "file" ? t("formSwitchToText") : t("formSwitchToFile")}
          onclick={() => setFormRowKind(index, row.kind === "file" ? "text" : "file")}
        >
          {#if row.kind === "file"}
            <svg viewBox="0 0 12 12" width="13" height="13" fill="none" aria-hidden="true"
              ><path
                d="M3.2 1.5h3.6L9.4 4.2v6.3H3.2z"
                stroke="currentColor"
                stroke-width="1.2"
                stroke-linejoin="round"
              /><path d="M6.8 1.6v2.7h2.6" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" /></svg
            >
          {:else}
            T
          {/if}
        </button>
      {/if}
      <input
        class="input"
        value={row.name}
        placeholder={t("formFieldName")}
        spellcheck="false"
        aria-label={t("formFieldName")}
        title={multipart && row.kind === "file" ? t("formFieldNameEmptyHint") : undefined}
        oninput={(event) => patchFormRow(index, { name: event.currentTarget.value })}
        onkeydown={(event) => onFieldKeydown(event, index)}
      />
      {#if multipart && row.kind === "file"}
        <div class="form-files" class:is-empty={!row.files?.length}>
          {#each row.files || [] as file (file.id)}
            <span
              class="form-file-chip"
              class:is-missing={!file.uploadId && file.status !== "staging"}
              class:is-error={file.status === "error"}
              class:is-staging={file.status === "staging"}
            >
              <button
                class="form-file-main"
                type="button"
                disabled={file.status === "staging"}
                title={t("formReselect")}
                onclick={() => chooseFiles(index, { fileId: file.id, multiple: false })}
              >
                <span class="form-file-name" title={file.fileName}>{file.fileName}</span>
                <span class="form-file-size">
                  {#if file.status === "staging"}
                    {t("formFileStagingShort", { percent: file.progress })}
                  {:else if file.uploadId}
                    {formatBytes(file.size)}
                  {:else if file.status === "error"}
                    {t("formFileStageFailedShort")}
                  {:else}
                    {t("formFileMissingShort")}
                  {/if}
                </span>
              </button>
              <button
                class="form-file-remove"
                type="button"
                title={t("remove")}
                aria-label={t("remove")}
                onclick={() => removeFormFile(file.id)}
              >
                <svg viewBox="0 0 11 11" width="8" height="8" fill="none" aria-hidden="true"
                  ><path d="M2.5 2.5l6 6M8.5 2.5l-6 6" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"
                  /></svg
                >
              </button>
            </span>
          {/each}
          {#if !row.files?.length}
            <button class="form-file-empty" type="button" onclick={() => chooseFiles(index, { multiple: true })}>
              <svg viewBox="0 0 12 12" width="10" height="10" fill="none" aria-hidden="true"
                ><path d="M6 2.2v7.6M2.2 6h7.6" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /></svg
              >
              <span>{t("formChooseFile")}</span>
            </button>
          {:else}
            <button
              class="form-file-add"
              type="button"
              title={t("formAddFiles")}
              aria-label={t("formAddFiles")}
              onclick={() => chooseFiles(index, { multiple: true })}
            >
              <svg viewBox="0 0 12 12" width="10" height="10" fill="none" aria-hidden="true"
                ><path d="M6 2.2v7.6M2.2 6h7.6" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /></svg
              >
            </button>
          {/if}
        </div>
      {:else}
        <input
          class="input"
          value={row.value}
          placeholder={t("value")}
          spellcheck="false"
          aria-label={t("value")}
          oninput={(event) => patchFormRow(index, { value: event.currentTarget.value })}
          onkeydown={(event) => onFieldKeydown(event, index)}
        />
      {/if}
      <button
        class="kv-remove"
        type="button"
        title={t("remove")}
        aria-label={t("remove")}
        onclick={() => removeFormRow(index)}
      >
        <svg viewBox="0 0 11 11" width="11" height="11" fill="none" aria-hidden="true"
          ><path d="M2.5 2.5l6 6M8.5 2.5l-6 6" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg
        >
      </button>
    </div>
  {/each}
</div>

<input
  type="file"
  class="form-file-input"
  tabindex="-1"
  aria-hidden="true"
  multiple
  bind:this={fileInput}
  onchange={onFileInput}
/>
