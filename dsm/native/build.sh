#!/bin/sh
set -eu
# Zig 0.16.0 cross-compiles this small libc/dlopen bridge. No proprietary SDK
# headers or libraries are distributed. GNU ABI 2.17 covers every DSM target.
cd "$(dirname "$0")"
[ "$(zig version)" = "0.16.0" ] || { echo 'requires Zig 0.16.0' >&2; exit 2; }
build() { # <arch> <zig target> [compiler flags]
  arch=$1 target=$2
  shift 2
  for bridge in admin apppriv account; do
    zig cc "${bridge}_check.c" -target "$target" "$@" -std=c11 -O2 -Wall -Wextra -Werror -fPIE -pie -fstack-protector-strong -D_FORTIFY_SOURCE=2 -Wl,-z,relro,-z,now -Wl,--build-id=none -s -ldl -o "$bridge-$arch.elf"
  done
}
build x86_64 x86_64-linux-gnu.2.17
build aarch64 aarch64-linux-gnu.2.17
# ARMv7-A, VFPv3 and NEON, no integer divide: the Cortex-A9 (armada38x,
# monaco) and Cortex-A15 (alpine, alpine4k) models both run it.
build armv7 arm-linux-gnueabihf.2.17 -mcpu=baseline
