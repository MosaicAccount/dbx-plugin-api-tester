<script>
  /**
   * 带 JSON 高亮的代码编辑器。
   *
   * 用「透明 textarea 叠在高亮层上」实现：textarea 负责输入与光标，高亮层只做上色。
   * 两层滚动位置必须同步，否则输入时光标会和文字错位。
   */
  import { highlightJSON, escapeHTML } from "../lib/format.js";

  let {
    value = "",
    placeholder = "",
    disabled = false,
    json = false,
    ariaLabel = "",
    onchange = () => {},
  } = $props();

  let textarea = $state(null);
  let highlight = $state(null);

  const html = $derived(`${json ? highlightJSON(value) : escapeHTML(value)} `);

  function syncScroll() {
    if (!textarea || !highlight) return;
    highlight.scrollTop = textarea.scrollTop;
    highlight.scrollLeft = textarea.scrollLeft;
  }

  $effect(() => {
    // value 变化后高亮层高度可能改变，重新对齐一次滚动位置。
    void value;
    syncScroll();
  });
</script>

<div class="code-editor">
  <pre class="code-highlight" aria-hidden="true" bind:this={highlight}>{@html html}</pre>
  <textarea
    class="code-area"
    spellcheck="false"
    {placeholder}
    {disabled}
    aria-label={ariaLabel || undefined}
    bind:this={textarea}
    {value}
    oninput={(event) => onchange(event.currentTarget.value)}
    onscroll={syncScroll}
  ></textarea>
</div>
