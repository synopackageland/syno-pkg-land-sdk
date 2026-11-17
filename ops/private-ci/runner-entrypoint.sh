#!/bin/bash
set -euo pipefail
# The single-use JIT config arrives as a 0600 file and is deleted at once. It is
# handed to the runner through its ACTIONS_RUNNER_INPUT_ environment input, never
# argv (argv is world-readable in the process table). The runner removes the
# variable from its own environment on startup, so job steps do not inherit it.
ACTIONS_RUNNER_INPUT_JITCONFIG=$(cat /home/runner/.jitconfig)
rm /home/runner/.jitconfig
export ACTIONS_RUNNER_INPUT_JITCONFIG
cd /home/runner
mkdir -p /cache/go/mod /cache/go/build /cache/cargo/registry /cache/cargo/git /cache/npm
mkdir -p /home/runner/.cargo
ln -s /cache/cargo/registry /home/runner/.cargo/registry
ln -s /cache/cargo/git /home/runner/.cargo/git
if test -d .bundles; then
  mkdir -p deps
  for dep in sdk templates; do
    test -f ".bundles/$dep.bundle" || continue
    git clone --quiet ".bundles/$dep.bundle" "deps/$dep"
    rm ".bundles/$dep.bundle"
  done
  rmdir .bundles
  if test -d deps/sdk/.git; then
    git config --global --add url.file:///home/runner/deps/sdk.insteadOf https://github.com/synopackageland/syno-pkg-land-sdk
    git config --global --add url.file:///home/runner/deps/sdk.insteadOf https://github.com/synopackageland/syno-pkg-land-sdk.git
  fi
fi
exec ./run.sh
