// Package credentials keeps model secrets out of ordinary configuration and
// diagnostic output. It never imports another application's credentials.
package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
)

const MaxSecretBytes = 64 << 10

var (
	ErrUnavailable   = errors.New("系统安全存储不可用，未保存明文凭据")
	ErrLocked        = errors.New("系统安全存储需要解锁或授权，请在系统设置中处理后重试")
	ErrNotFound      = errors.New("凭据不存在，请重新连接模型")
	ErrExists        = errors.New("凭据引用已存在，未覆盖原凭据")
	ErrInvalid       = errors.New("凭据内容或引用无效")
	ErrSerialization = errors.New("凭据不能写入普通 JSON 或文本配置")
)

// Secret owns its bytes. Calling Destroy prevents later access through this
// object; it does not promise erasure of copies made by the OS or HTTP runtime.
type Secret struct {
	state *secretState
}
type secretState struct {
	mu   sync.Mutex
	data []byte
}

func NewSecret(value []byte) (*Secret, error) {
	if len(value) == 0 || len(value) > MaxSecretBytes {
		return nil, ErrInvalid
	}
	return &Secret{state: &secretState{data: append([]byte(nil), value...)}}, nil
}
func (s *Secret) Bytes() ([]byte, error) {
	if s == nil || s.state == nil {
		return nil, ErrInvalid
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if len(s.state.data) == 0 {
		return nil, ErrInvalid
	}
	return append([]byte(nil), s.state.data...), nil
}
func (s *Secret) Destroy() {
	if s == nil || s.state == nil {
		return
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	Wipe(s.state.data)
	s.state.data = nil
}
func Wipe(bytes []byte) {
	for i := range bytes {
		bytes[i] = 0
	}
}
func (Secret) Format(state fmt.State, verb rune) { _, _ = io.WriteString(state, "[secret redacted]") }
func (Secret) MarshalJSON() ([]byte, error)      { return nil, ErrSerialization }
func (Secret) MarshalText() ([]byte, error)      { return nil, ErrSerialization }

type Vault interface {
	// Create never replaces an existing reference. Save a new configuration
	// with a new reference before explicitly deleting the previous secret.
	Create(context.Context, string, *Secret) error
	Read(context.Context, string) (*Secret, error)
	Delete(context.Context, string) error
}

var namespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)
var referencePattern = regexp.MustCompile(`^cred_[0-9a-f]{32}$`)

func NewReference() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "cred_" + hex.EncodeToString(value[:]), nil
}
func ValidReference(ref string) bool { return referencePattern.MatchString(ref) }
func NewVault(namespace string) (Vault, error) {
	if !namespacePattern.MatchString(namespace) {
		return nil, ErrInvalid
	}
	return newPlatformVault("com.workshelf.models." + namespace)
}
