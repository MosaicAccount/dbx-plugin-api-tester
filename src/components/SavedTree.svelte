<script>
  /**
   * 已保存请求列表。
   *
   * 目前是扁平列表；按目录分组的树形展示留待后续再加。
   */
  import { methodTone, protocolTone, entryMethod, shouldShowMethod } from "../lib/model.js";

  let { t, savedIndex = [], activeSavedId = null, onopen, ondelete } = $props();
</script>

{#if savedIndex.length === 0}
  <div class="side-empty">{t("savedEmpty")}</div>
{:else}
  {#each savedIndex as item (item.id)}
    <div
      class="side-item"
      class:is-active={item.id === activeSavedId}
      role="button"
      tabindex="0"
      data-request-id={item.id}
      onclick={() => onopen?.(item.id)}
      onkeydown={(event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        onopen?.(item.id);
      }}
    >
      <div class="side-item-top">
        <span class="proto-badge is-{protocolTone(item.protocol)}">{item.protocol === "grpc" ? "GRPC" : "HTTP"}</span>
        {#if shouldShowMethod(item)}
          <span class="method-tag is-{methodTone(entryMethod(item))}">{entryMethod(item)}</span>
        {/if}
        <span class="side-name">{item.name || t("untitled")}</span>
        <span class="side-actions">
          <button
            class="icon-btn"
            type="button"
            title={t("delete")}
            aria-label={t("delete")}
            onclick={(event) => {
              event.stopPropagation();
              ondelete?.(item.id);
            }}>×</button
          >
        </span>
      </div>
    </div>
  {/each}
{/if}
