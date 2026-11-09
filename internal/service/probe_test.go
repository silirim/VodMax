package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseProbeTracks(t *testing.T) {
	out := []byte(`{"format":{"duration":"6012.4"},"streams":[
		{"codec_type":"video","codec_name":"h264"},
		{"codec_type":"audio","codec_name":"ac3","channels":6,"tags":{"language":"eng","title":"English 5.1"},"disposition":{"default":1}},
		{"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"ita"}},
		{"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"ita"},"disposition":{"forced":1}},
		{"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle","tags":{"language":"eng"}},
		{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}]}`)
	p, err := parseProbe(out)
	if err != nil {
		t.Fatal(err)
	}
	if p.Duration != 6012.4 || p.VideoCodec != "h264" || len(p.Audio) != 2 || len(p.Subtitles) != 2 {
		t.Fatalf("%+v", p)
	}
	if a := p.Audio[1]; a.Index != 1 || a.Language != "ita" || a.Codec != "aac" {
		t.Fatalf("audio: %+v", a)
	}
	if p.preferredAudio() != 1 {
		t.Fatalf("preferred audio = %d, want the Italian track", p.preferredAudio())
	}
	if !p.Subtitles[0].Supported || !p.Subtitles[0].Forced || p.Subtitles[1].Supported {
		t.Fatalf("subtitles: %+v", p.Subtitles)
	}
	p.Audio[1].Language = "fre"
	if p.preferredAudio() != 0 {
		t.Fatal("without Italian the file's default track is preferred")
	}
}

// A film taken over by another device during the analysis hears that the
// connection is busy ("take control?"), not that the file is broken.
func TestProbeTakenOver(t *testing.T) {
	s, sub := oneConnectionService(t)
	dir := t.TempDir()
	ffprobe, started := filepath.Join(dir, "ffprobe"), filepath.Join(dir, "started")
	if err := os.WriteFile(ffprobe, []byte("#!/bin/sh\ntouch "+started+"\nexec sleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.SetFFmpeg("", ffprobe)
	sess := &vodSession{id: "sesscccc", viewer: "film-viewer-1", subID: sub, mode: "remux", internal: "probecccc", expires: time.Now().Add(time.Hour)}
	done := make(chan error, 1)
	go func() {
		_, err := s.probe(context.Background(), sess, false)
		done <- err
	}()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ffprobe never started")
		}
	}
	_, release, err := s.acquire(context.Background(), sub, "tv-viewer-02", true, false)
	if err != nil {
		t.Fatalf("take control refused: %v", err)
	}
	defer release()
	if err := <-done; !errors.As(err, new(ConnectionBusy)) {
		t.Fatalf("analysis ended with %v, want busy", err)
	}
}
