"""Installer folder selection through the package-account SDK controller."""
import json
import os
import re
import subprocess
from pathlib import Path

# The shared work allowance (spec/protocol/v1/timeouts.json request.default_s);
# a slow controller is not a failure.
OPERATION_SECONDS = 300


class SharedFolderConfig:
    """Configure trusted package paths once; never pass user-supplied paths."""

    def __init__(self, package_id, data_dir=None, payload_root=None):
        if not re.fullmatch(r"[A-Za-z][A-Za-z0-9]{1,63}", package_id):
            raise ValueError("invalid package ID")
        self.package_id = package_id
        self.root = str(payload_root or "/var/packages/{}/target".format(package_id))
        self.data = str(data_dir or "/var/packages/{}/var".format(package_id))
        if not Path(self.root).is_absolute() or not Path(self.data).is_absolute():
            raise ValueError("package paths must be absolute")

    def require(self, key):
        """Re-resolve the selection and require its manifest's RO/RW access."""
        if not re.fullmatch(r"[a-z][a-z0-9_]{0,31}", key):
            raise ValueError("invalid folder key")
        result = subprocess.run(
            [str(Path(self.root) / "install/control"), "folder-path", "--root", self.root,
             "--package", self.package_id, "--key", key],
            env=dict(os.environ, SYNOPKG_PKGVAR=self.data),
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=OPERATION_SECONDS, check=False,
        )
        if result.returncode:
            raise OSError(result.stderr[:16384].decode("utf-8", errors="replace").strip()
                          or "Selected folder is unavailable")
        if len(result.stdout) > 16384:
            raise ValueError("invalid SDK folder response")
        path = json.loads(result.stdout)["path"]
        if not isinstance(path, str) or not Path(path).is_absolute():
            raise ValueError("invalid SDK folder response")
        return path
