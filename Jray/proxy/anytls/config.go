package anytls

import (
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/protobuf/proto"
)

// MemoryAccount is an account type converted from Account.
type MemoryAccount struct {
	Password string
}

// AsAccount implements protocol.AsAccount.
func (a *Account) AsAccount() (protocol.Account, error) {
	return &MemoryAccount{
		Password: a.GetPassword(),
	}, nil
}

// Equals implements protocol.Account.Equals.
func (a *MemoryAccount) Equals(another protocol.Account) bool {
	if other, ok := another.(*MemoryAccount); ok {
		return a.Password == other.Password
	}
	return false
}

// ToProto implements protocol.Account.ToProto.
func (a *MemoryAccount) ToProto() proto.Message {
	return &Account{
		Password: a.Password,
	}
}
