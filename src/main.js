import "./styles.css";
import App from "./App.svelte";
import { mount } from "svelte";

const target = document.getElementById("app");

/**
 * Svelte 5 的 `mount` 在该版本的类型定义里，`Renderer` 默认 `undefined` 却被
 * `Renderer extends MountRenderer` 判定进了自定义渲染器分支，导致 `{ target }`
 * 被要求补一个并不存在的 `renderer`。运行时与构建都正常，这里只在类型层做一次
 * 断言，避免为了绕开检查而改变调用方式。
 */
const app = mount(App, /** @type {any} */ ({ target }));

export default app;
