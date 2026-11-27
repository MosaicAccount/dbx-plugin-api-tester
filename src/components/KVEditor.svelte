<script>
  /**
   * 键值行编辑器（参数 / 请求头 / gRPC metadata 共用）。
   *
   * 只发事件、不改数据，所有变更都交给 store 的 patchKV / removeKV，
   * 这样这一份组件同时服务三处表单，行为不会各自漂移。
   */
  import { appendKVOnEnter, patchKV, removeKV } from "../lib/store.js";

  let { t, scope, listName, rows = [], namePlaceholder = "", valuePlaceholder = "" } = $props();

  function onKey(event, index) {
    if (event.key !== "Enter") return;
    if (!appendKVOnEnter(scope, listName, index)) return;
    event.preventDefault();
    // 新增行在下一帧渲染，聚焦它的「键」输入框。
    queueMicrotask(() => {
      const container = event.target.closest(".kv-editor");
      const inputs = container?.querySelectorAll(".kv-row:last-child input");
      if (inputs && inputs[1]) inputs[1].focus();
    });
  }
</script>

<div class="kv-editor">
  {#each rows as row, index (index)}
    <div class="kv-row">
      <input
        type="checkbox"
        checked={row.enabled !== false}
        title={t("enabled")}
        aria-label={t("enabled")}
        onchange={(event) => patchKV(scope, listName, index, { enabled: event.currentTarget.checked })}
      />
      <input
        class="input"
        value={row.name}
        placeholder={namePlaceholder || t("key")}
        spellcheck="false"
        aria-label={namePlaceholder || t("key")}
        oninput={(event) => patchKV(scope, listName, index, { name: event.currentTarget.value })}
        onkeydown={(event) => onKey(event, index)}
      />
      <input
        class="input"
        value={row.value}
        placeholder={valuePlaceholder || t("value")}
        spellcheck="false"
        aria-label={valuePlaceholder || t("value")}
        oninput={(event) => patchKV(scope, listName, index, { value: event.currentTarget.value })}
        onkeydown={(event) => onKey(event, index)}
      />
      <button
        class="kv-remove"
        type="button"
        title={t("remove")}
        aria-label={t("remove")}
        onclick={() => removeKV(scope, listName, index)}
      >
        <svg viewBox="0 0 11 11" width="11" height="11" fill="none" aria-hidden="true"
          ><path d="M2.5 2.5l6 6M8.5 2.5l-6 6" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg
        >
      </button>
    </div>
  {/each}
</div>
