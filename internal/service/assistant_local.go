package service

import (
	"archive/tar"
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The built-in assistant: VodMax downloads llama.cpp's server and a model
// when the admin presses "Attiva", starts it as a child process listening
// only on 127.0.0.1, and stops it after a while without requests. Nothing
// is in the package or the image until then. Where the official builds do
// not run (the Alpine Docker image), an Ollama service next to VodMax
// (VODMAX_ASSISTANT_OLLAMA) does the same job: VodMax asks it to download
// the same model files.

const (
	localIdle    = 10 * time.Minute
	localContext = "8192"
	localEngine  = "b12020" // llama.cpp release, with the digests below
)

// localEngines: the official Vulkan builds, which also carry the processor
// backends and fall back to them without a graphics card or Vulkan.
var localEngines = map[string]struct{ URL, SHA256 string }{
	"amd64": {"https://github.com/ggml-org/llama.cpp/releases/download/b12020/llama-b12020-bin-ubuntu-vulkan-x64.tar.gz", "8b7ec1fbef304ea4e024cf34f68ef1276556b38b666dc71b8af5de6e9d93b42c"},
	"arm64": {"https://github.com/ggml-org/llama.cpp/releases/download/b12020/llama-b12020-bin-ubuntu-vulkan-arm64.tar.gz", "e4db03071cffa07d3ff278cc39545db7ae729fa6ca907fc96af3f01a70272b30"},
}

// LocalModel is one of the models VodMax offers (Apache 2.0, from Hugging
// Face, checked by digest). RAM is what the model needs while it answers.
type LocalModel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Note   string `json:"note"`
	Size   int64  `json:"size"`
	RAM    int64  `json:"ram"`
	url    string
	sha256 string
	ollama string
}

const gb = 1 << 30

var localModels = []LocalModel{
	{ID: "light", Name: "Leggero", Note: "Qwen 2.5 1.5B: crea bene le regole, i consigli per la sera sono approssimativi", Size: 986048768, RAM: 2 * gb,
		url: "https://huggingface.co/bartowski/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf", sha256: "1adf0b11065d8ad2e8123ea110d1ec956dab4ab038eab665614adba04b6c3370",
		ollama: "hf.co/bartowski/Qwen2.5-1.5B-Instruct-GGUF:Q4_K_M"},
	{ID: "balanced", Name: "Bilanciato", Note: "Qwen 3 4B: il consigliato senza scheda video", Size: 2497281120, RAM: 4 * gb,
		url: "https://huggingface.co/unsloth/Qwen3-4B-Instruct-2507-GGUF/resolve/main/Qwen3-4B-Instruct-2507-Q4_K_M.gguf", sha256: "3605803b982cb64aead44f6c1b2ae36e3acdb41d8e46c8a94c6533bc4c67e597",
		ollama: "hf.co/unsloth/Qwen3-4B-Instruct-2507-GGUF:Q4_K_M"},
	{ID: "quality", Name: "Qualità", Note: "Qwen 2.5 14B: il migliore, lento senza scheda video", Size: 8988110976, RAM: 11 * gb,
		url: "https://huggingface.co/bartowski/Qwen2.5-14B-Instruct-GGUF/resolve/main/Qwen2.5-14B-Instruct-Q4_K_M.gguf", sha256: "e47ad95dad6ff848b431053b375adb5d39321290ea2c638682577dafca87c008",
		ollama: "hf.co/bartowski/Qwen2.5-14B-Instruct-GGUF:Q4_K_M"},
}

func localModel(id string) (LocalModel, bool) {
	for _, m := range localModels {
		if m.ID == id {
			return m, true
		}
	}
	return LocalModel{}, false
}

type localState struct {
	mu     sync.Mutex
	dir    string // <data>/assistant
	ollama string // the Ollama service, in Docker
	log    *slog.Logger
	job    *LocalJob
	cancel context.CancelFunc

	cmd     *exec.Cmd
	port    int
	running string // model id
	last    time.Time
	busy    int
	exited  chan struct{}
	died    string // why the last engine stopped by itself
}

// LocalJob is an installation in progress (or just failed).
type LocalJob struct {
	Model string `json:"model"`
	Phase string `json:"phase"` // engine, model, start, test, error
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

// SetAssistantDir sets where the engine and the models go; ollama is the
// address of an Ollama service to use instead (empty: llama.cpp here).
func (s *Service) SetAssistantDir(dir, ollama string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	l := &s.asst.local
	l.mu.Lock()
	l.dir, l.ollama, l.log = dir, strings.TrimRight(ollama, "/"), log
	l.mu.Unlock()
}

// LocalHardware is what the server has, to offer the models that fit.
type LocalHardware struct {
	RAM      int64    `json:"ram"`
	RAMFree  int64    `json:"ram_free"`
	Disk     int64    `json:"disk_free"`
	GPU      string   `json:"gpu,omitempty"`
	GPUNote  string   `json:"gpu_note,omitempty"`
	Backend  string   `json:"backend"` // llama.cpp, ollama
	NoReason string   `json:"unavailable,omitempty"`
	missing  []string // Debian packages of system libraries the engine needs
}

// LocalView is the state of the built-in assistant for the settings page.
type LocalView struct {
	Hardware  LocalHardware    `json:"hardware"`
	Models    []LocalModelView `json:"models"`
	Job       *LocalJob        `json:"job,omitempty"`
	Running   string           `json:"running,omitempty"`
	Installed bool             `json:"engine_installed"`
}

type LocalModelView struct {
	LocalModel
	Fits        bool `json:"fits"`
	Recommended bool `json:"recommended"`
	Installed   bool `json:"installed"`
}

func (s *Service) LocalAssistant(ctx context.Context) LocalView {
	l := &s.asst.local
	l.mu.Lock()
	dir, ollama, running := l.dir, l.ollama, l.running
	var job *LocalJob
	if l.job != nil {
		j := *l.job
		job = &j
	}
	l.mu.Unlock()
	hw := localHardware(dir, ollama)
	hw.NoReason, hw.GPU, hw.GPUNote = T(ctx, hw.NoReason), T(ctx, hw.GPU), T(ctx, hw.GPUNote)
	if hw.NoReason == "" && len(hw.missing) > 0 {
		hw.NoReason = T(ctx, "al motore mancano librerie di sistema: installale con  apt install %s  e ricarica la pagina", strings.Join(hw.missing, " "))
	}
	v := LocalView{Hardware: hw, Job: job, Running: running}
	installed := map[string]bool{}
	if ollama != "" {
		tags, ok := ollamaTags(ctx, ollama)
		if !ok && v.Hardware.NoReason == "" {
			v.Hardware.NoReason = T(ctx, "il servizio \"assistant\" non risponde: avvialo con  docker compose --profile assistant up -d")
		}
		for _, tag := range tags {
			installed[tag] = true
		}
	} else if dir != "" {
		_, err := os.Stat(filepath.Join(dir, "engine-"+localEngine, "llama-server"))
		v.Installed = err == nil
	}
	best := -1
	for i, m := range localModels {
		mv := LocalModelView{LocalModel: m}
		mv.Name, mv.Note = T(ctx, m.Name), T(ctx, m.Note)
		mv.Fits = v.Hardware.NoReason == "" && (hw.RAM == 0 || m.RAM <= hw.RAM-gb)
		if ollama != "" {
			mv.Installed = installed[m.ollama]
		} else if dir != "" {
			_, err := os.Stat(filepath.Join(dir, "models", m.ID+".gguf"))
			mv.Installed = err == nil
		}
		// Recommended: the largest that fits, but the 14B only with a GPU.
		if mv.Fits && m.ID != "light" && (m.ID != "quality" || hw.GPU != "") {
			best = i
		}
		v.Models = append(v.Models, mv)
	}
	if best >= 0 {
		v.Models[best].Recommended = true
	}
	return v
}

func localHardware(dir, ollama string) LocalHardware {
	hw := LocalHardware{Backend: "llama.cpp"}
	if ollama != "" {
		hw.Backend = "ollama"
	} else if dir == "" {
		hw.NoReason = "l'assistente integrato non è disponibile su questo dispositivo"
	} else if _, ok := localEngines[runtime.GOARCH]; !ok || runtime.GOOS != "linux" {
		hw.NoReason = "l'assistente integrato funziona su Linux (x86-64 o ARM64): qui usa un servizio online o un tuo server"
	} else if muslLibc() {
		hw.NoReason = "questo sistema usa musl (Alpine): avvia il servizio \"assistant\" del docker-compose, oppure usa un servizio online"
	} else {
		hw.missing = missingLibraries()
	}
	if ollama == "" { // Ollama runs elsewhere, with its own memory
		hw.RAM, hw.RAMFree = memInfo()
	}
	if dir != "" {
		probe := dir
		for {
			if _, err := os.Stat(probe); err == nil || filepath.Dir(probe) == probe {
				break
			}
			probe = filepath.Dir(probe)
		}
		if free, _, ok := diskSpace(probe); ok {
			hw.Disk = free
		}
	}
	if ollama == "" {
		if gpus, _ := filepath.Glob("/dev/dri/renderD*"); len(gpus) > 0 {
			if vulkanLoader() {
				hw.GPU = "scheda video (Vulkan)"
			} else {
				hw.GPUNote = "c'è una scheda video ma manca Vulkan: installa libvulkan1 e mesa-vulkan-drivers per usarla"
			}
		}
	}
	return hw
}

// memInfo reads the memory of the machine, or of the container when its
// limit is lower.
func memInfo() (total, free int64) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = kb << 10
		case "MemAvailable:":
			free = kb << 10
		}
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if lim, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err == nil && lim > 0 && lim < total {
			if cur, err := os.ReadFile("/sys/fs/cgroup/memory.current"); err == nil {
				if used, err := strconv.ParseInt(strings.TrimSpace(string(cur)), 10, 64); err == nil && lim-used < free {
					free = lim - used
				}
			}
			total = lim
		}
	}
	return total, free
}

// engineLibraries: what the official builds need from the system, with
// the Debian package that has it (libgomp1 usually comes with ffmpeg).
var engineLibraries = map[string]string{"libgomp.so.1": "libgomp1", "libstdc++.so.6": "libstdc++6", "libssl.so.3": "libssl3", "libcrypto.so.3": "libssl3"}

// missingLibraries asks the dynamic linker's cache which of them are not
// installed; nothing when it cannot tell.
func missingLibraries() []string {
	var out []byte
	for _, ldconfig := range []string{"/sbin/ldconfig", "/usr/sbin/ldconfig", "ldconfig"} {
		var err error
		if out, err = exec.Command(ldconfig, "-p").Output(); err == nil {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) > 1 && strings.Contains(line, "=>") {
			have[f[0]] = true
		}
	}
	var pkgs []string
	for lib, pkg := range engineLibraries {
		if !have[lib] && !slices.Contains(pkgs, pkg) {
			pkgs = append(pkgs, pkg)
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

func muslLibc() bool {
	m, _ := filepath.Glob("/lib/ld-musl-*")
	return len(m) > 0
}

func vulkanLoader() bool {
	for _, p := range []string{"/usr/lib/x86_64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/usr/lib64", "/usr/lib"} {
		if _, err := os.Stat(filepath.Join(p, "libvulkan.so.1")); err == nil {
			return true
		}
	}
	return false
}

// InstallLocalAssistant downloads what model id needs, in the background,
// then turns the assistant on with it and tries it.
func (s *Service) InstallLocalAssistant(ctx context.Context, id string) error {
	m, ok := localModel(id)
	if !ok {
		return invalid("modello sconosciuto")
	}
	v := s.LocalAssistant(ctx)
	if v.Hardware.NoReason != "" {
		return invalid("%s", v.Hardware.NoReason)
	}
	l := &s.asst.local
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dir == "" && l.ollama == "" {
		return invalid("l'assistente integrato non è disponibile su questo server")
	}
	if l.job != nil && l.job.Phase != "error" {
		return invalid("c'è già un'installazione in corso")
	}
	if l.ollama == "" && v.Hardware.Disk > 0 && v.Hardware.Disk < m.Size+200<<20 {
		return invalid("spazio insufficiente: servono %.1f GB liberi", float64(m.Size+200<<20)/gb)
	}
	jctx, cancel := context.WithCancel(context.Background())
	l.job, l.cancel = &LocalJob{Model: id, Phase: "engine"}, cancel
	go s.runInstall(jctx, m)
	return nil
}

// CancelLocalInstall stops a download; what was downloaded stays, to go on
// from there next time.
func (s *Service) CancelLocalInstall() {
	l := &s.asst.local
	l.mu.Lock()
	if l.cancel != nil {
		l.cancel()
	}
	l.job, l.cancel = nil, nil
	l.mu.Unlock()
}

func (s *Service) jobUpdate(f func(j *LocalJob)) {
	l := &s.asst.local
	l.mu.Lock()
	if l.job != nil {
		f(l.job)
	}
	l.mu.Unlock()
}

func (s *Service) runInstall(ctx context.Context, m LocalModel) {
	l := &s.asst.local
	err := s.install(ctx, m)
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		s.jobUpdate(func(j *LocalJob) { j.Phase = "test" })
		err = s.SetAssistant(ctx, AssistantInput{Enabled: true, Mode: "local", Model: m.ID})
		if err == nil {
			_, _, err = s.TestAssistant(ctx)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		l.log.Warn("assistente integrato: installazione non riuscita", "modello", m.ID, "errore", err)
		if l.job != nil {
			l.job.Phase, l.job.Error = "error", err.Error()
		}
		return
	}
	l.job, l.cancel = nil, nil
}

func (s *Service) install(ctx context.Context, m LocalModel) error {
	l := &s.asst.local
	l.mu.Lock()
	dir, ollama := l.dir, l.ollama
	l.mu.Unlock()
	if ollama != "" {
		s.jobUpdate(func(j *LocalJob) { j.Phase = "model" })
		return ollamaPull(ctx, ollama, m.ollama, func(done, total int64) {
			s.jobUpdate(func(j *LocalJob) { j.Done, j.Total = done, total })
		})
	}
	eng := localEngines[runtime.GOARCH]
	engDir := filepath.Join(dir, "engine-"+localEngine)
	if _, err := os.Stat(filepath.Join(engDir, "llama-server")); err != nil {
		archive := filepath.Join(dir, "engine-"+localEngine+".tar.gz")
		if err := s.fetchVerified(ctx, eng.URL, eng.SHA256, archive); err != nil {
			return err
		}
		if err := extractEngine(archive, engDir); err != nil {
			return fmt.Errorf("motore dell'assistente: %w", err)
		}
		os.Remove(archive)
		removeOldEngines(dir)
	}
	s.jobUpdate(func(j *LocalJob) { j.Phase, j.Done, j.Total = "model", 0, m.Size })
	path := filepath.Join(dir, "models", m.ID+".gguf")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return s.fetchVerified(ctx, m.url, m.sha256, path)
}

// fetchVerified downloads url to path, going on from a partial download, and
// keeps it only if its SHA-256 is want.
func (s *Service) fetchVerified(ctx context.Context, url, want, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	part := path + ".part"
	h := sha256.New()
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	have, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "VodMax/"+s.version())
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("download non riuscito: %w", redactURL(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		// The server starts over (or the part is already whole: check it).
		if resp.StatusCode == http.StatusOK && have > 0 {
			have = 0
			h.Reset()
			if err := f.Truncate(0); err != nil {
				return err
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("download non riuscito: risposta %d", resp.StatusCode)
	}
	total := have + resp.ContentLength
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		total = have
	} else {
		w := io.MultiWriter(f, h)
		buf := make([]byte, 1<<20)
		done := have
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, err := w.Write(buf[:n]); err != nil {
					return err
				}
				done += int64(n)
				s.jobUpdate(func(j *LocalJob) { j.Done, j.Total = done, total })
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return fmt.Errorf("download interrotto: %w", rerr)
			}
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		f.Close()
		os.Remove(part)
		return errors.New("il file scaricato non corrisponde all'impronta attesa: scartato, riprova")
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, path)
}

// extractEngine keeps from the release archive only the server and its
// libraries, flat in dir.
func extractEngine(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Base(hdr.Name)
		if name != "llama-server" && !strings.Contains(name, ".so") {
			continue
		}
		dst := filepath.Join(tmp, name)
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			if strings.Contains(hdr.Linkname, "/") {
				continue
			}
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				return err
			}
		case tar.TypeReg:
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, io.LimitReader(tr, 1<<30))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			found = found || name == "llama-server"
		}
	}
	if !found {
		return errors.New("llama-server non trovato nel pacchetto")
	}
	return os.Rename(tmp, dir)
}

func removeOldEngines(dir string) {
	old, _ := filepath.Glob(filepath.Join(dir, "engine-*"))
	for _, d := range old {
		if filepath.Base(d) != "engine-"+localEngine {
			os.RemoveAll(d)
		}
	}
}

// RemoveLocalAssistant stops the engine and deletes engine and models.
func (s *Service) RemoveLocalAssistant(ctx context.Context) error {
	s.CancelLocalInstall()
	s.stopLocal()
	l := &s.asst.local
	l.mu.Lock()
	dir, ollama := l.dir, l.ollama
	l.mu.Unlock()
	if ollama != "" {
		for _, m := range localModels {
			ollamaDelete(ctx, ollama, m.ollama)
		}
	} else if dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	a, _, err := s.AssistantSettings(ctx)
	if err == nil && a.Mode == "local" {
		a.Enabled, a.Model = false, ""
		err = s.st.SetSetting(ctx, assistantKey, a)
	}
	return err
}

// localEndpoint returns the address of the running engine for model id,
// starting it if needed; release must be called when the answer is in.
func (s *Service) localEndpoint(ctx context.Context, id string) (base, model string, release func(), err error) {
	m, ok := localModel(id)
	if !ok {
		return "", "", nil, ErrAssistantOff
	}
	l := &s.asst.local
	l.mu.Lock()
	if l.ollama != "" {
		l.mu.Unlock()
		return l.ollama + "/v1", m.ollama, func() {}, nil
	}
	// Another model: the engine changes once no answer runs on it. Until
	// then the old model answers, rather than an answer being cut.
	if l.running != id && l.cmd != nil && l.busy == 0 {
		cmd, exited := l.takeEngine()
		l.mu.Unlock()
		stopEngine(cmd, exited)
		l.mu.Lock()
	}
	if l.cmd == nil {
		if err := s.startLocal(m); err != nil {
			l.mu.Unlock()
			return "", "", nil, err
		}
	}
	l.busy++
	port, exited := l.port, l.exited
	l.mu.Unlock()
	release = func() {
		l.mu.Lock()
		l.busy--
		l.last = time.Now()
		l.mu.Unlock()
	}
	base = fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := s.waitHealthy(ctx, base, exited); err != nil {
		release()
		return "", "", nil, err
	}
	return base + "/v1", m.ID, release, nil
}

// startLocal runs llama-server; l.mu is held.
func (s *Service) startLocal(m LocalModel) error {
	l := &s.asst.local
	engDir := filepath.Join(l.dir, "engine-"+localEngine)
	model := filepath.Join(l.dir, "models", m.ID+".gguf")
	if _, err := os.Stat(model); err != nil {
		return errors.New("il modello dell'assistente non è installato: Impostazioni → Assistente")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cmd := exec.Command(filepath.Join(engDir, "llama-server"), "-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-c", localContext, "-ngl", "99", "--no-webui")
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+engDir)
	var stderr tailBuffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("avvio dell'assistente: %w", err)
	}
	exited := make(chan struct{})
	l.cmd, l.port, l.running, l.last, l.exited = cmd, port, m.ID, time.Now(), exited
	l.log.Info("assistente integrato avviato", "modello", m.ID)
	go func() {
		err := cmd.Wait()
		l.mu.Lock()
		if l.cmd == cmd {
			l.cmd, l.running = nil, ""
			if err != nil {
				l.died = stderr.String()
				l.log.Warn("assistente integrato fermato", "errore", err, "ultime righe", l.died)
			}
		}
		l.mu.Unlock()
		close(exited)
	}()
	go s.idleStop(cmd, exited)
	return nil
}

// idleStop stops the engine after localIdle without requests.
func (s *Service) idleStop(cmd *exec.Cmd, exited chan struct{}) {
	l := &s.asst.local
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-exited:
			return
		case <-t.C:
			l.mu.Lock()
			// Checked and taken in one go: a request arriving now finds no
			// engine and starts one, instead of losing this one mid-answer.
			idle := l.cmd == cmd && l.busy == 0 && time.Since(l.last) > localIdle
			if idle {
				l.takeEngine()
			}
			l.mu.Unlock()
			if idle {
				l.log.Info("assistente integrato fermato: nessuna richiesta da 10 minuti")
				stopEngine(cmd, exited)
				return
			}
		}
	}
}

// stopLocal stops the engine whatever it is doing: the admin changed the
// mode or removed the assistant, or VodMax is shutting down.
func (s *Service) stopLocal() {
	l := &s.asst.local
	l.mu.Lock()
	cmd, exited := l.takeEngine()
	l.mu.Unlock()
	stopEngine(cmd, exited)
}

// takeEngine detaches the running engine, for stopEngine; l.mu is held.
func (l *localState) takeEngine() (*exec.Cmd, chan struct{}) {
	cmd, exited := l.cmd, l.exited
	l.cmd, l.running = nil, ""
	return cmd, exited
}

func stopEngine(cmd *exec.Cmd, exited chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	cmd.Process.Signal(os.Interrupt)
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		<-exited
	}
}

// StopAssistant stops the built-in engine (VodMax shutting down).
func (s *Service) StopAssistant() { s.stopLocal() }

func (s *Service) waitHealthy(ctx context.Context, base string, exited chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-exited:
			s.asst.local.mu.Lock()
			died := s.asst.local.died
			s.asst.local.mu.Unlock()
			// "error while loading shared libraries: libgomp.so.1: cannot open..."
			if _, after, ok := strings.Cut(died, "error while loading shared libraries: "); ok {
				lib, _, _ := strings.Cut(after, ":")
				return fmt.Errorf("al motore manca la libreria di sistema %s: installala (Debian: apt install %s)", lib, cmp.Or(engineLibraries[lib], lib))
			}
			return errors.New("l'assistente integrato non è partito: controlla RAM e registro del server")
		case <-ctx.Done():
			return errors.New("l'assistente integrato non è pronto: il modello si sta ancora caricando, riprova tra poco")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// tailBuffer keeps the last lines a process wrote, for the log.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, " | ")
}

// ---- Ollama next to VodMax (Docker) ----

func ollamaTags(ctx context.Context, base string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tags", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	var tags []string
	for _, m := range out.Models {
		tags = append(tags, m.Name)
	}
	return tags, true
}

func ollamaPull(ctx context.Context, base, model string, progress func(done, total int64)) error {
	body, _ := json.Marshal(map[string]any{"model": model, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("servizio assistant non raggiungibile: %w", redactURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("il servizio assistant ha risposto %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			return fmt.Errorf("download del modello: %s", ev.Error)
		}
		if ev.Total > 0 {
			progress(ev.Completed, ev.Total)
		}
		if ev.Status == "success" {
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("download interrotto: %w", err)
	}
	return errors.New("download del modello interrotto")
}

func ollamaDelete(ctx context.Context, base, model string) {
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/api/delete", bytes.NewReader(body))
	if err != nil {
		return
	}
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

func (s *Service) localInstalled(ctx context.Context, id string) bool {
	for _, m := range s.LocalAssistant(ctx).Models {
		if m.ID == id {
			return m.Installed
		}
	}
	return false
}
