package main

import (
	"strconv"
	"strings"
)

// What a write or move does when the file name is taken. The empty policy is
// "fail", the historical behavior, and is stored as "" so it stays implicit.
const (
	conflictOverwrite = "overwrite"
	conflictRename    = "rename"
)

// maxRenameAttempts bounds the names tried under the publish lock: "a (1).txt"
// to "a (99).txt". After that the move fails with destination-exists.
const maxRenameAttempts = 99

type writeConflictKey struct{}

func validConflict(c string) bool {
	return c == "fail" || c == conflictOverwrite || c == conflictRename
}

// storedConflict normalizes an admitted policy for storage and comparison.
func storedConflict(c string) string {
	if c == "fail" {
		return ""
	}
	return c
}

// renameCandidate inserts " (n)" before the file extension, as DSM does. The
// folders and a leading dot (".bashrc") are left alone.
func renameCandidate(name string, n int) string {
	folders, leaf := destinationFolders(name)
	stem, ext := leaf, ""
	if i := strings.LastIndex(leaf, "."); i > 0 {
		stem, ext = leaf[:i], leaf[i:]
	}
	return strings.Join(append(folders, stem+" ("+strconv.Itoa(n)+")"+ext), "/")
}

// receiptName is the destination name a committed move published. Only a
// rename may differ from the request, and only to one of its own candidates.
func receiptName(op Operation, out MoveReceipt) string {
	if op.Conflict != conflictRename || out.Name == "" {
		return op.Name
	}
	for n := 1; n <= maxRenameAttempts; n++ {
		if out.Name == renameCandidate(op.Name, n) {
			return out.Name
		}
	}
	return op.Name
}
