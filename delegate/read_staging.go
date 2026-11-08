package delegate

import (
	"os"
	"path/filepath"
)

// A memory reply names a file, never a server-supplied directory. Resolve its
// root next to this client's trusted Unix socket so path aliases of appdata
// cannot redirect read outputs to persistent storage.
func openMemoryReadStaging(client ExecutorClient) (*os.Root, error) {
	c, ok := client.(*Client)
	if !ok || !filepath.IsAbs(c.Socket) {
		return nil, fail(CodeExecutorProtocol)
	}
	return openPrivateReadStaging(filepath.Join(filepath.Dir(c.Socket), "read-staging"))
}

func openPrivateReadStaging(dir string) (*os.Root, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fail("STAGING_UNAVAILABLE")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fail("STAGING_UNAVAILABLE")
	}
	return root, nil
}
