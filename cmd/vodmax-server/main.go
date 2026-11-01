// Command vodmax-server runs the VodMax server.
//
//	vodmax-server -data /var/lib/vodmax -listen 127.0.0.1:8095
//
// On first start it creates in the data directory the database, the key
// that encrypts credentials (secret.key) and the admin token (admin.token),
// all readable only by the owner.
//
// With -device it runs inside the Android app, for that device alone:
// no ffmpeg there, so no recordings and no pause of live TV (the native
// player reads films and episodes as they are).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/silirim/vodmax/internal/api"
	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/service"
	"github.com/silirim/vodmax/internal/store"
)

// version is set at build time (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	data := flag.String("data", "./vodmax-data", "cartella dei dati")
	listen := flag.String("listen", "127.0.0.1:8095", "indirizzo HTTP")
	recordings := flag.String("recordings", "", "cartella delle registrazioni (predefinita: <data>/recordings)")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "eseguibile ffmpeg (per film in MKV/AVI nel browser)")
	ffprobe := flag.String("ffprobe", "ffprobe", "eseguibile ffprobe")
	downloads := flag.String("downloads", "", "cartella dei download di film e serie (predefinita: <data>/downloads)")
	device := flag.Bool("device", false, "dentro l'app Android, per quel dispositivo: senza registrazioni né pausa della diretta")
	showVersion := flag.Bool("version", false, "mostra la versione ed esce")
	flag.Parse()
	if *showVersion {
		fmt.Println("vodmax-server", version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *downloads == "" {
		*downloads = filepath.Join(*data, "downloads")
	}
	if *recordings == "" {
		*recordings = filepath.Join(*data, "recordings")
	}
	if err := run(*data, *listen, *recordings, *downloads, *ffmpeg, *ffprobe, *device, log); err != nil {
		log.Error("avvio fallito", "err", err)
		os.Exit(1)
	}
}

func run(data, listen, recordings, downloads, ffmpeg, ffprobe string, device bool, log *slog.Logger) error {
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	if host, _, err := net.SplitHostPort(listen); err == nil {
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			log.Warn("API raggiungibile dalla rete: l'accesso è protetto solo dal token di amministrazione", "listen", listen)
		}
	}
	box, err := secret.LoadOrCreate(filepath.Join(data, "secret.key"))
	if err != nil {
		return err
	}
	token, err := adminToken(filepath.Join(data, "admin.token"))
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(data, "vodmax.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	svc := service.New(st, box, nil)
	defer svc.Close()
	svc.SetFFmpeg(ffmpeg, ffprobe)
	svc.SetVersion(version)

	srv := &http.Server{
		Addr:              listen,
		Handler:           api.Handler(svc, token, log),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !device {
		if err := svc.StartRecorder(ctx, recordings, log); err != nil {
			return err
		}
		if err := svc.SetTimeshiftDir(filepath.Join(data, "timeshift"), log); err != nil {
			return err
		}
		if err := svc.StartDownloads(ctx, downloads, log); err != nil {
			return err
		}
	}
	svc.SetBackupDir(filepath.Join(data, "backups"))
	if !device {
		svc.SetAssistantDir(filepath.Join(data, "assistant"), os.Getenv("VODMAX_ASSISTANT_OLLAMA"), log)
	}
	defer svc.StopAssistant()
	if err := svc.SetCacheDir(filepath.Join(data, "cache")); err != nil {
		return err
	}
	svc.StartMaintenance(ctx, log)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Info("VodMax server avviato", "versione", version, "listen", listen, "data", data, "dispositivo", device)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Recordings in progress note where they got to, to carry on at the
	// next start.
	svc.WaitRecorder(15 * time.Second)
	return nil
}

// adminToken reads or creates the admin token file (mode 0600).
func adminToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		token := hex.EncodeToString(b)
		if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
			return "", err
		}
		return token, nil
	}
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 32 {
		return "", fmt.Errorf("%s: token troppo corto", path)
	}
	return token, nil
}
