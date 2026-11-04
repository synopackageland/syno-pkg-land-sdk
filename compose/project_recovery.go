package compose

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// ProjectRecoveryHint supplies Package Center hook logs with a recovery path.
// The docker-project worker runs after preinst, outside the App's privileges;
// an App cannot catch its acquisition errors or edit its root-owned inventory.
// This is conditional guidance, not evidence that a conflict exists.
func ProjectRecoveryHint(project string) string {
	f, err := os.OpenFile(filepath.Join(project, "compose.lock.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 4<<20 {
		return ""
	}
	var lock struct {
		Schema      int    `json:"schema"`
		Package     string `json:"package"`
		ProjectName string `json:"project_name"`
	}
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	if d.Decode(&lock) != nil || d.Decode(new(any)) != io.EOF || lock.Schema != PlannerVersion || !packageName.MatchString(lock.Package) || !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`).MatchString(lock.ProjectName) {
		return ""
	}
	return fmt.Sprintf("[COMPOSE_PROJECT_RECOVERY_HINT] If Container Manager reports a duplicate project during install or repair, use an administrator's synopkgland install %s --repair with a CLI that supports owned-project recovery. For Package Center-only recovery, verify that the stopped project %s belongs to this App at /var/packages/%s/target/compose; preserve volumes, data files and images when removing its registration, then repair this App again. Never remove a same-name project with a different owner or path", lock.Package, lock.ProjectName, lock.Package)
}
