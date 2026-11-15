// Package scripts holds tests for the shell scripts in this directory.
package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sandbox makes a HOME whose PATH has only the stubs plus /usr/bin:/bin,
// and skips the test if a real telegraph would be found there.
func sandbox(t *testing.T) (home, stubs string, env []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	home = t.TempDir()
	stubs = filepath.Join(home, "stubs")
	if err := os.MkdirAll(stubs, 0o755); err != nil {
		t.Fatal(err)
	}
	path := stubs + ":/usr/bin:/bin"
	for _, dir := range filepath.SplitList("/usr/bin:/bin") {
		if _, err := os.Stat(filepath.Join(dir, "telegraph")); err == nil {
			t.Skip("telegraph is installed in " + dir)
		}
	}
	env = []string{"HOME=" + home, "PATH=" + path, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".state"), "LANG=C"}
	return home, stubs, env
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeTelegraph logs its arguments and answers just enough for node-join.sh.
func fakeTelegraph(t *testing.T, home string) (bin, log string) {
	bin = filepath.Join(home, "custom", "telegraph-test")
	log = filepath.Join(home, "calls.log")
	write(t, bin, `#!/bin/sh
echo "$*" >>"`+log+`"
case $1 in
init) mkdir -p "$TELEGRAPH_HOME"; echo '{"entries":[{}]}' >"$TELEGRAPH_HOME/data.json" ;;
resolve) exit 1 ;;
config) echo deadbeef ;;
esac
`, 0o755)
	return bin, log
}

func envFile(t *testing.T, home, node, extra string) string {
	path := filepath.Join(home, ".config", "telegraph", node+".env")
	write(t, path, "TELEGRAPH_HOME="+filepath.Join(home, "th-"+node)+"\nTELEGRAPH_BOTS=123:test\nTELEGRAPH_COMMITTEE="+strings.Repeat("cd", 32)+"\n"+extra, 0o600)
	return path
}

func run(t *testing.T, env []string, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{script}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// node-join.sh must run the binary named by TELEGRAPH_BIN in the node's env
// file, not whatever telegraph is first on PATH.
func TestNodeJoinUsesTelegraphBin(t *testing.T) {
	home, stubs, env := sandbox(t)
	bin, log := fakeTelegraph(t, home)
	envFile(t, home, "node9", "TELEGRAPH_BIN="+bin+"\n")
	write(t, filepath.Join(stubs, "systemctl"), "#!/bin/sh\nexit 1\n", 0o755)

	out, err := run(t, env, "node-join.sh", "node9", strings.Repeat("ab", 32), "node9")
	if !strings.Contains(out, "no systemd user session") {
		t.Fatalf("expected the no-systemd hint (err %v):\n%s", err, out)
	}
	calls := read(t, log)
	for _, want := range []string{"init", "resolve node9@nym", "register --handle node9@nym", "sync --secs 10", "config key"} {
		if !strings.Contains(calls, want) {
			t.Errorf("TELEGRAPH_BIN not called with %q; calls:\n%s\noutput:\n%s", want, calls, out)
		}
	}
	if !strings.Contains(out, "deadbeef") {
		t.Errorf("node key missing:\n%s", out)
	}
}

// install-service.sh accepts a TELEGRAPH_BIN with no telegraph on PATH, and
// the unit lets each node's env file pick its binary.
func TestInstallServiceHonoursTelegraphBin(t *testing.T) {
	home, stubs, env := sandbox(t)
	bin, _ := fakeTelegraph(t, home)
	envFile(t, home, "node9", "TELEGRAPH_BIN="+bin+"\n")
	sysLog := filepath.Join(home, "systemctl.log")
	write(t, filepath.Join(stubs, "systemctl"), "#!/bin/sh\necho \"$*\" >>"+sysLog+"\n", 0o755)
	write(t, filepath.Join(stubs, "loginctl"), "#!/bin/sh\necho yes\n", 0o755)

	if out, err := run(t, env, "install-service.sh", "node9"); err != nil {
		t.Fatalf("install-service: %v\n%s", err, out)
	}
	unit := read(t, filepath.Join(home, ".config", "systemd", "user", "telegraph@.service"))
	want := `ExecStart=/bin/sh -c 'exec "$${TELEGRAPH_BIN:-` + filepath.Join(home, "go", "bin", "telegraph") + `}" sync --serve'`
	if !strings.Contains(unit, want) {
		t.Fatalf("unit lacks %s:\n%s", want, unit)
	}
	if !strings.Contains(read(t, sysLog), "enable --now telegraph@node9") {
		t.Fatal("service not enabled")
	}
}

// telegraph-boot.sh starts each node's supervisor once, however often it runs,
// and skips env files with no token or TELEGRAPH_BOOT=0.
func TestBootStartsEachNodeOnce(t *testing.T) {
	home, _, env := sandbox(t)
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps")
	}
	pids := filepath.Join(home, "pids")
	sup := filepath.Join(home, "fake", "telegraph-supervise.sh")
	write(t, sup, "echo $$ >>"+pids+"\nwhile :; do sleep 1; done\n", 0o755)
	t.Cleanup(func() {
		b, _ := os.ReadFile(pids)
		for _, f := range strings.Fields(string(b)) {
			_ = exec.Command("kill", f).Run()
		}
	})
	env = append(env, "TELEGRAPH_SUPERVISE="+sup)
	envFile(t, home, "node8", "")
	if err := os.Mkdir(filepath.Join(home, "th-node8"), 0o700); err != nil {
		t.Fatal(err)
	}
	envFile(t, home, "off", "TELEGRAPH_BOOT=0\n")
	write(t, filepath.Join(home, ".config", "telegraph", "empty.env"), "TELEGRAPH_BOTS=\n", 0o600)

	boot, _ := filepath.Abs("telegraph-boot.sh")
	out, err := run(t, env, boot)
	if err != nil || !strings.Contains(out, "started node8") || strings.Contains(out, "off") || strings.Contains(out, "empty") {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	for i := 0; i < 3; i++ {
		if out, err := run(t, env, boot, "-q"); err != nil || out != "" {
			t.Fatalf("rerun %d: %v %q", i, err, out)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(strings.Fields(read(t, pids))); n != 1 {
		t.Fatalf("%d supervisors started, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(home, "th-node8", "node8.log")); err != nil {
		t.Fatalf("log not in TELEGRAPH_HOME: %v", err)
	}
}
