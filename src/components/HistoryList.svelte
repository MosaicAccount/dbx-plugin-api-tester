<script>
  /**
   * 历史记录列表。
   * 与已保存列表结构相近但更简单：没有目录、没有移动/删除单条。
   */
  import { entryMethod, historyTitle, methodTone, protocolTone, shouldShowMethod } from "../lib/model.js";

  let { t, historyIndex = [], onopen } = $props();
</script>

{#if !historyIndex.length}
  <div class="side-empty">{t("historyEmpty")}</div>
{:else}
  {#each historyIndex as item (item.id)}
    <div
      class="side-item"
      role="button"
      tabindex="0"
      onclick={() => onopen?.(item.id)}
      onkeydown={(event) => {
        if (event.key !== "Enter") return;
        event.preventDefault();
        onopen?.(item.id);
      }}
    >
      <div class="side-item-top">
        <span class="proto-badge is-{protocolTone(item.protocol)}">{item.protocol === "grpc" ? "GRPC" : "HTTP"}</span>
        {#if shouldShowMethod(item)}
          <span class="method-tag is-{methodTone(entryMethod(item))}">{entryMethod(item)}</span>
        {/if}
        <span class="side-name">{historyTitle(item)}</span>
      </div>
    </div>
  {/each}
{/if}
