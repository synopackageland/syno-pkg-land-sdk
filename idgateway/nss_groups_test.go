package idgateway

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const directoryID = `uid=2969567732(EXAMPLE\Administrator) gid=2969567745(EXAMPLE\Domain Users) groups=2969567745(EXAMPLE\Domain Users),2969567751(EXAMPLE\Enterprise Admins)`

func TestNSSParserPreservesQualifiedNamesAndFailsClosed(t *testing.T) {
	for _, text := range []string{directoryID + "\n", `uid=123(alice@example.test) gid=200(staff@example.test) groups=200(staff@example.test),201(Domain Users@example.test)`} {
		groups, err := parseDirectoryGroups([]byte(text))
		if err != nil || len(groups) != 2 {
			t.Fatalf("groups=%q err=%v", groups, err)
		}
	}
	for _, text := range []string{"", "100 101", `uid=1(alice) gid=2(staff) groups=2`, `uid=1(alice) gid=2(staff) groups=3(ops)`, `uid=1(alice) gid=2(staff) groups=2(staff),3`, `uid=1(alice) gid=2(staff) groups=2(staff),3(bad(name))`, directoryID + "\n" + directoryID, `uid=1(alice) gid=2(staff) groups=2(staff),2(ops)`} {
		if _, err := parseDirectoryGroups([]byte(text)); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
}

func TestDirectoryCommandIsBoundedAndHasNoSessionEnvironment(t *testing.T) {
	var gotPath string
	var gotArgs, gotEnv []string
	lookup := directoryLookup{timeout: 20 * time.Millisecond, run: func(ctx context.Context, path string, args, env []string) ([]byte, error) {
		gotPath, gotArgs, gotEnv = path, args, env
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		return []byte(directoryID), nil
	}}
	if _, err := lookup.groups(context.Background(), `EXAMPLE\Administrator`); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/usr/bin/id" || !slices.Equal(gotArgs, []string{`EXAMPLE\Administrator`}) || !slices.Equal(gotEnv, []string{"LC_ALL=C"}) {
		t.Fatal(gotPath, gotArgs, gotEnv)
	}
	for _, user := range []string{"", "-G", "--help", "alice\nadmin", "alice;id"} {
		gotPath = ""
		if _, err := lookup.groups(context.Background(), user); err == nil || gotPath != "" {
			t.Fatal(user, err, gotPath)
		}
	}
	lookup.run = func(ctx context.Context, _ string, _, _ []string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if _, err := lookup.groups(context.Background(), "alice"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestNSSParserBoundsIDsGroupsAndOutput(t *testing.T) {
	base := `uid=1(alice) gid=2(staff) groups=2(staff)`
	for _, text := range []string{strings.Replace(base, "uid=1", "uid=4294967295", 1), strings.Replace(base, "gid=2", "gid=4294967296", 1), base + strings.Repeat(",3(ops)", 256), base + strings.Repeat(" ", maxDirectoryOutput), strings.Replace(base, "2(staff)", "02(staff)", -1)} {
		if _, err := parseDirectoryGroups([]byte(text)); err == nil {
			t.Fatal("accepted out-of-bounds output")
		}
	}
	var output directoryOutput
	if _, err := output.Write(make([]byte, maxDirectoryOutput)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte{1}); err == nil || len(output.data) != maxDirectoryOutput {
		t.Fatal("output not bounded")
	}
}

func TestNSSNonzeroExitAndCancellationDiscardEvenValidGroups(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		d := directoryLookup{timeout: time.Second, run: func(context.Context, string, []string, []string) ([]byte, error) {
			if cancelled {
				cancel()
				return []byte(directoryID), nil
			}
			return []byte(directoryID), errors.New("exit 1")
		}}
		if groups, err := d.groups(ctx, `EXAMPLE\Administrator`); err == nil || len(groups) != 0 {
			t.Fatal(groups, err)
		}
	}
}
