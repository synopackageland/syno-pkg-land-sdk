package sharedfolder

import "fmt"

// SelectionError is a saved folder that needs repair, not an ACL grant.
// The existing startup dialog renders it without exposing arbitrary I/O errors.
type SelectionError struct {
	Name, Subpath, Account, Reason string
}

func (e *SelectionError) Error() string {
	return fmt.Sprintf("Selected folder %q for package account %q is unavailable: %s. In File Station > %s > Properties > Permission, check the folder and its parent permissions; restore it or choose another folder in Package Center > the App > Update/Repair > Folder, then Retry", e.Name+"/"+e.Subpath, e.Account, e.Reason, e.Name+"/"+e.Subpath)
}
