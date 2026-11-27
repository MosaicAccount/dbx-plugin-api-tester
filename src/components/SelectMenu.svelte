<script>
  /**
   * 下拉选择菜单。
   *
   * 用原生 button + listbox 语义实现，避免依赖任何 UI 库：
   * - 点击外部关闭、Escape 关闭并归还焦点。
   * - 支持 disabled 项（例如"请先发现服务"占位）。
   */
  import { onDestroy } from "svelte";

  let {
    items = [],
    value = "",
    disabled = false,
    placeholder = "",
    ariaLabel = "",
    onchange = () => {},
  } = $props();

  let open = $state(false);
  let root = $state(null);
  let trigger = $state(null);
  let popup = $state(null);

  const selected = $derived(items.find((item) => item.value === value) || null);
  const label = $derived(selected ? selected.label : placeholder);
  /** 可选的右侧标签（例如方法类型）；没有 items 带 badge 时完全不占位。 */
  const selectedBadge = $derived(selected?.badge || "");

  /**
   * 按 `group` 把选项切成小节，连续同组算一节；没有 group 的选项保持不分组，
   * 所以现有调用方不需要改。组标题可选一个 12×12 的 `groupIcon` 路径（取自该组
   * 第一项），用来和组内的选项标题区分开。
   */
  const sections = $derived.by(() => {
    /** @type {{ name: string, icon: string, items: any[] }[]} */
    const out = [];
    for (const item of items) {
      const name = item.group || "";
      const last = out[out.length - 1];
      if (last && last.name === name) last.items.push(item);
      else out.push({ name, icon: item.groupIcon || "", items: [item] });
    }
    return out;
  });

  function close(returnFocus = false) {
    open = false;
    if (returnFocus && trigger) trigger.focus();
  }

  function toggle() {
    if (disabled) return;
    open = !open;
  }

  function choose(item) {
    if (item.disabled) return;
    close();
    onchange(item.value);
  }

  function onKeydown(event) {
    if (event.key === "Escape" && open) {
      event.stopPropagation();
      close(true);
    }
  }

  // 点击外部关闭。用捕获阶段，确保在别的控件处理点击之前先关闭。
  $effect(() => {
    if (!open || !root) return;
    const onPointerDown = (event) => {
      if (root.contains(event.target)) return;
      close();
    };
    document.addEventListener("mousedown", onPointerDown, true);
    return () => document.removeEventListener("mousedown", onPointerDown, true);
  });

  onDestroy(() => close());
</script>

<div class="select-menu" class:is-open={open} bind:this={root}>
  <button
    class="select select-trigger"
    class:is-disabled={disabled}
    type="button"
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-label={ariaLabel || undefined}
    {disabled}
    bind:this={trigger}
    onclick={toggle}
    onkeydown={onKeydown}
  >
    {#if selectedBadge}
      <span class="select-badge select-badge--trigger" data-tone={selected?.tone || "other"}>{selectedBadge}</span>
    {/if}
    <span class="select-value">{label}</span>
    <svg class="chevron" viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
  </button>

  {#if open}
    <div class="select-popup" role="listbox" tabindex="-1" bind:this={popup} onkeydown={onKeydown}>
      {#each sections as section, sectionIndex (sectionIndex)}
        <div class="select-group" role={section.name ? "group" : "presentation"} aria-label={section.name || undefined}>
          {#if section.name}
            <div class="select-group-label" aria-hidden="true">
              {#if section.icon}
                <svg class="select-group-icon" viewBox="0 0 12 12" width="12" height="12" fill="none" aria-hidden="true">
                  <path
                    d={section.icon}
                    stroke="currentColor"
                    stroke-width="1.25"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  />
                </svg>
              {/if}
              <span>{section.name}</span>
            </div>
          {/if}
          {#each section.items as item (item.value)}
            <button
              class="select-option"
              class:is-selected={item.value === value}
              type="button"
              role="option"
              aria-selected={item.value === value}
              disabled={item.disabled}
              onclick={() => choose(item)}
            >
              {#if item.badge}
                <span class="select-badge" data-tone={item.tone || "other"}>{item.badge}</span>
              {/if}
              <span class="select-option-label">{item.label}</span>
            </button>
          {/each}
        </div>
      {/each}
    </div>
  {/if}
</div>
