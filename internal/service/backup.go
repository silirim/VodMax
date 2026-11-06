package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/silirim/vodmax/internal/store"
)

// Backup files: "VODMAXB1", a mode byte, a 16-byte salt, a 12-byte nonce,
// then gzip(JSON) sealed with AES-256-GCM (the header is authenticated).
// Mode 'P': key from the user's password (PBKDF2-SHA256), the file can be
// restored on any installation. Mode 'S': key derived from this server's
// secret.key, for the automatic daily copies.
const (
	backupMagic      = "VODMAXB1"
	backupIterations = 600_000
	backupMaxSize    = 64 << 20
	backupKeep       = 14
	backupEvery      = 24 * time.Hour
	minBackupPass    = 8
)

var (
	ErrBackupPassword = errors.New("password del backup errata, oppure file danneggiato")
	ErrBusyRecording  = errors.New("una registrazione è in corso: riprova quando è finita")
	autoBackupName    = regexp.MustCompile(`^vodmax-\d{8}-\d{6}\.vodmaxbackup$`)
)

// SetBackupDir sets where the automatic copies go (created on demand).
func (s *Service) SetBackupDir(dir string) {
	s.mu.Lock()
	s.maint.backupDir = dir
	s.mu.Unlock()
}

func (s *Service) exportData(ctx context.Context) ([]byte, error) {
	b, err := s.st.ExportBackup(ctx,
		func(id int64, sealed []byte) ([]byte, error) { return s.box.Open(sealed, vpnOwner(id)) },
		func(id int64, sealed []byte) ([]byte, error) { return s.box.Open(sealed, subOwner(id)) })
	if err != nil {
		return nil, err
	}
	// Sealed settings (the TMDB key) travel in clear inside the encrypted
	// backup, so another installation can seal them with its own key.
	b.Secrets = map[string]string{}
	for k := range b.Settings {
		if strings.HasPrefix(k, secretPrefix) {
			if v, err := s.secretSetting(ctx, k); err == nil && v != "" {
				b.Secrets[k] = v
			}
			delete(b.Settings, k)
		}
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExportBackup returns the user's data encrypted with password.
func (s *Service) ExportBackup(ctx context.Context, password string) ([]byte, error) {
	if len(password) < minBackupPass {
		return nil, invalid("la password del backup deve avere almeno %d caratteri", minBackupPass)
	}
	plain, err := s.exportData(ctx)
	if err != nil {
		return nil, err
	}
	return sealBackup('P', plain, func(salt []byte) ([]byte, error) { return passwordKey(password, salt) })
}

func passwordKey(password string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, backupIterations, 32)
}

func (s *Service) serverKey(salt []byte) ([]byte, error) {
	k := sha256.Sum256(append(s.box.Derive("backup"), salt...))
	return k[:], nil
}

func sealBackup(mode byte, plain []byte, key func(salt []byte) ([]byte, error)) ([]byte, error) {
	header := make([]byte, len(backupMagic)+1+16)
	copy(header, backupMagic)
	header[len(backupMagic)] = mode
	salt := header[len(backupMagic)+1:]
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	k, err := key(salt)
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(k)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(append(header, nonce...), aead.Seal(nil, nonce, plain, header)...)
	return out, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Service) openBackup(data []byte, password string) (*store.Backup, error) {
	hl := len(backupMagic) + 1 + 16
	if len(data) < hl+12+16 || string(data[:len(backupMagic)]) != backupMagic {
		return nil, invalid("il file non è un backup di VodMax")
	}
	header, salt := data[:hl], data[len(backupMagic)+1:hl]
	var key []byte
	var err error
	switch data[len(backupMagic)] {
	case 'P':
		if password == "" {
			return nil, invalid("serve la password del backup")
		}
		key, err = passwordKey(password, salt)
	case 'S':
		key, err = s.serverKey(salt)
	default:
		return nil, invalid("il file non è un backup di VodMax")
	}
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := data[hl : hl+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, data[hl+aead.NonceSize():], header)
	if err != nil {
		if data[len(backupMagic)] == 'S' {
			return nil, invalid("copia automatica di un'altra installazione o danneggiata")
		}
		return nil, ErrInvalid{Msg: ErrBackupPassword.Error(), format: ErrBackupPassword.Error()}
	}
	zr, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, err
	}
	var b store.Backup
	if err := json.NewDecoder(io.LimitReader(zr, 512<<20)).Decode(&b); err != nil {
		return nil, fmt.Errorf("backup illeggibile: %w", err)
	}
	return &b, nil
}

// RestoreBackup replaces the user's data with the backup. Tunnels are
// restarted with the restored VPNs and every subscription is synchronised
// again in the background; watch progress and hidden titles are attached
// as their catalogs come back.
func (s *Service) RestoreBackup(ctx context.Context, data []byte, password string) error {
	b, err := s.openBackup(data, password)
	if err != nil {
		return err
	}
	s.rec.mu.Lock()
	busy := len(s.rec.running) > 0 || len(s.rec.converting) > 0
	s.rec.mu.Unlock()
	if busy {
		return ErrBusyRecording
	}
	// A safety copy of what is being replaced.
	if _, err := s.AutoBackup(ctx); err != nil && !errors.Is(err, errNoBackupDir) {
		return fmt.Errorf("copia di sicurezza prima del ripristino non riuscita: %w", err)
	}
	s.Close() // tunnels of the old VPNs
	s.mu.Lock()
	clear(s.vpnOff)
	clear(s.maint.vpn)
	clear(s.maint.guideErr)
	clear(s.maint.guideRetry)
	s.mu.Unlock()
	for k := range b.Settings {
		if strings.HasPrefix(k, secretPrefix) {
			delete(b.Settings, k) // sealed by another installation
		}
	}
	err = s.st.ImportBackup(ctx, b,
		func(id int64, plain []byte) []byte { return s.box.Seal(plain, vpnOwner(id)) },
		func(id int64, plain []byte) []byte { return s.box.Seal(plain, subOwner(id)) },
		s.recordingFile)
	if err != nil {
		return err
	}
	for k, v := range b.Secrets {
		if strings.HasPrefix(k, secretPrefix) {
			if err := s.setSecretSetting(ctx, k, v); err != nil {
				return err
			}
		}
	}
	go func() {
		ctx := context.Background()
		subs, err := s.st.Subscriptions(ctx)
		if err != nil {
			return
		}
		for _, sub := range subs {
			s.Sync(ctx, sub.ID, true)
		}
	}()
	return nil
}

// recordingFile is the path of a file of the recordings folder, by name;
// "" if it is not there. A name from a backup cannot point elsewhere.
func (s *Service) recordingFile(name string) string {
	s.rec.mu.Lock()
	dir := s.rec.dir
	s.rec.mu.Unlock()
	name = filepath.Base(name)
	if dir == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	p := filepath.Join(dir, name)
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return p
}

var errNoBackupDir = errors.New("cartella dei backup non impostata")

// BackupFile is an automatic copy.
type BackupFile struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
}

// AutoBackup writes a copy sealed with the server key and keeps the most
// recent backupKeep.
func (s *Service) AutoBackup(ctx context.Context) (BackupFile, error) {
	s.mu.Lock()
	dir := s.maint.backupDir
	s.mu.Unlock()
	if dir == "" {
		return BackupFile{}, errNoBackupDir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupFile{}, err
	}
	plain, err := s.exportData(ctx)
	if err != nil {
		return BackupFile{}, err
	}
	data, err := sealBackup('S', plain, s.serverKey)
	if err != nil {
		return BackupFile{}, err
	}
	now := time.Now()
	name := now.Format("vodmax-20060102-150405") + ".vodmaxbackup"
	tmp := filepath.Join(dir, "."+name)
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return BackupFile{}, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return BackupFile{}, err
	}
	list, err := s.Backups()
	if err == nil && len(list) > backupKeep {
		for _, old := range list[backupKeep:] {
			os.Remove(filepath.Join(dir, old.Name))
		}
	}
	return BackupFile{Name: name, Size: int64(len(data)), Created: now}, nil
}

// Backups lists the automatic copies, newest first.
func (s *Service) Backups() ([]BackupFile, error) {
	s.mu.Lock()
	dir := s.maint.backupDir
	s.mu.Unlock()
	out := []BackupFile{}
	if dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !autoBackupName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupFile{Name: e.Name(), Size: info.Size(), Created: info.ModTime()})
	}
	slices.SortFunc(out, func(a, b BackupFile) int { return strings.Compare(b.Name, a.Name) })
	return out, nil
}

// AutoBackupData returns an automatic copy (to download or restore).
func (s *Service) AutoBackupData(name string) ([]byte, error) {
	if !autoBackupName.MatchString(name) {
		return nil, store.ErrNotFound
	}
	s.mu.Lock()
	dir := s.maint.backupDir
	s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, store.ErrNotFound
	}
	return data, err
}

// autoBackupDue: no copy in the last backupEvery.
func (s *Service) autoBackupDue() bool {
	list, err := s.Backups()
	if err != nil {
		return false
	}
	return len(list) == 0 || time.Since(list[0].Created) >= backupEvery
}
