# Frontend bundles

The CLI's DSM window supports two entries in `ui.dir`:

| Entry | Contract |
| --- | --- |
| `app.js` | Classic script registering `window.DsmPackageApps[packageID].mount(root, ctx)` |
| `app.module.js` | ES module exporting `mount(root, ctx)` |

Both entries require `app.css`. When both JavaScript entries exist, the module
entry takes precedence. Use CLI 0.34.0 and templates 0.32.0 with SDK 0.32.0.
The frontend SDK API and backend language do not change.

The Vue template builds ES modules. Keep `base: './'`, emit `app.module.js`
and `app.css`, and allow additional chunks. For example:

```ts
export default defineConfig({
  base: './',
  build: {
    lib: {
      entry: 'src/main.ts',
      formats: ['es'],
      fileName: () => 'app.module.js',
    },
    cssCodeSplit: false,
    rollupOptions: {
      output: {
        chunkFileNames: 'chunks/[name]-[hash].js',
        assetFileNames: asset =>
          asset.names?.some(name => name.endsWith('.css'))
            ? 'app.css'
            : 'assets/[name]-[hash][extname]',
      },
    },
  },
})
```

Keep a per-window mount and its synchronous cleanup contract:

```ts
export function mount(root, ctx) {
  const app = createApp(App, {ctx})
  app.mount(root)
  return {unmount() {app.unmount()}}
}
```

A route can use `() => import('./ChatPage.vue')` or Vue's
`defineAsyncComponent(() => import('./ChatPage.vue'))`. Do not set
`inlineDynamicImports: true`. Keep module imports relative; a leading `/`
resolves against DSM's origin instead of the installed package directory.
The module entry must export `mount` directly; `document.currentScript` is not
available while a module evaluates.

The CLI copies the whole UI output, including chunks, styles, fonts and images,
into `ui/app/<tree-sha256>/`. The entry and stylesheet load from that directory;
relative imports, CSS URLs and `import.meta.url` stay inside the same snapshot.
The `.js` suffix uses DSM's JavaScript MIME mapping. Every byte or pathname
change gives the entire snapshot a new URL, including changes in a lazy chunk
with an unchanged entry file. The build inventory records the snapshot digest,
and `synopkgland inspect` verifies the archived tree against it.

DSM supplies static response headers; the CLI does not install a separate
server or change DSM's nginx configuration. The snapshot URLs do not rely on
ETag or Last-Modified revalidation to distinguish builds. A proxy may cache
successful snapshot responses as immutable; avoid caching missing files or
overriding JavaScript/CSS MIME types. The mutable `ui/config` update probe
continues to use `cache: 'no-store'`. Classic Apps keep their existing paths
and version query parameters.

An already open page can finish using modules it downloaded before an upgrade.
An unopened lazy chunk from an older, removed snapshot can return 404 after
the package updates. Show the SDK shell's update notice and reload the page;
do not substitute a newer chunk into an older module graph. This packaging
change does not retain prior package versions.
