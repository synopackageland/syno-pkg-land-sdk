# Plain HTTP DSM

Browser code must work when DSM is opened over HTTP on the local network.
Use the headless `requestKey()` export for request deduplication. It uses
`crypto.getRandomValues`, available on HTTP DSM, and does not require
`crypto.randomUUID` or `crypto.subtle`. Request keys are not authority.

The legacy `DelegatePanel` uses the same helper and retains its submission key
after a failed or lost reply. A retry must preserve the original input and key.
The current `BackgroundSavePanel` and File Station nonce generator already use
`getRandomValues`.

The frontend SDK uses no service worker, SubtleCrypto, browser file-system
picker or required Clipboard API. The UI package's `writeClipboard()` tries
`navigator.clipboard.writeText` when available and falls back to a selected
textarea with `document.execCommand('copy')`. A failed copy returns `false` so
the App can offer manual selection. Optional clipboard reads must leave the
normal paste field usable when the API or permission is unavailable.

Apps that ship the legacy `DelegatePanel` must rebuild after adopting the SDK
fix. Updating a library does not change JavaScript already bundled in an SPK.
