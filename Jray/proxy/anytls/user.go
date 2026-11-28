package anytls

import (
	"crypto/sha256"
	"maps"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/protocol"
)

type userSnapshot struct {
	byHash  map[[passwordLen]byte]*protocol.MemoryUser
	byEmail map[string][32]byte
}

// Validator stores valid AnyTLS users with lock-free read lookups.
type Validator struct {
	mu   sync.Mutex
	snap atomic.Pointer[userSnapshot]
}

// NewValidator creates a new AnyTLS user validator.
func NewValidator() *Validator {
	v := &Validator{}
	v.snap.Store(&userSnapshot{
		byHash:  make(map[[passwordLen]byte]*protocol.MemoryUser),
		byEmail: make(map[string][32]byte),
	})
	return v
}

// Add registers a user. Password must be non-empty and unique; non-empty email must also be unique.
func (v *Validator) Add(u *protocol.MemoryUser) error {
	if u == nil || u.Account == nil {
		return errors.New("anytls: invalid user")
	}
	acc, ok := u.Account.(*MemoryAccount)
	if !ok {
		return errors.New("anytls: invalid account type")
	}
	if acc.Password == "" {
		return errors.New("anytls: empty password")
	}
	sum := sha256.Sum256([]byte(acc.Password))
	emailKey := strings.ToLower(u.Email)

	v.mu.Lock()
	defer v.mu.Unlock()

	old := v.snap.Load()
	if emailKey != "" {
		if _, exists := old.byEmail[emailKey]; exists {
			return errors.New("User ", u.Email, " already exists.")
		}
	}
	if _, exists := old.byHash[sum]; exists {
		return errors.New("anytls: duplicate user password")
	}

	newByHash := maps.Clone(old.byHash)
	newByEmail := maps.Clone(old.byEmail)
	newByHash[sum] = u
	if emailKey != "" {
		newByEmail[emailKey] = sum
	}
	v.snap.Store(&userSnapshot{
		byHash:  newByHash,
		byEmail: newByEmail,
	})
	return nil
}

// Del removes a user by non-empty email.
func (v *Validator) Del(email string) error {
	if email == "" {
		return errors.New("Email must not be empty.")
	}
	emailKey := strings.ToLower(email)

	v.mu.Lock()
	defer v.mu.Unlock()

	old := v.snap.Load()
	sum, exists := old.byEmail[emailKey]
	if !exists {
		return errors.New("User ", email, " not found.")
	}

	newByHash := maps.Clone(old.byHash)
	newByEmail := maps.Clone(old.byEmail)
	delete(newByEmail, emailKey)
	delete(newByHash, sum)
	v.snap.Store(&userSnapshot{
		byHash:  newByHash,
		byEmail: newByEmail,
	})
	return nil
}

// GetByHash looks up a user by SHA-256(password) without locking.
func (v *Validator) GetByHash(sum [passwordLen]byte) *protocol.MemoryUser {
	return v.snap.Load().byHash[sum]
}

// GetByEmail looks up a user by case-insensitive email.
func (v *Validator) GetByEmail(email string) *protocol.MemoryUser {
	if email == "" {
		return nil
	}
	snap := v.snap.Load()
	if sum, ok := snap.byEmail[strings.ToLower(email)]; ok {
		return snap.byHash[sum]
	}
	return nil
}

// GetAll returns all registered users.
func (v *Validator) GetAll() []*protocol.MemoryUser {
	snap := v.snap.Load()
	users := make([]*protocol.MemoryUser, 0, len(snap.byHash))
	for _, u := range snap.byHash {
		users = append(users, u)
	}
	return users
}

// GetCount returns the total number of registered users.
func (v *Validator) GetCount() int64 {
	return int64(len(v.snap.Load().byHash))
}
