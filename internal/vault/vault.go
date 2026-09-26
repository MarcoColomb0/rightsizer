package vault

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/MarcoColomb0/rightsizer/internal/atomicfile"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// Vault holds vCenter credentials encrypted with a key derived from the
// administrator password. Nothing on disk is usable without that password:
// login verification and encryption use independent salts.
type Vault struct {
	dir string
	mu  sync.Mutex
	key []byte
	sec map[string]Secret
}

type Secret struct {
	User     string
	Password string
}

type kdf struct {
	Salt    []byte
	Time    uint32
	Memory  uint32
	Threads uint8
}

type file struct {
	Version int
	Auth    kdf
	Hash    []byte
	Enc     kdf
	Nonce   []byte
	Data    []byte
}

const (
	MinPassword = 12
	aad         = "rightsizer-vault-v1"
)

var (
	ErrLocked        = errors.New("vault is locked")
	ErrWrongPassword = errors.New("wrong password")
	ErrNotConfigured = errors.New("administrator password not set")
	defaultKDF       = kdf{Time: 3, Memory: 64 * 1024, Threads: 2}
	slots            = make(chan struct{}, 2)
)

func Open(dir string) *Vault { return &Vault{dir: dir} }

func (v *Vault) path() string { return filepath.Join(v.dir, "vault.json") }

func (v *Vault) Configured() bool {
	_, err := os.Stat(v.path())
	return err == nil
}

func (v *Vault) Locked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.key == nil
}

func CheckPassword(p string) error {
	if utf8.RuneCountInString(p) < MinPassword {
		return fmt.Errorf("password must have at least %d characters", MinPassword)
	}
	if len(p) > 1024 {
		return errors.New("password too long")
	}
	return nil
}

// Reset sets a new administrator password and discards every stored secret.
func (v *Vault) Reset(password string) error {
	if err := CheckPassword(password); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.write(password, map[string]Secret{})
}

// Verify checks the administrator password without unlocking.
func (v *Vault) Verify(password string) bool {
	f, err := v.read()
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(derive(password, f.Auth), f.Hash) == 1
}

func (v *Vault) Unlock(password string) error {
	f, err := v.read()
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(derive(password, f.Auth), f.Hash) != 1 {
		return ErrWrongPassword
	}
	key := derive(password, f.Enc)
	sec, err := open(key, f)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.key, v.sec = key, sec
	v.mu.Unlock()
	return nil
}

func (v *Vault) ChangePassword(old, next string) error {
	if err := CheckPassword(next); err != nil {
		return err
	}
	if err := v.Unlock(old); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.write(next, v.sec)
}

func (v *Vault) Get(id string) (Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return Secret{}, ErrLocked
	}
	s, ok := v.sec[id]
	if !ok {
		return Secret{}, fmt.Errorf("no stored credentials for %s", id)
	}
	return s, nil
}

func (v *Vault) Put(id string, s Secret) error {
	return v.update(func(m map[string]Secret) { m[id] = s })
}

func (v *Vault) Delete(id string) error {
	return v.update(func(m map[string]Secret) { delete(m, id) })
}

func (v *Vault) update(fn func(map[string]Secret)) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return ErrLocked
	}
	f, err := v.read()
	if err != nil {
		return err
	}
	next := make(map[string]Secret, len(v.sec)+1)
	maps.Copy(next, v.sec)
	fn(next)
	if err := v.seal(v.key, f, next); err != nil {
		return err
	}
	v.sec = next
	return nil
}

func (v *Vault) write(password string, sec map[string]Secret) error {
	f := &file{Version: 1, Auth: newKDF(), Enc: newKDF()}
	f.Hash = derive(password, f.Auth)
	key := derive(password, f.Enc)
	if err := v.seal(key, f, sec); err != nil {
		return err
	}
	v.key, v.sec = key, sec
	return nil
}

func (v *Vault) seal(key []byte, f *file, sec map[string]Secret) error {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(sec) // #nosec G117 -- sealed with XChaCha20-Poly1305 below, never stored in the clear
	if err != nil {
		return err
	}
	f.Nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(f.Nonce); err != nil {
		return err
	}
	f.Data = aead.Seal(nil, f.Nonce, plain, []byte(aad))
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(v.path(), b, 0o600)
}

func (v *Vault) read() (*file, error) {
	b, err := os.ReadFile(v.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("vault unreadable: %w", err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("unsupported vault version %d", f.Version)
	}
	return &f, nil
}

func open(key []byte, f *file) (map[string]Secret, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, f.Nonce, f.Data, []byte(aad))
	if err != nil {
		return nil, errors.New("vault cannot be decrypted")
	}
	sec := map[string]Secret{}
	return sec, json.Unmarshal(plain, &sec)
}

func newKDF() kdf {
	k := defaultKDF
	k.Salt = make([]byte, 16)
	if _, err := rand.Read(k.Salt); err != nil {
		panic(err)
	}
	return k
}

// derive runs Argon2id with a global concurrency cap so that parallel login
// attempts cannot exhaust memory.
func derive(password string, k kdf) []byte {
	slots <- struct{}{}
	defer func() { <-slots }()
	return argon2.IDKey([]byte(password), k.Salt, k.Time, k.Memory, k.Threads, 32)
}
