Run Vite from ts/ and open /test/fixtures/executor-approval/index.html with
view=approval|notice|save, admin=true|false and locale (en, zh-TW, ...). It uses
the real SDK components and the embedded UI theme with a fake executor. It does
not contact a NAS. fixture-password approves; any other password is refused.
