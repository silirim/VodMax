package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
)

func localTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	s := New(st, box, nil)
	s.SetAssistantDir(filepath.Join(dir, "assistant"), "", nil)
	t.Cleanup(s.StopAssistant)
	return s, filepath.Join(dir, "assistant")
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestFetchVerifiedResumesAndChecks(t *testing.T) {
	s, dir := localTestService(t)
	body := bytes.Repeat([]byte("vodmax "), 50000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "m.gguf", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	path := filepath.Join(dir, "models", "x.gguf")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path+".part", body[:1000], 0o644) // an interrupted download
	if err := s.fetchVerified(context.Background(), srv.URL, digest(body), path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, body) {
		t.Fatalf("file of %d bytes, want %d", len(got), len(body))
	}
	if err := s.fetchVerified(context.Background(), srv.URL, strings.Repeat("0", 64), path+"2"); err == nil {
		t.Fatal("a file with another digest was kept")
	}
	if _, err := os.Stat(path + "2"); err == nil {
		t.Fatal("the wrong file is on disk")
	}
}

func engineArchive(t *testing.T, files map[string]string, links map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		tw.WriteHeader(&tar.Header{Name: "llama-" + localEngine + "/" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	for name, target := range links {
		tw.WriteHeader(&tar.Header{Name: "llama-" + localEngine + "/" + name, Linkname: target, Typeflag: tar.TypeSymlink})
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractEngineKeepsServerAndLibraries(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "e.tar.gz")
	os.WriteFile(archive, engineArchive(t,
		map[string]string{"llama-server": "srv", "libllama.so.0.8.0": "lib", "llama-bench": "no"},
		map[string]string{"libllama.so.0": "libllama.so.0.8.0", "libevil.so": "../../etc/passwd"}), 0o644)
	out := filepath.Join(dir, "engine")
	if err := extractEngine(archive, out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"llama-server", "libllama.so.0.8.0", "libllama.so.0"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	for _, name := range []string{"llama-bench", "libevil.so"} {
		if _, err := os.Lstat(filepath.Join(out, name)); err == nil {
			t.Errorf("%s extracted", name)
		}
	}
}

// TestHelperLlamaServer is the fake llama-server the tests start.
func TestHelperLlamaServer(t *testing.T) {
	if os.Getenv("VODMAX_FAKE_LLAMA") == "" {
		t.Skip("helper process")
	}
	args := strings.Join(os.Args, " ")
	port := args[strings.Index(args, "--port ")+7:]
	port = strings.Fields(port)[0]
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) })
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Sono il modello integrato."}}]}`)
	})
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	go http.ListenAndServe("127.0.0.1:"+port, mux)
	<-stop
	os.Exit(0)
}

func TestInstallAndRunLocalAssistant(t *testing.T) {
	if runtime.GOOS != "linux" || muslLibc() {
		t.Skip("the built-in assistant runs on Linux with glibc")
	}
	if _, ok := localEngines[runtime.GOARCH]; !ok {
		t.Skip("no engine for this architecture")
	}
	s, dir := localTestService(t)
	ctx := context.Background()
	exe, _ := os.Executable()
	script := "#!/bin/sh\nVODMAX_FAKE_LLAMA=1 exec " + exe + " -test.run=TestHelperLlamaServer -- \"$@\"\n"
	engine := engineArchive(t, map[string]string{"llama-server": script, "libggml-base.so.0": "lib"}, nil)
	model := []byte("GGUF fake model")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tar.gz") {
			w.Write(engine)
			return
		}
		w.Write(model)
	}))
	defer srv.Close()
	oldEngines, oldModels, oldLibs := localEngines, localModels, engineLibraries
	t.Cleanup(func() { localEngines, localModels, engineLibraries = oldEngines, oldModels, oldLibs })
	engineLibraries = map[string]string{}
	localEngines = map[string]struct{ URL, SHA256 string }{runtime.GOARCH: {srv.URL + "/engine.tar.gz", digest(engine)}}
	localModels = []LocalModel{{ID: "balanced", Name: "Bilanciato", Size: int64(len(model)), url: srv.URL + "/m.gguf", sha256: digest(model)},
		{ID: "fast", Name: "Veloce", Size: int64(len(model)), url: srv.URL + "/m.gguf", sha256: digest(model)}}

	if err := s.SetAssistant(ctx, AssistantInput{Enabled: true, Mode: "local", Model: "balanced"}); err == nil {
		t.Fatal("turned on before the model was installed")
	}
	if err := s.InstallLocalAssistant(ctx, "balanced"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		v := s.LocalAssistant(ctx)
		if v.Job == nil {
			break
		}
		if v.Job.Phase == "error" {
			t.Fatalf("installation: %s", v.Job.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %s", v.Job.Phase)
		}
		time.Sleep(50 * time.Millisecond)
	}
	a, _, _ := s.AssistantSettings(ctx)
	if !a.Enabled || a.Mode != "local" || a.Model != "balanced" {
		t.Fatalf("settings after the installation: %+v", a)
	}
	answer, _, err := s.TestAssistant(ctx)
	if err != nil || answer != "Sono il modello integrato." {
		t.Fatalf("answer %q, %v", answer, err)
	}
	if v := s.LocalAssistant(ctx); v.Running != "balanced" || !v.Models[0].Installed || !v.Installed {
		t.Fatalf("view: %+v", v)
	}
	// Another model asked for while an answer runs on the engine: that
	// answer keeps it; the model changes once nothing runs.
	os.WriteFile(filepath.Join(dir, "models", "fast.gguf"), model, 0o600)
	running := func() *exec.Cmd {
		s.asst.local.mu.Lock()
		defer s.asst.local.mu.Unlock()
		return s.asst.local.cmd
	}
	_, _, answering, err := s.localEndpoint(ctx, "balanced")
	if err != nil {
		t.Fatal(err)
	}
	first := running()
	_, _, other, err := s.localEndpoint(ctx, "fast")
	if err != nil || running() != first {
		t.Fatalf("engine stopped under a running answer: %v", err)
	}
	other()
	answering()
	_, _, release, err := s.localEndpoint(ctx, "fast")
	if err != nil || running() == first || s.LocalAssistant(ctx).Running != "fast" {
		t.Fatalf("model not changed when idle: %v", err)
	}
	release()
	if err := s.RemoveLocalAssistant(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("files left after Rimuovi")
	}
	if a, _, _ := s.AssistantSettings(ctx); a.Enabled {
		t.Fatal("still on after Rimuovi")
	}
}

func TestOllamaNextToVodMax(t *testing.T) {
	var pulled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pull":
			pulled.Store(true)
			fmt.Fprintln(w, `{"status":"pulling","total":100,"completed":40}`)
			fmt.Fprintln(w, `{"status":"success"}`)
		case "/api/tags":
			if pulled.Load() {
				fmt.Fprintf(w, `{"models":[{"name":%q}]}`, localModels[1].ollama)
			} else {
				fmt.Fprint(w, `{"models":[]}`)
			}
		case "/v1/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Ollama."}}]}`)
		}
	}))
	defer srv.Close()
	s, _ := localTestService(t)
	s.SetAssistantDir("", srv.URL, nil)
	ctx := context.Background()
	if v := s.LocalAssistant(ctx); v.Hardware.Backend != "ollama" || v.Hardware.NoReason != "" {
		t.Fatalf("hardware: %+v", v.Hardware)
	}
	if err := s.InstallLocalAssistant(ctx, "balanced"); err != nil {
		t.Fatal(err)
	}
	for i := 0; s.LocalAssistant(ctx).Job != nil; i++ {
		if j := s.LocalAssistant(ctx).Job; j != nil && j.Phase == "error" || i > 200 {
			t.Fatalf("job: %+v", j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if answer, _, err := s.TestAssistant(ctx); err != nil || answer != "Ollama." {
		t.Fatalf("answer %q, %v", answer, err)
	}
}

func TestLocalEngineMissingLibrary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell script engine")
	}
	s, dir := localTestService(t)
	ctx := context.Background()
	engDir := filepath.Join(dir, "engine-"+localEngine)
	os.MkdirAll(engDir, 0o755)
	os.MkdirAll(filepath.Join(dir, "models"), 0o755)
	os.WriteFile(filepath.Join(engDir, "llama-server"), []byte("#!/bin/sh\n"+
		"echo \"$0: error while loading shared libraries: libgomp.so.1: cannot open shared object file: No such file or directory\" >&2\nexit 127\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "models", "balanced.gguf"), []byte("GGUF"), 0o644)
	_, _, _, err := s.localEndpoint(ctx, "balanced")
	if err == nil || !strings.Contains(err.Error(), "apt install libgomp1") {
		t.Fatalf("error %v, want the package to install", err)
	}
	old := engineLibraries
	t.Cleanup(func() { engineLibraries = old })
	engineLibraries = map[string]string{"libvodmax-missing.so.9": "libvodmax9"}
	if missing := missingLibraries(); len(missing) > 0 {
		if err := s.InstallLocalAssistant(ctx, "balanced"); err == nil || !strings.Contains(err.Error(), "apt install libvodmax9") {
			t.Fatalf("install with a missing library: %v", err)
		}
	}
}
