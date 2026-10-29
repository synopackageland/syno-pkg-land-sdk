#!/usr/bin/env python3
"""Publish only a clean packageDirectory returned by prepare-npm-publish.py."""
import argparse
import json
from pathlib import Path
import runpy
import subprocess

validate_manifest = runpy.run_path(
    str(Path(__file__).with_name('prepare-npm-publish.py'))
)['validate_manifest']


def publish(package_directory, userconfig):
    directory = package_directory.resolve()
    if not directory.is_dir() or not (directory / 'package.json').is_file():
        raise ValueError('Expected a prepared package directory; tarball paths are forbidden')
    validate_manifest(json.loads((directory / 'package.json').read_text()))
    config = userconfig.resolve(strict=True)
    if config.is_relative_to(directory):
        raise ValueError('Keep the private npm userconfig outside the package directory')
    return subprocess.run([
        'npm', 'publish', '--ignore-scripts', '--access', 'public',
        '--userconfig', str(config),
    ], cwd=directory).returncode


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('package_directory', type=Path)
    parser.add_argument('--userconfig', type=Path, required=True)
    args = parser.parse_args()
    raise SystemExit(publish(args.package_directory, args.userconfig))
