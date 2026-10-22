Run Vite from ts/ and open /test/fixtures/executor-approval/index.html with
view=approval|notice|save, admin=true|false and locale (en, zh-TW, ...). It uses
the real SDK components and the embedded UI theme with a fake executor. It does
not contact a NAS. fixture-password approves; any other password is refused.

Add upgrade=1 for protected-runtime upgrade diagnostics. The fixture loads the
UI repository's synthetic DSM host reset before dsm7-embedded.css; it exercises
host style isolation without copying any proprietary DSM CSS. Test at 1100×900
and 700×900 with locale=en and locale=zh-TW. The executor is a fake; this proves
layout, password handling and return to App content, not DSM installation.
