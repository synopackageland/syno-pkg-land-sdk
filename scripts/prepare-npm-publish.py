#!/usr/bin/env python3
"""Prepare a clean directory from a gated npm archive. Does not publish."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile

REMOVED = {'_from', '_resolved', '_where', '_args', '_id', '_integrity', '_shasum', '_spec', '_requested'}

def validate_manifest(value):
    if isinstance(value, dict):
        for key, child in value.items():
            if key in REMOVED:
                raise ValueError('Publish manifest contains installation metadata')
            validate_manifest(child)
    elif isinstance(value, list):
        for child in value:
            validate_manifest(child)
    elif isinstance(value, str):
        if value.startswith(('file:', 'link:')) or '/Users/' in value or '\\Users\\' in value:
            raise ValueError('Publish manifest contains a local filesystem reference')

def prepare(artifact, destination):
    destination.mkdir(parents=True, exist_ok=False)
    package = destination / 'package'
    package.mkdir()
    expected = {}
    with tarfile.open(artifact, 'r:gz') as archive:
        for member in archive:
            path = Path(member.name)
            if path.is_absolute() or '..' in path.parts or not path.parts or path.parts[0] != 'package':
                raise ValueError('Unsafe archive path')
            if member.isdir():
                continue
            if not member.isfile() or member.name in expected:
                raise ValueError('Unsupported or duplicate archive member')
            data = archive.extractfile(member).read()
            if member.name == 'package/package.json':
                manifest = json.loads(data)
                for key in REMOVED:
                    manifest.pop(key, None)
                validate_manifest(manifest)
                data = (json.dumps(manifest, ensure_ascii=False, indent=2) + '\n').encode()
            target = destination / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
            target.chmod(member.mode & 0o777)
            expected[member.name] = hashlib.sha256(data).hexdigest()
    if 'package/package.json' not in expected:
        raise ValueError('Missing package manifest')
    packed = json.loads(subprocess.check_output([
        'npm', 'pack', '--ignore-scripts', '--json', '--pack-destination', str(destination.resolve())
    ], cwd=package))
    tarball = destination / packed[0]['filename']
    with tarfile.open(tarball) as archive:
        actual = {m.name: hashlib.sha256(archive.extractfile(m).read()).hexdigest()
                  for m in archive if m.isfile()}
    if actual != expected:
        raise ValueError('Clean-directory pack changed gated package contents')
    return {'packageDirectory': str(package.resolve()), 'tarball': str(tarball.resolve()),
            'name': manifest['name'], 'version': manifest['version'], 'files': len(actual)}

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('artifact', type=Path)
    parser.add_argument('destination', type=Path, help='New private staging directory')
    args = parser.parse_args()
    print(json.dumps(prepare(args.artifact.resolve(), args.destination.resolve()), indent=2))
