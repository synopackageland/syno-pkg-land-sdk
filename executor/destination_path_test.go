package main

import (
	"context"
	"strings"
	"testing"
)

func TestDestinationNameAcceptsSubfolders(t *testing.T) {
	for _, name := range []string{
		"out.csv",
		"Album/CD1/01.flac",
		"Show/Season 1/E01 (1080p).mkv",
		"\u55ae\u66f2/\u6b4c.mp3", // non-ASCII folder and file names
		"a/b/" + strings.Repeat("x", 255),
		strings.Repeat("d/", maxDestinationFolders) + "f",
	} {
		if code := destinationNameCode(name); code != "" {
			t.Errorf("%q: %s", name, code)
		}
	}
}

func TestDestinationNameRejectsUnsafeSubpaths(t *testing.T) {
	for name, want := range map[string]string{
		"/abs/f":                         "invalid-path",
		"a//f":                           "invalid-path",
		"a/./f":                          "invalid-path",
		"a/../f":                         "invalid-path",
		"../f":                           "invalid-path",
		"a/f/":                           "invalid-path",
		"a\x01b/f":                       "invalid-path",
		"a/f\n":                          "invalid-path",
		"a:b/f":                          "invalid-path",
		"a*/f":                           "invalid-path",
		`a\b/f`:                          "invalid-path",
		"a?/f":                           "invalid-path",
		`"a"/f`:                          "invalid-path",
		"<a>/f":                          "invalid-path",
		"a|b/f":                          "invalid-path",
		"#recycle/f":                     "invalid-path",
		"x/@eaDir/f":                     "invalid-path",
		"#Snapshot/f":                    "invalid-path",
		"\xff/f":                         "invalid-path",
		strings.Repeat("x", 256) + "/f":  "invalid-path",
		"a/" + strings.Repeat("x", 256):  "invalid-path",
		strings.Repeat("abcdefgh/", 120): "invalid-path",
		strings.Repeat("d/", maxDestinationFolders+1) + "f": "path-too-deep",
	} {
		if code := destinationNameCode(name); code != want {
			t.Errorf("%q: got %q, want %q", name, code, want)
		}
	}
}

// A name without a folder keeps the historical rule and its INVALID_REQUEST.
func TestDestinationNameWithoutFolderKeepsLegacyRule(t *testing.T) {
	for _, name := range []string{"", "..", "a..b", "x\x00"} {
		if code := destinationNameCode(name); code != "INVALID_REQUEST" {
			t.Errorf("%q: %s", name, code)
		}
	}
	for _, name := range []string{"a:b", "a*b", `a\b`, "#recycle"} {
		if code := destinationNameCode(name); code != "" {
			t.Errorf("legacy %q: %s", name, code)
		}
	}
}

func TestDestinationFoldersSplitsLeaf(t *testing.T) {
	folders, leaf := destinationFolders("Album/CD1/01.flac")
	if strings.Join(folders, "|") != "Album|CD1" || leaf != "01.flac" {
		t.Fatal(folders, leaf)
	}
	folders, leaf = destinationFolders("out.csv")
	if len(folders) != 0 || leaf != "out.csv" {
		t.Fatal(folders, leaf)
	}
}

func TestMoveAdmitsSubfolderNameAndReceiptsFullPath(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	w := &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	e.Writer = w
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	p := moveParams(id, "nested")
	p["name"] = "Album/CD1/01.flac"
	if r := offline(e, "files.moveFromStaging", p); !r.OK {
		t.Fatal(r)
	}
	<-w.copying
	close(w.release)
	op := waitState(t, e, "nested", "committed")
	if op.Path != "/home/Album/CD1/01.flac" {
		t.Fatal(op.Path)
	}
}

func TestMoveRefusesUnsafeSubpathAtAdmission(t *testing.T) {
	e, _, _ := setup(t)
	id := longGrant(t, e)
	e.Writer = &barrierMoveWriter{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	if err := e.startOperations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.stopOperations()
	for name, want := range map[string]string{"a/../f": "invalid-path", strings.Repeat("d/", maxDestinationFolders+1) + "f": "path-too-deep", "..": "INVALID_REQUEST"} {
		p := moveParams(id, "bad")
		p["name"] = name
		if r := offline(e, "files.moveFromStaging", p); r.OK || r.Code != want {
			t.Errorf("%q: %+v", name, r)
		}
	}
}
