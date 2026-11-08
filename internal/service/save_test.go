package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/silirim/vodmax/internal/library"
	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
)

// A device may copy the video files of the collections, no more and no
// less, each with a video type: no mime.TypeByExtension, so /etc/mime.types
// changes nothing.
func TestSaveTypesAreCollectionVideos(t *testing.T) {
	if len(saveTypes) != len(library.VideoExt) {
		t.Fatalf("%d types for %d collection extensions", len(saveTypes), len(library.VideoExt))
	}
	for ext := range library.VideoExt {
		if !strings.HasPrefix(saveTypes[ext], "video/") {
			t.Errorf("%s: %q", ext, saveTypes[ext])
		}
	}
}

// A recording being converted waits; an expired link, or one of a device
// now locked to another profile, is not found.
func TestSaveLinkConversionExpiryAndLock(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	s := New(st, box, nil)
	s.dl.dir = dir
	devID, _ := st.AddDevice(ctx, "Fire TV", []byte("hash"))
	p, _ := st.Profile(ctx, 1)
	other, _ := st.AddProfile(ctx, store.Profile{Name: "Marco", Kind: "standard"})
	file := filepath.Join(dir, "1.ts")
	os.WriteFile(file, make([]byte, 188), 0o644)
	start := time.Now().Add(time.Hour).Unix()
	title := "X" + strings.Repeat("è", 70) // 141 bytes: fileName cuts at 120, inside an "è"
	rec, _ := st.AddRecording(ctx, store.Recording{Channel: "Rai 1", Title: title, Start: start, Stop: start + 60})
	st.UpdateRecording(ctx, store.Recording{ID: rec, Status: "completed", File: file, Stop: start + 60})
	id := Identity{DeviceID: devID, Profile: &p}

	s.rec.converting = map[int64]bool{rec: true}
	if _, err := s.SaveLink(ctx, id, "recording", rec); !errors.Is(err, ErrConverting) {
		t.Fatalf("while converting: %v", err)
	}
	delete(s.rec.converting, rec)
	l, err := s.SaveLink(ctx, id, "recording", rec)
	if err != nil {
		t.Fatal(err)
	}
	if want := " " + time.Unix(start, 0).Format("2006-01-02 1504") + ".ts"; !strings.HasSuffix(l.Name, want) || !utf8.ValidString(l.Name) {
		t.Errorf("name %q: want valid UTF-8 ending in %q", l.Name, want)
	}
	token := strings.TrimPrefix(l.URL, "/stream/save/")
	f, _, err := s.OpenSave(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	tk, _, _ := parseSaveToken(token)
	d, _ := st.Device(ctx, devID)
	tk.expires = time.Now().Add(-time.Minute).Unix()
	if _, _, err := s.OpenSave(ctx, tk.String()+"."+s.saveSignature(tk, d.CreatedAt, p.PINEpoch)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	st.UpdateDevice(ctx, devID, "Fire TV", &other)
	if _, _, err := s.OpenSave(ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("device locked to another profile: %v", err)
	}
}
