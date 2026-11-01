package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxAuthSize = 1024 * 1024
const encryptedHeader = "IBAUTH1\n"

// EncryptedSecrets uses a separately provisioned key file, for example a service
// manager's credential mount. It never falls back to an unencrypted credential.
// The service account and its host remain trusted; this is not a remote KMS.
type EncryptedSecrets struct {
	KeyFile   string
	Dir       string
	Namespace string
}

func privateDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("credential paths must be absolute")
	}
	if e := os.MkdirAll(path, 0700); e != nil {
		return errors.New("cannot create private credential directory")
	}
	i, e := os.Lstat(path)
	if e != nil || !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return errors.New("credential directory must be a real private directory (0700)")
	}
	return nil
}

func readPrivateFile(path string, limit int64) ([]byte, error) {
	i, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() > limit {
		return nil, errors.New("credential file unavailable, insecure or too large")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("credential file cannot be opened")
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(i, actual) {
		return nil, errors.New("credential file changed during open")
	}
	raw, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(raw)) > limit {
		return nil, errors.New("credential file cannot be read within limit")
	}
	return raw, nil
}

// Publish a fully written inode without ever replacing an existing version.
func writePrivateImmutable(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if e := privateDirectory(dir); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return errors.New("cannot create private credential temporary file")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return errors.New("credential write failed")
	}
	if e = f.Sync(); e != nil {
		return errors.New("credential sync failed")
	}
	if e = f.Close(); e != nil {
		return errors.New("credential close failed")
	}
	if e = os.Link(f.Name(), path); e != nil {
		return errors.New("credential publication failed; existing files are never replaced")
	}
	d, e := os.Open(dir)
	if e != nil {
		return errors.New("credential directory unavailable after publication")
	}
	defer d.Close()
	if e = d.Sync(); e != nil {
		return errors.New("credential directory sync failed; inspect saved version before retrying")
	}
	return nil
}

func initCredentialKey(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("key file path must be absolute")
	}
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		return errors.New("system randomness unavailable")
	}
	defer clear(key)
	return writePrivateImmutable(path, key)
}

func (s *EncryptedSecrets) aead() (cipher.AEAD, error) {
	if !filepath.IsAbs(s.KeyFile) {
		return nil, errors.New("key file path must be absolute")
	}
	key, e := readPrivateFile(s.KeyFile, 32)
	if e != nil {
		return nil, e
	}
	defer clear(key)
	if len(key) != 32 {
		return nil, errors.New("credential key must contain exactly 32 binary bytes")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, errors.New("cannot initialize credential cipher")
	}
	return cipher.NewGCM(block)
}

func (s *EncryptedSecrets) location(user, version string) (string, []byte, error) {
	if validID(s.Namespace) != nil || validID(user) != nil || validID(version) != nil {
		return "", nil, errors.New("invalid credential identity")
	}
	if e := privateDirectory(s.Dir); e != nil {
		return "", nil, e
	}
	relative, e := filepath.Rel(s.Dir, s.KeyFile)
	if e != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
		return "", nil, errors.New("credential key must be kept outside the encrypted bundle directory")
	}
	aad := []byte(encryptedHeader + s.Namespace + "\x00" + user + "\x00" + version)
	sum := sha256.Sum256(aad)
	return filepath.Join(s.Dir, hex.EncodeToString(sum[:])+".enc"), aad, nil
}

func (s *EncryptedSecrets) Put(user, version string, value []byte) error {
	if len(value) > maxAuthSize {
		return errors.New("credential bundle too large")
	}
	if _, e := validateAuth(value); e != nil {
		return e
	}
	aead, e := s.aead()
	if e != nil {
		return e
	}
	path, aad, e := s.location(user, version)
	if e != nil {
		return e
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return errors.New("system randomness unavailable")
	}
	data := append([]byte(encryptedHeader), nonce...)
	data = aead.Seal(data, nonce, value, aad)
	return writePrivateImmutable(path, data)
}

func (s *EncryptedSecrets) Get(user, version string) ([]byte, error) {
	aead, e := s.aead()
	if e != nil {
		return nil, e
	}
	path, aad, e := s.location(user, version)
	if e != nil {
		return nil, e
	}
	data, e := readPrivateFile(path, maxAuthSize+1024)
	if e != nil {
		return nil, e
	}
	start := len(encryptedHeader)
	if len(data) < start+aead.NonceSize()+aead.Overhead() || string(data[:start]) != encryptedHeader {
		return nil, errors.New("invalid encrypted credential format")
	}
	nonce := data[start : start+aead.NonceSize()]
	raw, e := aead.Open(nil, nonce, data[start+aead.NonceSize():], aad)
	if e != nil {
		return nil, errors.New("credential decryption failed; key or stored identity does not match")
	}
	return validateAuth(raw)
}
