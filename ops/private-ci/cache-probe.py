#!/usr/bin/env python3
"""Opt-in repeatable compile/install workload for measuring private CI caches."""
import json
import os
from pathlib import Path
import subprocess
import time


def main():
    expected = {'GOMODCACHE': '/cache/go/mod', 'GOCACHE': '/cache/go/build',
                'SCCACHE_DIR': '/cache/cargo/sccache', 'NPM_CONFIG_CACHE': '/cache/npm'}
    if os.getuid() != 1001 or any(os.environ.get(k) != v for k, v in expected.items()):
        raise RuntimeError('cache probe requires the private CI environment')
    if Path('/var/run/docker.sock').exists() or Path('/run/secrets/github_token').exists():
        raise RuntimeError('unexpected privileged access')
    results = {}
    started = time.monotonic()
    for name, command in [
        ('go', ['go', 'build', './...']),
        ('rust', ['cargo', 'build', '--locked', '--manifest-path', 'rust/Cargo.toml', '--lib']),
        ('npm', ['npm', '--prefix', 'node', 'ci', '--ignore-scripts', '--no-audit', '--no-fund']),
    ]:
        before = time.monotonic()
        subprocess.run(command, check=True)
        results[name + '_seconds'] = round(time.monotonic() - before, 3)
    results['total_seconds'] = round(time.monotonic() - started, 3)
    subprocess.run(['sccache', '--show-stats'], check=True)
    print('CACHE_PROBE ' + json.dumps(results, sort_keys=True), flush=True)


if __name__ == '__main__':
    main()
