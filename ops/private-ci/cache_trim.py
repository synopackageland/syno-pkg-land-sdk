"""Bounded private CI cache pruning. No traversal of host paths or symlinks."""
import argparse
import errno
import json
import os
from pathlib import Path
import shutil
import stat
import time

LIMIT = 15 * 1024**3
MAX_AGE = 3 * 86400
ROOTS = (Path('/cache/go'), Path('/cache/cargo'), Path('/cache/npm'))


def entries(path):
    """lstat only; never walk a symlink, including at a cache root."""
    info = path.lstat()
    yield path, info
    if stat.S_ISDIR(info.st_mode):
        with os.scandir(path) as children:
            for entry in children:
                yield from entries(Path(entry.path))


def usage(roots):
    # Allocated bytes count real disk pressure (sparse fixtures cannot fake it).
    total, seen = 0, set()
    for root in roots:
        for _, info in entries(root):
            key = (info.st_dev, info.st_ino)
            if key not in seen:
                total += info.st_blocks * 512
                seen.add(key)
    return total


def remove(path):
    if path.is_symlink() or not path.is_dir():
        path.unlink()
    else:
        # Go's module cache intentionally has read-only extracted directories.
        for p, info in entries(path):
            if stat.S_ISDIR(info.st_mode):
                p.chmod(stat.S_IMODE(info.st_mode) | 0o700)
        shutil.rmtree(path)


def old_files(root, now, use_atime):
    removed = 0
    if not root.exists() or root.is_symlink():
        return removed
    # Only files, never directories, and never follow links out of this volume.
    for path, info in entries(root):
        if stat.S_ISREG(info.st_mode):
            used = max(info.st_mtime, info.st_atime) if use_atime else info.st_mtime
            if used < now - MAX_AGE:
                path.unlink()
                removed += 1
    return removed


def trim_thread_cache(root, now):
    """Clean the shared cache without crossing symlinks or changing permissions."""
    result = {'thread_gobuild': True, 'removed_files': 0, 'skipped_entries': 0, 'age_days': 3}
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    expected = {errno.ENOENT, errno.ENOTDIR, errno.ELOOP, errno.EACCES, errno.EPERM}

    def visit(fd):
        for name in os.listdir(fd):
            try:
                info = os.stat(name, dir_fd=fd, follow_symlinks=False)
                if stat.S_ISDIR(info.st_mode):
                    child = os.open(name, flags, dir_fd=fd)
                    try:
                        visit(child)
                    finally:
                        os.close(child)
                elif stat.S_ISREG(info.st_mode) and info.st_mtime < now - MAX_AGE:
                    os.unlink(name, dir_fd=fd)
                    result['removed_files'] += 1
            except OSError as error:
                if error.errno not in expected:
                    raise
                # Other gates may replace entries during this walk. This
                # helper also cannot write directories owned by other users.
                result['skipped_entries'] += 1

    fd = os.open(root, flags)
    try:
        visit(fd)
    finally:
        os.close(fd)
    return result


def units(root, category):
    """Prune complete source/index/checkout entries, not partial source trees."""
    if root.is_symlink():
        raise ValueError('symlink cache root')
    for child in root.iterdir():
        if child.is_symlink() or not child.is_dir():
            yield child
        elif category == 'go' and child.name == 'mod':
            # A whole module-domain directory is an eviction unit. Coarse but
            # safe for Go's immutable extracted modules and download metadata.
            yield from child.iterdir()
        elif category == 'cargo' and child.name == 'registry':
            for kind in child.iterdir():
                if kind.is_symlink() or not kind.is_dir():
                    yield kind
                elif kind.name in ('src', 'cache'):
                    for registry in kind.iterdir():
                        if registry.is_symlink() or not registry.is_dir():
                            yield registry
                        else:
                            yield from registry.iterdir()
                else:
                    yield from kind.iterdir()
        elif category == 'cargo' and child.name == 'git':
            for kind in child.iterdir():
                if kind.is_symlink() or not kind.is_dir():
                    yield kind
                else:
                    yield from kind.iterdir()
        else:
            # Build/npm/sccache entries are content-addressed or regenerate
            # missing data. Prefix directories are evicted atomically.
            yield from child.iterdir()


def trim(roots=ROOTS, limit=LIMIT, now=None):
    if limit <= 0:
        raise ValueError('limit must be positive')
    now = time.time() if now is None else now
    for root in roots:
        if root.is_symlink() or not root.is_dir():
            raise ValueError('cache root must be a real directory')
    stale = old_files(roots[0] / 'build', now, use_atime=True)
    before = usage(roots)
    removed = 0
    if before > limit:
        candidates = []
        for root, category in zip(roots, ('go', 'cargo', 'npm')):
            for path in units(root, category):
                stats = [s for _, s in entries(path)]
                latest = max(max(s.st_mtime, s.st_atime) if stat.S_ISREG(s.st_mode)
                             else s.st_mtime for s in stats)
                size = sum(s.st_blocks * 512 for s in stats)
                candidates.append((latest, str(path), path, size))
        # Aggregate accounting includes directory overhead. Read back disk
        # usage after each eviction instead of assuming apparent file length.
        estimate = before
        for _, _, path, size in sorted(candidates):
            remove(path)
            removed += 1
            estimate -= size
            if estimate <= int(limit * 0.8):
                estimate = usage(roots)
                if estimate <= int(limit * 0.8):
                    break
    after = usage(roots)
    if after > limit:
        raise RuntimeError('cache still exceeds limit')
    return {'limit': limit, 'before': before, 'after': after,
            'stale_go_files': stale, 'evicted_units': removed}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--thread-gobuild', action='store_true')
    args = parser.parse_args()
    if args.thread_gobuild:
        # This mount is provided only to the daily helper, never to a job.
        result = trim_thread_cache(Path('/thread-gobuild'), time.time())
    else:
        result = trim()
    print(json.dumps(result, sort_keys=True))


if __name__ == '__main__':
    main()
