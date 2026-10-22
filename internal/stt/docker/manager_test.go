package sttdocker

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// fakeRunner answers docker commands by their first argument(s); unmatched commands succeed silently.
type fakeRunner struct {
	calls   [][]string
	stdin   map[string][]byte
	answers map[string]func(args []string) (string, error)
	lines   map[string][]string
}

func newFake() *fakeRunner {
	return &fakeRunner{stdin: map[string][]byte{}, answers: map[string]func([]string) (string, error){}, lines: map[string][]string{}}
}

func (f *fakeRunner) on(prefix string, fn func(args []string) (string, error)) { f.answers[prefix] = fn }

func (f *fakeRunner) Run(_ context.Context, stdin io.Reader, line func(string), args ...string) (string, error) {
	f.calls = append(f.calls, args)
	key := strings.Join(args, " ")
	if stdin != nil {
		data, _ := io.ReadAll(stdin)
		f.stdin[args[0]] = data
	}
	best := ""
	for prefix := range f.answers {
		if strings.HasPrefix(key, prefix) && len(prefix) > len(best) {
			best = prefix
		}
	}
	for _, l := range f.lines[args[0]] {
		if line != nil {
			line(l)
		}
	}
	if best == "" {
		return "", nil
	}
	return f.answers[best](args)
}

func (f *fakeRunner) called(prefix string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			out = append(out, c)
		}
	}
	return out
}

func ok(out string) func([]string) (string, error) {
	return func([]string) (string, error) { return out, nil }
}

func fail(msg string) func([]string) (string, error) {
	return func([]string) (string, error) { return "", errors.New(msg) }
}

func healthy(runner *fakeRunner) {
	runner.on("version", ok("29.5.3"))
	runner.on("context inspect", ok("npipe:////./pipe/dockerDesktopLinuxEngine"))
	runner.on("image inspect", ok("sha256:abc"))
	runner.on("run --rm --gpus all --entrypoint nvidia-smi", ok("GPU 0: NVIDIA GeForce RTX 3070 Ti Laptop GPU (UUID: GPU-8bc8)"))
	runner.on("container inspect", fail("No such container: solveo-stt"))
}

func writeModel(t *testing.T, dir string) {
	t.Helper()
	m := filepath.Join(dir, ModelDirName)
	if err := os.MkdirAll(m, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"model-00001-of-00002.safetensors", "model-00002-of-00002.safetensors"} {
		if err := os.WriteFile(filepath.Join(m, name), []byte("w"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(m, "REVISION"), []byte(ModelRevision+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func manager(t *testing.T, runner *fakeRunner) *Manager {
	t.Helper()
	return &Manager{
		Runner:       runner,
		ModelsDir:    t.TempDir(),
		Getenv:       func(string) string { return "" },
		Health:       func(context.Context, string, string) (bool, error) { return true, nil },
		Image:        "solveo-stt:0123456789ab",
		Context:      fstest.MapFS{"Dockerfile": {Data: []byte("FROM base\n")}, "solveo_stt/__main__.py": {Data: []byte("")}},
		PollInterval: time.Millisecond,
	}
}

func states(checks []Check) string {
	var parts []string
	for _, c := range checks {
		parts = append(parts, c.ID+"="+c.State)
	}
	return strings.Join(parts, " ")
}

func TestStatusAllReady(t *testing.T) {
	runner := newFake()
	healthy(runner)
	m := manager(t, runner)
	writeModel(t, m.ModelsDir)
	got := m.Status(context.Background())
	if s := states(got); s != "docker=ok engine=ok image=ok gpu=ok model=ok" {
		t.Fatalf("status %s", s)
	}
	if !Ready(got) {
		t.Fatal("all checks ok must be ready")
	}
	if got[3].Message != "GPU 0: NVIDIA GeForce RTX 3070 Ti Laptop GPU" {
		t.Fatalf("gpu message %q", got[3].Message)
	}
}

func TestStatusDockerMissing(t *testing.T) {
	runner := newFake()
	runner.on("version", func([]string) (string, error) { return "", &exec.Error{Name: "docker", Err: exec.ErrNotFound} })
	got := manager(t, runner).Status(context.Background())
	if s := states(got); s != "docker=missing engine=unknown image=unknown gpu=unknown model=missing" {
		t.Fatalf("status %s", s)
	}
}

func TestStatusEngineStopped(t *testing.T) {
	runner := newFake()
	runner.on("version", fail("Cannot connect to the Docker daemon"))
	got := manager(t, runner).Status(context.Background())
	if s := states(got); !strings.HasPrefix(s, "docker=ok engine=error image=unknown gpu=unknown") {
		t.Fatalf("status %s", s)
	}
}

func TestRemoteEngineRejected(t *testing.T) {
	runner := newFake()
	healthy(runner)
	m := manager(t, runner)
	m.Getenv = func(k string) string {
		if k == "DOCKER_HOST" {
			return "tcp://10.0.0.5:2376"
		}
		return ""
	}
	got := m.Status(context.Background())
	if got[1].State != StateError || !strings.Contains(got[1].Message, "только локальный Docker") {
		t.Fatalf("engine check %+v", got[1])
	}
	runner.on("context inspect", ok("tcp://10.0.0.5:2376"))
	m.Getenv = func(string) string { return "" }
	if got = m.Status(context.Background()); got[1].State != StateError {
		t.Fatalf("remote context accepted: %+v", got[1])
	}
}

func TestGPUUnknownUntilImageExists(t *testing.T) {
	runner := newFake()
	healthy(runner)
	runner.on("image inspect", fail("No such image"))
	got := manager(t, runner).Status(context.Background())
	if got[2].State != StateMissing || got[3].State != StateUnknown {
		t.Fatalf("status %s", states(got))
	}
	if len(runner.called("run")) != 0 {
		t.Fatal("GPU check must not run without the image")
	}
}

func TestPrepareBuildsThenDownloadsThenChecksGPU(t *testing.T) {
	runner := newFake()
	healthy(runner)
	built := false
	runner.on("image inspect", func([]string) (string, error) {
		if built {
			return "sha256:abc", nil
		}
		return "", errors.New("No such image")
	})
	runner.on("build", func([]string) (string, error) { built = true; return "", nil })
	m := manager(t, runner)
	runner.on("run --rm --label solveo.component=stt -v", func([]string) (string, error) {
		writeModel(t, m.ModelsDir)
		return "", nil
	})
	runner.lines["build"] = []string{"#5 [2/4] RUN pip install"}
	var progress []string
	if err := m.Prepare(context.Background(), func(l string) { progress = append(progress, l) }); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range runner.calls {
		if c[0] == "build" || (c[0] == "run" && c[1] == "--rm") {
			order = append(order, strings.Join(c, " "))
		}
	}
	modelDir := filepath.Join(m.ModelsDir, ModelDirName)
	want := []string{
		"build --progress=plain -t solveo-stt:0123456789ab --label solveo.component=stt -",
		"run --rm --label solveo.component=stt -v " + modelDir + ":/out solveo-stt:0123456789ab download --out /out",
		"run --rm --gpus all --entrypoint nvidia-smi solveo-stt:0123456789ab -L",
	}
	if strings.Join(order, "\n") != strings.Join(want, "\n") {
		t.Fatalf("order:\n%s", strings.Join(order, "\n"))
	}
	names := tarNames(t, runner.stdin["build"])
	if strings.Join(names, ",") != "Dockerfile,solveo_stt/__main__.py" {
		t.Fatalf("build context %v", names)
	}
	if len(progress) == 0 || progress[len(progress)-1] == "" {
		t.Fatalf("progress not reported: %v", progress)
	}
}

func tarNames(t *testing.T, data []byte) []string {
	t.Helper()
	r := tar.NewReader(strings.NewReader(string(data)))
	var names []string
	for {
		h, err := r.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

func TestPrepareSkipsWhatExists(t *testing.T) {
	runner := newFake()
	healthy(runner)
	m := manager(t, runner)
	writeModel(t, m.ModelsDir)
	if err := m.Prepare(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(runner.called("build")) != 0 || len(runner.called("run --rm --label")) != 0 {
		t.Fatalf("prepare redid existing work: %v", runner.calls)
	}
}

func TestPrepareRequiresEngine(t *testing.T) {
	runner := newFake()
	runner.on("version", fail("Cannot connect"))
	err := manager(t, runner).Prepare(context.Background(), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "Docker") {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestStartPublishesLocalhostOnlyWithToken(t *testing.T) {
	runner := newFake()
	healthy(runner)
	m := manager(t, runner)
	writeModel(t, m.ModelsDir)
	var healthAddr, healthToken string
	m.Health = func(_ context.Context, addr, token string) (bool, error) {
		healthAddr, healthToken = addr, token
		return true, nil
	}
	ep, err := m.Start(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	runs := runner.called("run -d")
	if len(runs) != 1 {
		t.Fatalf("expected one container start, got %v", runner.calls)
	}
	args := strings.Join(runs[0], " ")
	port := strings.TrimPrefix(ep.Addr, "127.0.0.1:")
	for _, want := range []string{
		"--name solveo-stt", "--label solveo.component=stt", "--gpus all", "--shm-size 2g", "--memory 12g",
		"-p 127.0.0.1:" + port + ":8765", "-e SOLVEO_STT_TOKEN=" + ep.Token,
		filepath.Join(m.ModelsDir, ModelDirName) + ":/model:ro", "solveo-stt:0123456789ab serve --model /model",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("run args %q lack %q", args, want)
		}
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(ep.Token) {
		t.Fatalf("token %q", ep.Token)
	}
	if healthAddr != ep.Addr || healthToken != ep.Token {
		t.Fatal("health polled a different endpoint")
	}
}

func TestStartRemovesOnlyOwnContainer(t *testing.T) {
	runner := newFake()
	healthy(runner)
	m := manager(t, runner)
	writeModel(t, m.ModelsDir)
	runner.on("container inspect", ok("stt"))
	if _, err := m.Start(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(runner.called("rm -f solveo-stt")) != 1 {
		t.Fatalf("own stale container not removed: %v", runner.calls)
	}

	runner = newFake()
	healthy(runner)
	m.Runner = runner
	runner.on("container inspect", ok(""))
	_, err := m.Start(context.Background(), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "принадлежит не Solveo") {
		t.Fatalf("foreign container: %v", err)
	}
	if len(runner.called("rm")) != 0 || len(runner.called("run -d")) != 0 {
		t.Fatalf("foreign container touched: %v", runner.calls)
	}
}

func TestStartFailsFastWhenContainerExits(t *testing.T) {
	runner := newFake()
	healthy(runner)
	m := manager(t, runner)
	writeModel(t, m.ModelsDir)
	started := false
	runner.on("run -d", func([]string) (string, error) { started = true; return "id", nil })
	runner.on("container inspect", func(args []string) (string, error) {
		if !started {
			return "", errors.New("No such container")
		}
		if strings.Contains(strings.Join(args, " "), "Running") {
			return "false", nil
		}
		return "stt", nil
	})
	runner.on("logs", ok("RuntimeError: CUDA out of memory"))
	m.Health = func(context.Context, string, string) (bool, error) { return false, fmt.Errorf("connection refused") }
	_, err := m.Start(context.Background(), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "CUDA out of memory") {
		t.Fatalf("expected container log in error, got %v", err)
	}
}

func TestStartTimesOut(t *testing.T) {
	runner := newFake()
	healthy(runner)
	m := manager(t, runner)
	writeModel(t, m.ModelsDir)
	started := false
	runner.on("run -d", func([]string) (string, error) { started = true; return "id", nil })
	runner.on("container inspect", func(args []string) (string, error) {
		if !started {
			return "", errors.New("No such container")
		}
		if strings.Contains(strings.Join(args, " "), "Running") {
			return "true", nil
		}
		return "stt", nil
	})
	m.Health = func(context.Context, string, string) (bool, error) { return false, nil }
	m.ReadyTimeout = 20 * time.Millisecond
	_, err := m.Start(context.Background(), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "не загрузилась") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if len(runner.called("rm -f solveo-stt")) == 0 {
		t.Fatal("a container that never got ready must be removed")
	}
}

func TestStopRemovesOwnContainerOnly(t *testing.T) {
	runner := newFake()
	healthy(runner)
	runner.on("container inspect", ok("stt"))
	m := manager(t, runner)
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.called("rm -f solveo-stt")) != 1 {
		t.Fatalf("stop did not remove: %v", runner.calls)
	}
	runner = newFake()
	runner.on("container inspect", fail("No such container"))
	m.Runner = runner
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("stopping an absent container must succeed: %v", err)
	}
}
