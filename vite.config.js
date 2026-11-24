import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vite";

/**
 * DBX 会内联 ui/index.html 里引用的入口脚本与样式表，因此必须使用相对 base，
 * 否则产物会生成 /assets/... 这类绝对路径，加载时解析到宿主根目录而 404。
 */
export default defineConfig({
  base: "./",
  plugins: [
    svelte(),
    {
      name: "dbx-build-signal",
      /**
       * dbx-plugin dev 的 ui_watch 依赖这一行输出来触发页面重载。
       *
       * 必须挂在 closeBundle 的成功路径上，且不能被失败覆盖：这里用 writeBundle
       * 而不是 closeBundle 之前那样无条件打印 —— writeBundle 只在产物真正写出后调用，
       * 构建失败时不会走到这里，避免开发宿主把坏产物当成新版本去重载。
       */
      writeBundle() {
        console.log("DBX_UI_BUILD_SUCCESS");
      },
    },
  ],
  build: {
    outDir: "ui",
    emptyOutDir: true,
    target: "es2022",
    sourcemap: false,
  },
});
