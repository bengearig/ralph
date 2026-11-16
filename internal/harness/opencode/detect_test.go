package opencode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const helpWithFormat = "echo '  --format  output format'"

// fakeOpenCode installs an opencode script that dispatches on its first
// argument: version handles --version, help handles run --help.
func fakeOpenCode(t *testing.T, version, help string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n" + version + "\nelse\n" + help + "\nfi\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func emptyPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func detect(root string) (d struct {
	Found                        bool
	Version, Detail, Unsupported string
}) {
	r := New().Detect(context.Background(), root)
	d.Found, d.Version, d.Detail, d.Unsupported = r.Found, r.Version, r.Detail, r.Unsupported
	return d
}

func TestOpenCodeDetectSupported(t *testing.T) {
	path := fakeOpenCode(t, `echo "opencode v2.3.0"`, helpWithFormat)
	d := detect(t.TempDir())
	if !d.Found || d.Version != "2.3.0" || d.Unsupported != "" || d.Detail != "opencode command at "+path {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectVersionWithSuffix(t *testing.T) {
	fakeOpenCode(t, `echo "opencode v3.0.0-beta.1"`, helpWithFormat)
	if d := detect(t.TempDir()); d.Version != "3.0.0-beta.1" || d.Unsupported != "" {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectVersionWithoutPrefixV(t *testing.T) {
	fakeOpenCode(t, `echo "2.0.1"`, helpWithFormat)
	if d := detect(t.TempDir()); d.Version != "2.0.1" || d.Unsupported != "" {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectOtherProject(t *testing.T) {
	fakeOpenCode(t, `echo "opencode v2.3.0"`, `echo "usage: opencode run [--model m]"`)
	d := detect(t.TempDir())
	if !d.Found || d.Unsupported != otherProject {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectOldMajor(t *testing.T) {
	fakeOpenCode(t, `echo "opencode v1.4.2"`, helpWithFormat)
	d := detect(t.TempDir())
	if !d.Found || d.Version != "1.4.2" || d.Unsupported != "ralph supports OpenCode 2 and later (found 1.4.2)" {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectOtherProjectWinsOverOldMajor(t *testing.T) {
	fakeOpenCode(t, `echo "opencode v1.4.2"`, `echo "usage"`)
	if d := detect(t.TempDir()); d.Unsupported != otherProject {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectHelpOnStderr(t *testing.T) {
	fakeOpenCode(t, `echo "opencode v2.3.0"`, `echo "  --format  output format" >&2`)
	if d := detect(t.TempDir()); d.Unsupported != "" || d.Version != "2.3.0" {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectVersionOnlyStdout(t *testing.T) {
	fakeOpenCode(t, `echo "9.9.9 noise" >&2; echo hello`, helpWithFormat)
	if d := detect(t.TempDir()); d.Version != "" {
		t.Fatalf("stderr leaked: %+v", d)
	}
}

func TestOpenCodeDetectVersionUnparsable(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "help-ran")
	fakeOpenCode(t, `echo "hello there"`, "touch "+marker+"\n"+helpWithFormat)
	d := detect(t.TempDir())
	if !d.Found || d.Version != "" || !strings.HasPrefix(d.Detail, "could not read the version: unrecognized output") || d.Unsupported != "" {
		t.Fatalf("got %+v", d)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("help check did not run: %v", err)
	}
}

func TestOpenCodeDetectVersionExitFailure(t *testing.T) {
	fakeOpenCode(t, `exit 1`, helpWithFormat)
	d := detect(t.TempDir())
	if !d.Found || d.Version != "" || d.Detail != "could not read the version: exited with status 1" {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectVersionTimeout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	fakeOpenCode(t, `echo $$ > `+pidFile+`; exec sleep 30`, helpWithFormat)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	d := New().Detect(ctx, t.TempDir())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %v", elapsed)
	}
	if !d.Found || d.Version != "" || !strings.HasPrefix(d.Detail, "could not read the version: timed out") {
		t.Fatalf("got %+v", d)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %d still running", pid)
}

func TestOpenCodeDetectHelpFailureKeepsSupportedWhenVersionOk(t *testing.T) {
	fakeOpenCode(t, `echo "opencode v2.3.0"`, `exit 3`)
	d := detect(t.TempDir())
	if !d.Found || d.Version != "2.3.0" || d.Unsupported != "" || !strings.HasSuffix(d.Detail, `; could not check "run --format": exited with status 3`) {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectHelpFailureStillRejectsOldMajor(t *testing.T) {
	fakeOpenCode(t, `echo "opencode v1.0.0"`, `exit 3`)
	if d := detect(t.TempDir()); d.Unsupported != "ralph supports OpenCode 2 and later (found 1.0.0)" {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectRawOutputTruncated(t *testing.T) {
	fakeOpenCode(t, `printf 'x%.0s' $(seq 1 500); echo`, helpWithFormat)
	d := detect(t.TempDir())
	if !strings.Contains(d.Detail, strings.Repeat("x", 80)) || strings.Contains(d.Detail, strings.Repeat("x", 81)) {
		t.Fatalf("got %q", d.Detail)
	}
}

func TestOpenCodeDetectNoCommandWithDirectory(t *testing.T) {
	emptyPath(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := detect(root)
	if !d.Found || d.Version != "" || d.Unsupported != "" || d.Detail != "no opencode command on the path; found .opencode" {
		t.Fatalf("got %+v", d)
	}
}

func testNoCommandWithFile(t *testing.T, file string) {
	t.Helper()
	emptyPath(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, file), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := detect(root)
	if !d.Found || d.Version != "" || d.Detail != "no opencode command on the path; found "+file {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectNoCommandWithJSONFile(t *testing.T) { testNoCommandWithFile(t, "opencode.json") }

func TestOpenCodeDetectNoCommandWithJSONCFile(t *testing.T) {
	testNoCommandWithFile(t, "opencode.jsonc")
}

func TestOpenCodeDetectNoCommandIgnoresWrongKinds(t *testing.T) {
	emptyPath(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".opencode"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "opencode.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if d := detect(root); d.Found {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectNothing(t *testing.T) {
	emptyPath(t)
	d := detect(t.TempDir())
	if d.Found || d.Version != "" || d.Unsupported != "" || d.Detail != "no opencode command on the path and no .opencode directory or opencode.json file" {
		t.Fatalf("got %+v", d)
	}
}

func TestOpenCodeDetectDoesNotReadStdin(t *testing.T) {
	fakeOpenCode(t, `if read -r line; then echo "line: $line"; else echo "2.3.0"; fi`, helpWithFormat)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.WriteString("input\n"); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	if d := detect(t.TempDir()); d.Version != "2.3.0" {
		t.Fatalf("stdin inherited: %+v", d)
	}
}

func TestOpenCodeDetectWritesNothing(t *testing.T) {
	fakeOpenCode(t, `echo "opencode v2.3.0"`, helpWithFormat)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	list := func() []string {
		var names []string
		_ = filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
			names = append(names, p)
			return nil
		})
		return names
	}
	before := list()
	detect(root)
	if after := list(); !reflect.DeepEqual(before, after) {
		t.Fatalf("listing changed: %v -> %v", before, after)
	}
}
