package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// A move may name folders below the granted destination, e.g. "Album/CD1/01.flac".
// Sixteen folders cover real album, season and torrent layouts while bounding
// the directories one call creates as the user; 1024 bytes keeps the full path
// far below PATH_MAX (4096) under any Shared Folder or home root.
const (
	maxDestinationFolders = 16
	maxDestinationBytes   = 1024
	maxSegmentBytes       = 255
)

// Folder names exclude what DSM and its SMB/AFP clients reject or reserve.
// The file name keeps the historical validName rule so existing calls behave
// exactly as before. Names are not Unicode-normalized: the share stores the
// bytes the App sends, as File Station and SMB do, and the receipt path must
// equal the requested one for retries and conflict checks.
const reservedFolderRunes = `\:*?"<>|`

var reservedFolders = []string{"#recycle", "#snapshot", "@eadir"}

func validFolder(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) <= maxSegmentBytes && !strings.ContainsAny(s, reservedFolderRunes) && !strings.ContainsFunc(s, unicode.IsControl) && !contains(reservedFolders, strings.ToLower(s))
}

// destinationNameCode returns "" for a valid destination name, INVALID_REQUEST
// for a folder-less name the legacy rule refuses, and a stable code otherwise.
func destinationNameCode(name string) string {
	if !strings.Contains(name, "/") {
		if validName(name) {
			return ""
		}
		return "INVALID_REQUEST"
	}
	folders, leaf := destinationFolders(name)
	if len(name) > maxDestinationBytes || !utf8.ValidString(name) || !validName(leaf) || len(leaf) > maxSegmentBytes {
		return "invalid-path"
	}
	for _, folder := range folders {
		if !validFolder(folder) {
			return "invalid-path"
		}
	}
	if len(folders) > maxDestinationFolders {
		return "path-too-deep"
	}
	return ""
}
func validDestinationName(name string) bool { return destinationNameCode(name) == "" }

// destinationFolders splits a destination name into its folders and file name.
func destinationFolders(name string) ([]string, string) {
	parts := strings.Split(name, "/")
	return parts[:len(parts)-1], parts[len(parts)-1]
}
