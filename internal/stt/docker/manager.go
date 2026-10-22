// Package sttdocker prepares, starts and stops the solveo-stt container through the docker CLI.
package sttdocker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/97Dmitry/solveo/internal/stt/worker"
)

const (
	ContainerName = "solveo-stt"
	labelKey      = "solveo.component"
	labelValue    = "stt"
	ModelDirName  = "Qwen3-ASR-1.7B-7278e1e"
	ModelRevision = "7278e1e70fe206f11671096ffdd38061171dd6e5"
	containerPort = 8765
)

// Check states.
const (
	StateOK      = "ok"
	StateMissing = "missing"
	StateError   = "error"
	StateUnknown = "unknown"
)

// Check is one readiness item: docker, engine, image, gpu, model (in this order).
type Check struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
}

// Ready reports whether the model can be started.
func Ready(checks []Check) bool {
	for _, c := range checks {
		if c.State != StateOK {
			return false
		}
	}
	return len(checks) > 0
}

// Endpoint is where a started worker listens.
type Endpoint struct {
	Addr  string
	Token string
}

type Manager struct {
	Runner    Runner
	ModelsDir string
	Getenv    func(string) string
	Health    func(ctx context.Context, addr, token string) (bool, error)
	// Image and Context default to the embedded worker; tests replace them.
	Image        string
	Context      fs.FS
	ReadyTimeout time.Duration
	PollInterval time.Duration
}

func (m *Manager) image() string {
	if m.Image != "" {
		return m.Image
	}
	return worker.Tag()
}

func (m *Manager) context() fs.FS {
	if m.Context != nil {
		return m.Context
	}
	return worker.Context
}

func (m *Manager) modelDir() string { return filepath.Join(m.ModelsDir, ModelDirName) }

func (m *Manager) run(ctx context.Context, args ...string) (string, error) {
	return m.Runner.Run(ctx, nil, nil, args...)
}

func (m *Manager) engine(ctx context.Context) (docker, engine Check) {
	docker = Check{ID: "docker", State: StateOK, Message: "Docker установлен"}
	engine = Check{ID: "engine", State: StateOK}
	version, err := m.run(ctx, "version", "--format", "{{.Server.Version}}")
	if errors.Is(err, exec.ErrNotFound) {
		docker = Check{ID: "docker", State: StateMissing, Message: "Docker не найден: установите Docker Desktop с WSL2"}
		return docker, Check{ID: "engine", State: StateUnknown}
	}
	if err != nil {
		return docker, Check{ID: "engine", State: StateError, Message: "Docker не запущен: запустите Docker Desktop"}
	}
	host := m.Getenv("DOCKER_HOST")
	if host == "" {
		host, err = m.run(ctx, "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
		if err != nil {
			return docker, Check{ID: "engine", State: StateError, Message: "Не удалось определить Docker context: " + err.Error()}
		}
	}
	if !strings.HasPrefix(host, "npipe://") && !strings.HasPrefix(host, "unix://") {
		return docker, Check{ID: "engine", State: StateError, Message: "Поддерживается только локальный Docker, а не " + host}
	}
	engine.Message = "Docker запущен, версия " + version
	return docker, engine
}

func (m *Manager) imageCheck(ctx context.Context) Check {
	if _, err := m.run(ctx, "image", "inspect", "--format", "{{.Id}}", m.image()); err != nil {
		return Check{ID: "image", State: StateMissing, Message: "Образ " + m.image() + " не собран"}
	}
	return Check{ID: "image", State: StateOK, Message: "Образ " + m.image() + " собран"}
}

func (m *Manager) gpuCheck(ctx context.Context) Check {
	out, err := m.run(ctx, "run", "--rm", "--gpus", "all", "--entrypoint", "nvidia-smi", m.image(), "-L")
	if err != nil {
		return Check{ID: "gpu", State: StateError, Message: "GPU недоступна в Docker: " + err.Error()}
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	first, _, _ = strings.Cut(first, " (UUID")
	return Check{ID: "gpu", State: StateOK, Message: first}
}

func (m *Manager) modelCheck() Check {
	dir := m.modelDir()
	revision, err := os.ReadFile(filepath.Join(dir, "REVISION"))
	if err != nil || strings.TrimSpace(string(revision)) != ModelRevision {
		return Check{ID: "model", State: StateMissing, Message: "Модель не скачана"}
	}
	for _, name := range []string{"model-00001-of-00002.safetensors", "model-00002-of-00002.safetensors"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return Check{ID: "model", State: StateMissing, Message: "Модель скачана не полностью"}
		}
	}
	return Check{ID: "model", State: StateOK, Message: "Модель Qwen3-ASR-1.7B скачана"}
}

// Status inspects the environment without changing it.
func (m *Manager) Status(ctx context.Context) []Check {
	docker, engine := m.engine(ctx)
	image := Check{ID: "image", State: StateUnknown}
	gpu := Check{ID: "gpu", State: StateUnknown, Message: "Проверяется после подготовки"}
	if engine.State == StateOK {
		image = m.imageCheck(ctx)
		if image.State == StateOK {
			gpu = m.gpuCheck(ctx)
		}
	}
	return []Check{docker, engine, image, gpu, m.modelCheck()}
}

// Prepare builds the image and downloads the model when missing, then verifies GPU access.
func (m *Manager) Prepare(ctx context.Context, progress func(string)) error {
	docker, engine := m.engine(ctx)
	for _, c := range []Check{docker, engine} {
		if c.State != StateOK {
			return errors.New(c.Message)
		}
	}
	if m.imageCheck(ctx).State != StateOK {
		progress("Сборка образа " + m.image())
		context, err := tarContext(m.context())
		if err != nil {
			return err
		}
		args := []string{"build", "--progress=plain", "-t", m.image(), "--label", labelKey + "=" + labelValue, "-"}
		if _, err := m.Runner.Run(ctx, bytes.NewReader(context), progress, args...); err != nil {
			return fmt.Errorf("не удалось собрать образ: %w", err)
		}
	}
	if m.modelCheck().State != StateOK {
		progress("Загрузка модели Qwen3-ASR-1.7B")
		if err := os.MkdirAll(m.modelDir(), 0o700); err != nil {
			return err
		}
		args := []string{"run", "--rm", "--label", labelKey + "=" + labelValue, "-v", m.modelDir() + ":/out", m.image(), "download", "--out", "/out"}
		if _, err := m.Runner.Run(ctx, nil, progress, args...); err != nil {
			return fmt.Errorf("не удалось скачать модель: %w", err)
		}
		if c := m.modelCheck(); c.State != StateOK {
			return errors.New(c.Message)
		}
	}
	progress("Проверка GPU")
	if c := m.gpuCheck(ctx); c.State != StateOK {
		return errors.New(c.Message)
	}
	progress("Готово")
	return nil
}

// owned reports whether solveo-stt exists and whether Solveo created it.
func (m *Manager) owned(ctx context.Context) (exists, ours bool) {
	label, err := m.run(ctx, "container", "inspect", "--format", `{{index .Config.Labels "`+labelKey+`"}}`, ContainerName)
	if err != nil {
		return false, false
	}
	return true, strings.TrimSpace(label) == labelValue
}

func (m *Manager) remove(ctx context.Context) {
	_, _ = m.run(ctx, "rm", "-f", ContainerName)
}

// Start runs the worker and waits until its model is warmed up.
func (m *Manager) Start(ctx context.Context, progress func(string)) (Endpoint, error) {
	if m.imageCheck(ctx).State != StateOK || m.modelCheck().State != StateOK {
		return Endpoint{}, errors.New("сначала подготовьте распознавание")
	}
	if exists, ours := m.owned(ctx); exists {
		if !ours {
			return Endpoint{}, errors.New("контейнер solveo-stt принадлежит не Solveo; удалите или переименуйте его")
		}
		m.remove(ctx)
	}
	port, err := freePort()
	if err != nil {
		return Endpoint{}, err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Endpoint{}, err
	}
	ep := Endpoint{Addr: "127.0.0.1:" + strconv.Itoa(port), Token: hex.EncodeToString(raw[:])}
	progress("Запуск контейнера " + ContainerName)
	_, err = m.run(ctx, "run", "-d", "--name", ContainerName, "--label", labelKey+"="+labelValue,
		"--gpus", "all", "--shm-size", "2g", "--memory", "12g",
		"-p", fmt.Sprintf("%s:%d", ep.Addr, containerPort), "-e", "SOLVEO_STT_TOKEN="+ep.Token,
		"-v", m.modelDir()+":/model:ro", m.image(), "serve", "--model", "/model")
	if err != nil {
		m.remove(ctx)
		return Endpoint{}, fmt.Errorf("не удалось запустить контейнер: %w", err)
	}
	progress("Загрузка модели в видеопамять")
	if err := m.waitReady(ctx, ep); err != nil {
		m.remove(context.WithoutCancel(ctx))
		return Endpoint{}, err
	}
	return ep, nil
}

func (m *Manager) waitReady(ctx context.Context, ep Endpoint) error {
	timeout, poll := m.ReadyTimeout, m.PollInterval
	if timeout == 0 {
		timeout = 240 * time.Second
	}
	if poll == 0 {
		poll = time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if ok, _ := m.Health(ctx, ep.Addr, ep.Token); ok {
			return nil
		}
		running, err := m.run(ctx, "container", "inspect", "--format", "{{.State.Running}}", ContainerName)
		if err != nil || strings.TrimSpace(running) != "true" {
			return fmt.Errorf("распознаватель завершился при загрузке: %s", m.logs(ctx))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("модель не загрузилась за %s: %s", timeout, m.logs(ctx))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (m *Manager) logs(ctx context.Context) string {
	out, err := m.run(context.WithoutCancel(ctx), "logs", "--tail", "30", ContainerName)
	if err != nil {
		return "журнал контейнера недоступен"
	}
	return strings.TrimSpace(out)
}

// Stop removes Solveo's container if it exists; foreign containers are never touched.
func (m *Manager) Stop(ctx context.Context) error {
	if exists, ours := m.owned(ctx); exists && ours {
		if _, err := m.run(ctx, "rm", "-f", ContainerName); err != nil {
			return fmt.Errorf("не удалось остановить распознаватель: %w", err)
		}
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	return port, l.Close()
}

func tarContext(fsys fs.FS) ([]byte, error) {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, path := range paths {
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, err
		}
		if err := w.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(data))}); err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), w.Close()
}

