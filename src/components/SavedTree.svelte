<script>
  /**
   * 已保存请求的目录树。
   *
   * 树结构由扁平索引派生：目录来自 folderIndex ∪ 请求实际所在目录。
   * 折叠状态放在 store 的 collapsedFolders 里，切换目录时不丢失。
   */
  import { onMount } from "svelte";
  import { collapsedFolders, toggleFolder } from "../lib/store.js";
  import {
    collectFolders,
    entryMethod,
    methodTone,
    protocolTone,
    shouldShowMethod,
  } from "../lib/model.js";

  let {
    t,
    savedIndex = [],
    folderIndex = [],
    activeSavedId = null,
    onopen,
    onadd,
    onmoverequest,
    onmovefolder,
    ondelete,
    ondeletefolder,
    onDragChange,
  } = $props();

  // 显式依赖 props 里的两个索引：collectFolders 是纯函数，Svelte 能据此追踪更新。
  // 不能在这里调用 store 里的 listFolders()，那会把依赖藏在函数内部，变更不会触发重算。
  const folders = $derived(collectFolders(folderIndex, savedIndex));

  /** 把扁平列表构造成树。空目录也要保留，否则新建的空目录看不见。 */
  const tree = $derived.by(() => {
    const root = { path: "", name: "", children: new Map(), items: [] };
    const ensure = (folder) => {
      let node = root;
      let path = "";
      for (const segment of folder.split("/")) {
        path = path ? `${path}/${segment}` : segment;
        if (!node.children.has(segment)) {
          node.children.set(segment, { path, name: segment, children: new Map(), items: [] });
        }
        node = node.children.get(segment);
      }
      return node;
    };
    for (const folder of folders) ensure(folder);
    for (const item of savedIndex) {
      const folder = String(item.folder || "");
      if (folder) ensure(folder).items.push(item);
      else root.items.push(item);
    }
    return root;
  });

  const sortedChildren = (node) => [...node.children.values()].sort((a, b) => a.name.localeCompare(b.name));

  function countItems(node) {
    let total = node.items.length;
    for (const child of node.children.values()) total += countItems(child);
    return total;
  }

  const isEmpty = $derived(savedIndex.length === 0 && folders.length === 0);

  /**
   * 指针拖拽。
   *
   * 没用 HTML5 draggable：DBX 的工作台 iframe 是 sandbox="allow-scripts" 的不透明源，
   * 原生 DnD 的 dataTransfer 在部分宿主 WebView 里不可用，跨源也让自动化难以验证。
   * 这里改用 pointer 事件手动实现，行为只依赖坐标，宿主无关。
   *
   * 拖动超过阈值才算拖拽，否则保留普通点击打开请求的行为。
   */
  const DRAG_THRESHOLD = 5;

  /**
   * 当前拖拽会话。
   * kind 区分拖的是接口还是目录——两者投放语义不同，目录不能放进"未分组"。
   * @type {{ kind: "request" | "folder", id: string, path?: string, x: number, y: number, active: boolean } | null}
   */
  let drag = $state(null);
  /** 当前指针下的目录路径（投放目标）。 */
  let dropTarget = $state(null);
  /** 拖拽结束后浏览器会补发一次 click，用它挡掉误触发的"打开请求"。 */
  let suppressClick = false;
  /** 跟随指针的拖影位置（视口坐标）。 */
  let ghost = $state({ x: 0, y: 0 });

  const dragActive = $derived(Boolean(drag && drag.active));
  const draggingId = $derived(drag && drag.active && drag.kind === "request" ? drag.id : null);
  /** 被拖动的目录路径；拖接口时为 null。 */
  const draggingFolder = $derived(drag && drag.active && drag.kind === "folder" ? drag.path : null);
  /** 被拖动的那条请求，用来让父级绘制拖影内容。 */
  const draggingItem = $derived(draggingId ? (savedIndex.find((item) => item.id === draggingId) ?? null) : null);

  // 拖拽状态是组件内部状态，但要由 App 层渲染拖影，所以变化时上报一次。
  $effect(() => {
    reportDrag();
  });

  /**
   * 找出指针位置下的目录行，返回它的路径；没有则返回 null。
   *
   * 用几何命中而不是 elementFromPoint：拖拽时被拖的行会跟着指针，且它自身
   * 也可能覆盖目录行，elementFromPoint 会命中错误元素。
   *
   * 行高只有约 28px，纯边界判定容易在两个目录之间的缝隙脱靶，所以上下各放宽
   * 一半间距，取距离最近的那个目录。
   */
  function folderAt(x, y) {
    let best = null;
    let bestDistance = Infinity;
    for (const row of document.querySelectorAll("[data-folder-path]")) {
      const rect = row.getBoundingClientRect();
      if (x < rect.left || x > rect.right) continue;
      const slack = 6;
      if (y < rect.top - slack || y > rect.bottom + slack) continue;
      const distance = Math.abs(y - (rect.top + rect.bottom) / 2);
      if (distance < bestDistance) {
        bestDistance = distance;
        best = row.getAttribute("data-folder-path");
      }
    }
    return best;
  }

  /**
   * 拖拽状态通过回调上报给 App.svelte。
   *
   * 拖影必须渲染在 App 层（body 下），否则会被侧栏列表的 overflow:auto 裁掉。
   * 这样组件保持声明式，不做命令式 DOM 操作。
   */
  function reportDrag() {
    // 目录拖拽时没有接口条目，用 kind + name 描述拖影内容。
    const payload = draggingItem
      ? { kind: "request", item: draggingItem, x: ghost.x, y: ghost.y, overTarget: dropTarget !== null }
      : draggingFolder
        ? { kind: "folder", name: draggingFolder, x: ghost.x, y: ghost.y, overTarget: dropTarget !== null }
        : null;
    onDragChange?.(payload);
  }

  function beginRequestDrag(event, item) {
    // 只响应主键；触控/笔也走 pointerdown，但 trackpad 的右键菜单要排除。
    if (event.button !== 0) return;
    drag = { kind: "request", id: item.id, x: event.clientX, y: event.clientY, active: false };
  }

  /**
   * 目录也可以拖动。
   *
   * 根目录（未分组）不是真实目录，只是根级请求的容器，没有任何"移动"语义，
   * 所以它只作为投放目标，不能作为拖拽源。
   */
  function beginFolderDrag(event, node) {
    if (event.button !== 0) return;
    if (!node.path) return;
    drag = { kind: "folder", id: node.path, path: node.path, x: event.clientX, y: event.clientY, active: false };
  }

  function movePointerDrag(event) {
    if (!drag) return;
    if (!drag.active) {
      const moved = Math.hypot(event.clientX - drag.x, event.clientY - drag.y);
      if (moved < DRAG_THRESHOLD) return;
      drag = { ...drag, active: true };
      ghost = { x: event.clientX, y: event.clientY };
      suppressClick = true;
    }
    event.preventDefault();
    ghost = { x: event.clientX, y: event.clientY };
    dropTarget = folderAt(event.clientX, event.clientY);
  }

  function endPointerDrag(event) {
    if (!drag) return;
    const session = drag;
    const wasActive = session.active;
    const target = dropTarget;
    drag = null;
    dropTarget = null;
    if (!wasActive || target === null) return;

    if (session.kind === "request") {
      const entry = savedIndex.find((item) => item.id === session.id);
      if (!entry || String(entry.folder || "") === target) return;
      event.preventDefault();
      onmoverequest?.(session.id, target);
      return;
    }

    // 目录不能放进"未分组"——那是根级容器，不是可嵌套的目录。
    if (target === "") return;
    if (session.path === target || target.startsWith(`${session.path}/`)) return;
    event.preventDefault();
    onmovefolder?.(session.path, target);
  }

  function cancelPointerDrag() {
    drag = null;
    dropTarget = null;
  }

  // 拖拽时指针会离开请求行，所以移动到 window 上监听；只在按下后才真正挂载逻辑。
  onMount(() => {
    const onMove = (event) => movePointerDrag(event);
    const onUp = (event) => endPointerDrag(event);
    const onCancel = () => cancelPointerDrag();
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onCancel);
    return () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onCancel);
    };
  });
</script>

{#snippet requestRow(item, depth)}
  <div
    class="side-item"
    class:is-active={item.id === activeSavedId}
    class:is-dragging={draggingId === item.id}
    role="button"
    tabindex="0"
    data-request-id={item.id}
    style:padding-left={depth ? `${8 + depth * 12}px` : undefined}
    onclick={() => {
      // 拖拽结束后浏览器仍会补一次 click，这里要挡掉，避免顺手打开请求。
      if (suppressClick) {
        suppressClick = false;
        return;
      }
      onopen?.(item.id);
    }}
    onpointerdown={(event) => beginRequestDrag(event, item)}
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
          }}
        >
          <svg viewBox="0 0 13 13" width="13" height="13" fill="none" aria-hidden="true"
            ><path d="M3.2 3.2l6.6 6.6M9.8 3.2l-6.6 6.6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg
          >
        </button>
      </span>
    </div>
  </div>
{/snippet}

{#snippet folderNode(node, depth)}
  {@const collapsed = $collapsedFolders.has(node.path)}
  <div
    class="folder-head"
    class:is-collapsed={collapsed}
    class:is-drop-target={dragActive && dropTarget === node.path}
    role="button"
    tabindex="0"
    aria-expanded={!collapsed}
    data-folder-path={node.path}
    class:is-dragging={draggingFolder === node.path}
    style:padding-left={`${8 + depth * 12}px`}
    onclick={() => toggleFolder(node.path)}
    onpointerdown={(event) => beginFolderDrag(event, node)}
    onkeydown={(event) => {
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      toggleFolder(node.path);
    }}
  >
    <span class="folder-chevron">
      <svg viewBox="0 0 12 12" width="11" height="11" fill="none" aria-hidden="true"
        ><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg
      >
    </span>
    <span class="folder-icon">
      <svg viewBox="0 0 16 16" width="12" height="12" fill="none" aria-hidden="true"
        ><path
          d="M2.5 4.5A1.5 1.5 0 0 1 4 3h2.4l1.2 1.6H12a1.5 1.5 0 0 1 1.5 1.5v5.4A1.5 1.5 0 0 1 12 13H4a1.5 1.5 0 0 1-1.5-1.5z"
          stroke="currentColor"
          stroke-width="1.3"
          stroke-linejoin="round"
        /></svg
      >
    </span>
    <span class="folder-name">{node.name}</span>
    {#if countItems(node)}
      <span class="folder-count" title={t("folderCountHint", { count: countItems(node) })}>{countItems(node)}</span>
    {/if}
    <span class="folder-actions">
      <button
        class="icon-btn folder-add"
        type="button"
        title={t("newAction")}
        aria-label={t("newAction")}
        onclick={(event) => {
          event.stopPropagation();
          onadd?.(node.path, event.currentTarget);
        }}
      >
        <svg viewBox="0 0 13 13" width="13" height="13" fill="none" aria-hidden="true"
          ><path d="M6.5 2.9v7.2M2.9 6.5h7.2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg
        >
      </button>
      <button
        class="icon-btn folder-delete"
        type="button"
        title={t("deleteFolderTitle")}
        aria-label={t("deleteFolderTitle")}
        onclick={(event) => {
          event.stopPropagation();
          ondeletefolder?.(node.path);
        }}
      >
        <svg viewBox="0 0 13 13" width="13" height="13" fill="none" aria-hidden="true"
          ><path d="M3.2 3.2l6.6 6.6M9.8 3.2l-6.6 6.6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg
        >
      </button>
    </span>
  </div>
  {#if !collapsed}
    {#each sortedChildren(node) as child (child.path)}
      {@render folderNode(child, depth + 1)}
    {/each}
    {#each node.items as item (item.id)}
      {@render requestRow(item, depth + 1)}
    {/each}
  {/if}
{/snippet}

{#if isEmpty}
  <div class="side-empty">{t("savedEmpty")}</div>
{:else}
  {#each sortedChildren(tree) as child (child.path)}
    {@render folderNode(child, 0)}
  {/each}
  {#if tree.items.length}
    {@const rootCollapsed = $collapsedFolders.has("")}
    <div
      class="folder-head"
      class:is-collapsed={rootCollapsed}
      class:is-drop-target={dragActive && dropTarget === ""}
      role="button"
      tabindex="0"
      data-folder-path=""
      aria-expanded={!rootCollapsed}
      onclick={() => toggleFolder("")}
      onkeydown={(event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        toggleFolder("");
      }}
    >
      <span class="folder-chevron">
        <svg viewBox="0 0 12 12" width="11" height="11" fill="none" aria-hidden="true"
          ><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg
        >
      </span>
      <span class="folder-name">{t("ungrouped")}</span>
      <span class="folder-count" title={t("requestCountHint", { count: tree.items.length })}>{tree.items.length}</span>
    </div>
    {#if !rootCollapsed}
      {#each tree.items as item (item.id)}
        {@render requestRow(item, 0)}
      {/each}
    {/if}
  {/if}
{/if}
