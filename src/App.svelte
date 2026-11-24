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
    deleteSaved,
    initialize,
    invokeGrpc,
    newRequest,
    openHistory,
    openSaved,
    refreshTranslator,
    saveCurrent,
    sendHttp,
    setProtocol,
    showToast,
    state as appState,
    toast as toastStore,
  } from "./lib/store.js";
  import { suggestName, toSnapshot } from "./lib/model.js";
  import SavedTree from "./components/SavedTree.svelte";
  import HistoryList from "./components/HistoryList.svelte";
  import HttpPanel from "./components/HttpPanel.svelte";
  import GrpcPanel from "./components/GrpcPanel.svelte";
  import ResponsePanel from "./components/ResponsePanel.svelte";
  import Modal from "./components/Modal.svelte";

  const t = $derived($tStore);

  /** 当前模态框请求；null 表示不显示。 */
  let dialog = $state(null);
  /** 新建菜单。 */
  let sideMenu = $state(null);
  let menuPosition = $state({ left: 8, top: 0 });

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

  function openNewMenu(anchor) {
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
      items: [{ label: t("newRequest"), run: () => newRequest() }],
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

  async function onDialogResolve(result) {
    const pending = dialog;
    dialog = null;
    if (!pending || result === null || result === false) return;

    switch (pending.kind) {
      case "save":
        await saveCurrent({ name: result.name }, pending.asNew);
        break;
      case "confirm-delete":
        await deleteSaved(pending.id);
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
      void saveCurrent({ name: s.activeName }, false);
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
      confirmLabel: t("save"),
      validate: (value) => (value.name ? "" : t("needName")),
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
  <div class="app">
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
          aria-label={t("newRequest")}
          title={t("newRequest")}
          onclick={(event) => openNewMenu(event.currentTarget)}>+</button
        >
      </div>
      <div class="side-list">
        <SavedTree
          {t}
          savedIndex={$appState.savedIndex}
          activeSavedId={$appState.activeSavedId}
          onopen={(id) => void openSaved(id)}
          ondelete={(id) => confirmDelete(id)}
        />
      </div>
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
          <button
            class="btn btn-ghost btn-sm"
            type="button"
            title={t("saveAs")}
            onclick={() => save(true)}>{t("saveAs")}</button
          >
          <button
            class="btn btn-secondary btn-sm"
            type="button"
            title={t("save")}
            onclick={() => save(false)}>{t("save")}</button
          >
        </div>
      </header>

      <section class="editor">
        {#if $appState.protocol === "http"}
          <HttpPanel {t} http={$appState.http} sending={$appState.sending} />
        {:else}
          <GrpcPanel {t} grpc={$appState.grpc} sending={$appState.sending} discovering={$appState.discovering} />
        {/if}
      </section>

      <ResponsePanel {t} response={$appState.response} responseTab={$appState.responseTab} />
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

{#if dialog}
  <Modal
    {t}
    title={dialog.title}
    message={dialog.message || ""}
    input={Boolean(dialog.input)}
    inputLabel={dialog.inputLabel || ""}
    inputValue={dialog.inputValue || ""}
    placeholder={dialog.placeholder || ""}
    confirmLabel={dialog.confirmLabel || ""}
    danger={Boolean(dialog.danger)}
    validate={dialog.validate || null}
    onresolve={onDialogResolve}
  />
{/if}

{#if $toastStore}
  <div class="toast toast--{$toastStore.tone}" role="status">{$toastStore.message}</div>
{/if}
