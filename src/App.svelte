<script>
  /**
   * 工作台根组件。
   *
   * 只做三件事：初始化、布局装配、全局快捷键与菜单。
   * 表单与渲染逻辑下沉到各面板组件，业务状态在 lib/store.js。
   *
   * 注意：store 里的 `state` 在组件内以 `appState` 引入。Svelte 5 把 `$state`
   * 当作 rune，直接用 `$state` 会与之冲突。
   */
  import { onMount } from "svelte";
  import { t as tStore, locale } from "./lib/i18n.js";
  import * as host from "./lib/host.js";
  import {
    clearHistory,
    createFolder,
    deleteFolder,
    deleteSaved,
    folderDeleteStats,
    initialize,
    invokeGrpc,
    layout as layoutStore,
    listFolders,
    moveFolder,
    moveSaved,
    newRequest,
    openHistory,
    openSaved,
    refreshTranslator,
    saveCurrent,
    sendHttp,
    setProtocol,
    setSplit,
    showToast,
    state as appState,
    toast as toastStore,
  } from "./lib/store.js";
  import {
    SAVE_AS_SHORTCUT,
    SAVE_SHORTCUT,
    entryMethod,
    methodTone,
    normalizeFolder,
    protocolTone,
    shouldShowMethod,
    suggestName,
    toSnapshot,
  } from "./lib/model.js";
  import SavedTree from "./components/SavedTree.svelte";
  import HistoryList from "./components/HistoryList.svelte";
  import HttpRequestBar from "./components/HttpRequestBar.svelte";
  import HttpPanel from "./components/HttpPanel.svelte";
  import GrpcRequestHead from "./components/GrpcRequestHead.svelte";
  import GrpcPanel from "./components/GrpcPanel.svelte";
  import ResponsePanel from "./components/ResponsePanel.svelte";
  import SettingsDialog from "./components/SettingsDialog.svelte";
  import Modal from "./components/Modal.svelte";

  const t = $derived($tStore);

  /** 当前模态框请求；null 表示不显示。 */
  let dialog = $state(null);
  /** 设置弹窗是否打开。 */
  let settingsOpen = $state(false);
  /** 新建菜单。 */
  let sideMenu = $state(null);
  /** 侧栏拖拽中的请求与指针位置；null 表示没有拖拽。 */
  let dragState = $state(null);
  let menuPosition = $state({ left: 8, top: 0 });
  /** 请求/响应分栏容器；null 表示还没挂载。 */
  let workspace = $state(null);
  /** 正在拖动分栏分隔条。 */
  let resizingSplit = $state(false);

  const title = $derived(
    $appState.activeName
      ? `${$appState.activeFolder ? `${$appState.activeFolder} / ` : ""}${$appState.activeName}`
      : t("untitled"),
  );

  function detectLocale(value) {
    return String(value ?? host.hostLocale()).toLowerCase().startsWith("zh") ? "zh" : "en";
  }

  onMount(() => {
    locale.set(detectLocale());

    void initialize().then(() => {
      refreshTranslator();
      locale.set(detectLocale());
    });

    const onEnv = (event) => {
      const next = detectLocale(event.detail?.locale);
      if (next === $locale) return;
      locale.set(next);
      refreshTranslator();
    };
    window.addEventListener("dbx-plugin-env", onEnv);
    window.addEventListener("keydown", onKeydown);
    return () => {
      window.removeEventListener("dbx-plugin-env", onEnv);
      window.removeEventListener("keydown", onKeydown);
    };
  });

  $effect(() => {
    document.documentElement.lang = $locale === "zh" ? "zh-CN" : "en";
    document.title = t("appName");
  });

  // -------------------------------------------------------------------------
  // 新建菜单
  // -------------------------------------------------------------------------

  function openNewMenu(folder, anchor) {
    const target = normalizeFolder(folder);
    if (sideMenu?.anchor === anchor) {
      sideMenu = null;
      return;
    }
    const rect = anchor?.getBoundingClientRect();
    menuPosition = {
      left: Math.max(8, Math.min(rect?.left ?? 8, window.innerWidth - 220)),
      top: (rect?.bottom ?? 0) + 4,
    };
    sideMenu = {
      anchor,
      items: [
        { label: t("newRequest"), run: () => newRequest(target) },
        { label: target ? t("newSubfolder") : t("newFolder"), run: () => askFolderName(target) },
      ],
    };
  }

  function closeNewMenu() {
    sideMenu = null;
  }

  function onOutsidePointerDown(event) {
    if (!sideMenu) return;
    if (document.getElementById("side-menu")?.contains(event.target)) return;
    if (sideMenu.anchor?.contains(event.target)) return;
    closeNewMenu();
  }

  $effect(() => {
    if (!sideMenu) return;
    document.addEventListener("mousedown", onOutsidePointerDown, true);
    document.addEventListener("scroll", closeNewMenu, true);
    window.addEventListener("resize", closeNewMenu);
    return () => {
      document.removeEventListener("mousedown", onOutsidePointerDown, true);
      document.removeEventListener("scroll", closeNewMenu, true);
      window.removeEventListener("resize", closeNewMenu);
    };
  });

  // -------------------------------------------------------------------------
  // 模态框
  // -------------------------------------------------------------------------

  function askFolderName(parent) {
    dialog = {
      kind: "folder",
      title: t("newFolderTitle"),
      input: true,
      inputLabel: t("newFolderLabel"),
      placeholder: t("folderNamePlaceholder"),
      confirmLabel: t("create"),
      validate: (value) => (value.name ? "" : t("needFolderName")),
      parent: normalizeFolder(parent),
    };
  }

  async function onDialogResolve(result) {
    const pending = dialog;
    dialog = null;
    if (!pending || result === null || result === false) return;

    switch (pending.kind) {
      case "folder":
        await createFolder(pending.parent, result.name);
        break;
      case "save":
        await saveCurrent({ name: result.name, folder: result.folder }, pending.asNew);
        break;
      case "confirm-delete":
        await deleteSaved(pending.id);
        break;
      case "confirm-delete-folder":
        await deleteFolder(pending.path);
        break;
      case "confirm-clear-history":
        await clearHistory();
        break;
      default:
        break;
    }
  }

  function save(asNew) {
    const s = $appState;
    // 已有归属且不是「另存为」时直接覆盖，不再打扰用户填名称。
    if (!asNew && s.activeSavedId && s.activeName) {
      void saveCurrent({ name: s.activeName, folder: s.activeFolder }, false);
      return;
    }
    dialog = {
      kind: "save",
      asNew,
      title: asNew ? t("saveAsTitle") : t("savePromptTitle"),
      input: true,
      inputLabel: t("requestNameLabel"),
      inputValue: s.activeName || suggestName(toSnapshot(s), t("untitled")),
      placeholder: t("savePromptPlaceholder"),
      folder: true,
      folderLabel: t("saveLocationLabel"),
      folderValue: s.activeFolder || "",
      folders: listFolders(),
      confirmLabel: t("save"),
      validate: (value, context) => {
        if (!value.name) return t("needName");
        if (context.creatingFolder && !value.folder) return t("needFolderName");
        return "";
      },
    };
  }

  function confirmDelete(id) {
    const entry = $appState.savedIndex.find((item) => item.id === id);
    dialog = {
      kind: "confirm-delete",
      id,
      title: t("deleteTitle"),
      message: t("deleteMessage", { name: entry?.name || t("untitled") }),
      confirmLabel: t("delete"),
      danger: true,
    };
  }

  function confirmDeleteFolder(path) {
    const stats = folderDeleteStats(path);
    dialog = {
      kind: "confirm-delete-folder",
      path,
      title: t("deleteFolderTitle"),
      message: t("deleteFolderMessage", { name: path, requests: stats.requests, folders: stats.folders }),
      confirmLabel: t("delete"),
      danger: true,
    };
  }

  function confirmClearHistory() {
    if (!$appState.historyIndex.length) return;
    dialog = {
      kind: "confirm-clear-history",
      title: t("clearHistoryTitle"),
      message: t("clearHistoryMessage"),
      confirmLabel: t("clear"),
      danger: true,
    };
  }

  // -------------------------------------------------------------------------
  // 快捷键
  // -------------------------------------------------------------------------

  // -------------------------------------------------------------------------
  // 请求 / 响应分栏
  // -------------------------------------------------------------------------

  function onSplitterMove(event) {
    if (!resizingSplit || !workspace) return;
    const rect = workspace.getBoundingClientRect();
    if (rect.width <= 0) return;
    setSplit((event.clientX - rect.left) / rect.width);
  }

  function onSplitterDown(event) {
    if (event.button !== 0) return;
    event.preventDefault();
    resizingSplit = true;
    try {
      event.currentTarget.setPointerCapture?.(event.pointerId);
    } catch {
      // 指针捕获失败时仍可用键盘调整，不影响布局。
    }
  }

  function onSplitterUp(event) {
    if (!resizingSplit) return;
    resizingSplit = false;
    try {
      event.currentTarget.releasePointerCapture?.(event.pointerId);
    } catch {
      // 指针可能已经释放，忽略即可。
    }
  }

  /** 分隔条支持方向键微调，键盘用户不必拖拽。 */
  function onSplitterKeydown(event) {
    const step = event.shiftKey ? 0.1 : 0.02;
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      setSplit($layoutStore.split - step);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      setSplit($layoutStore.split + step);
    } else if (event.key === "Home") {
      event.preventDefault();
      setSplit(0.5);
    }
  }

  function onKeydown(event) {
    if (event.key === "Escape") {
      if (!sideMenu) return;
      event.preventDefault();
      closeNewMenu();
      return;
    }
    if (!(event.metaKey || event.ctrlKey) || event.altKey) return;

    const key = event.key.toLowerCase();
    if (key === "s") {
      event.preventDefault();
      if (dialog) return;
      closeNewMenu();
      save(event.shiftKey);
      return;
    }
    if (key === "enter") {
      event.preventDefault();
      if ($appState.protocol === "http") void sendHttp();
      else void invokeGrpc();
    }
  }
</script>

{#if !host.available}
  <div class="app app--degraded">
    <p>{t("hostUnavailable")}</p>
  </div>
{:else}
  <div class="app" class:is-dragging={dragState !== null}>
    <aside class="sidebar">
      <div class="brand">
        <svg class="brand-icon" viewBox="0 0 48 48" fill="none" aria-hidden="true">
          <rect width="48" height="48" rx="12" fill="#2563EB" />
          <path d="M17 17l-7 7 7 7" stroke="#FFFFFF" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" />
          <path d="M31 17l7 7-7 7" stroke="#FFFFFF" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" />
          <circle cx="24" cy="24" r="3" fill="#FFFFFF" />
        </svg>
        <div class="brand-copy">
          <div class="brand-name">{t("appName")}</div>
          <div class="brand-tagline">{t("tagline")}</div>
        </div>
      </div>

      <div class="side-head">
        <span>{t("saved")}</span>
        <button
          class="icon-btn"
          type="button"
          aria-haspopup="menu"
          aria-expanded={Boolean(sideMenu)}
          aria-label={t("newAction")}
          title={t("newAction")}
          onclick={(event) => openNewMenu("", event.currentTarget)}>+</button
        >
      </div>
      <div class="side-list">
        <SavedTree
          {t}
          savedIndex={$appState.savedIndex}
          folderIndex={$appState.folderIndex}
          activeSavedId={$appState.activeSavedId}
          onopen={(id) => void openSaved(id)}
          onadd={(folder, anchor) => openNewMenu(folder, anchor)}
          onmoverequest={(id, folder) => void moveSaved(id, folder)}
          onmovefolder={(path, parent) => void moveFolder(path, parent)}
          ondelete={(id) => confirmDelete(id)}
          ondeletefolder={(path) => confirmDeleteFolder(path)}
          onDragChange={(next) => (dragState = next)}
        />
      </div>
      <!-- 拖拽是隐式交互，需要一句提示让用户发现它。 -->
      {#if $appState.savedIndex.length > 0}
        <p class="side-hint hint">{t("dragHint")}</p>
      {/if}

      <div class="side-head side-head--history">
        <span>{t("history")}</span>
        <button class="link-btn" type="button" onclick={confirmClearHistory}>{t("clear")}</button>
      </div>
      <div class="side-list side-list--history">
        <HistoryList {t} historyIndex={$appState.historyIndex} onopen={(id) => void openHistory(id)} />
      </div>
      <div class="side-foot hint">{$appState.storageAvailable ? "" : t("storageUnavailable")}</div>
    </aside>

    <main class="main">
      <header class="topbar">
        <div class="segmented" role="tablist">
          {#each ["http", "grpc"] as protocol (protocol)}
            <button
              class="seg-btn"
              class:is-active={$appState.protocol === protocol}
              type="button"
              role="tab"
              aria-selected={$appState.protocol === protocol}
              onclick={() => setProtocol(protocol)}
            >
              {protocol === "http" ? "HTTP" : "gRPC"}
            </button>
          {/each}
        </div>
        <div class="topbar-title">{title}</div>
        <div class="topbar-actions">
          <button class="btn btn-ghost btn-sm" type="button" title={t("settings")} onclick={() => (settingsOpen = true)}>
            {t("settings")}
          </button>
          <button
            class="btn btn-ghost btn-sm"
            type="button"
            title={t("saveAsHint", { shortcut: SAVE_AS_SHORTCUT })}
            onclick={() => save(true)}>{t("saveAs")}</button
          >
          <button
            class="btn btn-secondary btn-sm"
            type="button"
            title={t("saveHint", { shortcut: SAVE_SHORTCUT })}
            onclick={() => save(false)}>{t("save")}</button
          >
        </div>
      </header>

      <!-- 接口信息（方法 / URL / 目标 / 描述来源）占满整行，下面才是编辑器和响应栏的分栏。 -->
      <div class="request-head">
        {#if $appState.protocol === "http"}
          <HttpRequestBar
            {t}
            http={$appState.http}
            sending={$appState.sending}
            canceling={$appState.canceling}
          />
        {:else}
          <GrpcRequestHead
            {t}
            grpc={$appState.grpc}
            sending={$appState.sending}
            discovering={$appState.discovering}
            canceling={$appState.canceling}
          />
        {/if}
      </div>

      <div class="workspace" class:is-resizing={resizingSplit} bind:this={workspace} style:--split={$layoutStore.split}>
        <section class="editor">
          {#if $appState.protocol === "http"}
            <HttpPanel {t} http={$appState.http} defaultTimeout={$appState.settings.httpTimeoutMs} />
          {:else}
            <GrpcPanel
              {t}
              grpc={$appState.grpc}
              sending={$appState.sending}
              defaultTimeout={$appState.settings.grpcTimeoutMs}
            />
          {/if}
        </section>

        <!-- 分隔条是可用方向键调整的 widget：用 slider 角色向读屏器暴露"当前比例"。 -->
        <div
          class="splitter"
          role="slider"
          aria-orientation="horizontal"
          aria-label={t("splitPanes")}
          title={t("splitPanes")}
          aria-valuemin="25"
          aria-valuemax="75"
          aria-valuenow={Math.round($layoutStore.split * 100)}
          tabindex="0"
          onpointerdown={onSplitterDown}
          onpointermove={onSplitterMove}
          onpointerup={onSplitterUp}
          onpointercancel={onSplitterUp}
          onkeydown={onSplitterKeydown}
        ></div>

        <ResponsePanel
          {t}
          response={$appState.response}
          responseTab={$appState.responseTab}
          protocol={$appState.protocol}
          canceling={$appState.canceling}
        />
      </div>
    </main>
  </div>
{/if}

{#if sideMenu}
  <div id="side-menu" class="side-menu" role="menu" style:left={`${menuPosition.left}px`} style:top={`${menuPosition.top}px`}>
    {#each sideMenu.items as item (item.label)}
      <button
        class="side-menu-item"
        type="button"
        role="menuitem"
        onclick={() => {
          closeNewMenu();
          void item.run();
        }}>{item.label}</button
      >
    {/each}
  </div>
{/if}

{#if settingsOpen}
  <SettingsDialog {t} settings={$appState.settings} onclose={() => (settingsOpen = false)} />
{/if}

{#if dialog}
  <Modal
    {t}
    title={dialog.title}
    message={dialog.message || ""}
    input={Boolean(dialog.input)}
    inputLabel={dialog.inputLabel || ""}
    inputValue={dialog.inputValue || ""}
    placeholder={dialog.placeholder || ""}
    folder={Boolean(dialog.folder)}
    folderLabel={dialog.folderLabel || ""}
    folderValue={dialog.folderValue || ""}
    folders={dialog.folders || []}
    confirmLabel={dialog.confirmLabel || ""}
    danger={Boolean(dialog.danger)}
    validate={dialog.validate || null}
    onresolve={onDialogResolve}
  />
{/if}

{#if $toastStore}
  <div class="toast toast--{$toastStore.tone}" role="status">{$toastStore.message}</div>
{/if}

<!-- 拖拽拖影：渲染在 App 顶层，脱离侧栏列表的 overflow 裁切。 -->
{#if dragState}
  <div
    class="drag-ghost"
    class:is-folder={dragState.kind === "folder"}
    class:is-over-target={dragState.overTarget}
    style:left={`${dragState.x}px`}
    style:top={`${dragState.y}px`}
    aria-hidden="true"
  >
    {#if dragState.kind === "folder"}
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
      <span class="drag-ghost-name">{dragState.name}</span>
    {:else}
      <span class="proto-badge is-{protocolTone(dragState.item.protocol)}"
        >{dragState.item.protocol === "grpc" ? "GRPC" : "HTTP"}</span
      >
      {#if shouldShowMethod(dragState.item)}
        <span class="method-tag is-{methodTone(entryMethod(dragState.item))}">{entryMethod(dragState.item)}</span>
      {/if}
      <span class="drag-ghost-name">{dragState.item.name || t("untitled")}</span>
    {/if}
  </div>
{/if}
