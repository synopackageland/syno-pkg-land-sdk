---
name: Native DSM identity surface
description: Synology account sign-in within the inherited embedded DSM theme.
---

# Design System: Identity

## Overview

This Operate surface inherits the frontend design system and public
`@synopackageland/ui` controls with `themes/dsm7-embedded.css`. It introduces no
new palette, global tokens or decorative identity. The task is signing in to the
app with an account on this NAS.

## Layout

The centered form has a maximum width of 36rem, theme spacing and vertical
overflow. Keep `Form` in automatic direction: labels stack below 520px of
container width. Actions follow the fields and retain the library's responsive
layout and keyboard focus treatment.

## Components

Account stays visible with `autocomplete="username"`; password is masked with
`autocomplete="current-password"`. HTTPS is required and insecure-page sign-in
controls are disabled.

When DSM requests OTP, the verification field visually replaces the password
field. The password remains only in component memory for verification; success,
restart, other errors and unmount clear it. OTP uses numeric input hints and
`autocomplete="one-time-code"`. Focus moves to OTP after busy clears and the
field renders.

Busy state disables competing actions and shows “Signing in…”. Danger callouts
provide safe errors and retry guidance; “Use another account” restarts OTP.
“Continue with DSM” attempts the browser session. Session expiry returns to
sign-in with a notice; expiry and successful sign-out clear loaded app data,
greeting text and feedback.

## Do's and Don'ts

Use inherited controls and theme semantics. Local HTTPS fixture captures at
1100 by 740 and 390 by 640 pixels verify credential and OTP layout. Keyboard
submission, enabled OTP focus, app data loading and sign-out were exercised
locally. The OTP focus regression is resolved. This evidence does not establish NAS visual acceptance.
