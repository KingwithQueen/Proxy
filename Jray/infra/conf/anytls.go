package conf

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/anytls"
	"google.golang.org/protobuf/proto"
)

// AnyTLSPaddingScheme accepts either a JSON array of lines or a single multiline string.
type AnyTLSPaddingScheme []string

func (p *AnyTLSPaddingScheme) UnmarshalJSON(data []byte) error {
	var lines []string
	if err := json.Unmarshal(data, &lines); err == nil {
		*p = lines
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		if raw != "" {
			*p = strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
		}
		return nil
	}
	return errors.New("anytls: invalid paddingScheme format")
}

type AnyTLSUserConfig struct {
	Password string `json:"password"`
	Level    uint32 `json:"level"`
	Email    string `json:"email"`
}

type AnyTLSInboundFallback struct {
	Name string          `json:"name"`
	Alpn string          `json:"alpn"`
	Path string          `json:"path"`
	Type string          `json:"type"`
	Dest json.RawMessage `json:"dest"`
	Xver uint64          `json:"xver"`
}

type AnyTLSServerConfig struct {
	Users         []*AnyTLSUserConfig      `json:"users"`
	Clients       []*AnyTLSUserConfig      `json:"clients"`
	PaddingScheme AnyTLSPaddingScheme      `json:"paddingScheme"`
	Fallbacks     []*AnyTLSInboundFallback `json:"fallbacks"`
}

func (c *AnyTLSServerConfig) Build() (proto.Message, error) {
	users := c.Users
	if c.Clients != nil {
		users = c.Clients
	}
	if len(users) == 0 {
		return nil, errors.New("anytls: users required")
	}

	config := &anytls.ServerConfig{
		Users: make([]*protocol.User, 0, len(users)),
	}
	if len(c.PaddingScheme) > 0 {
		config.PaddingScheme = []byte(strings.Join(c.PaddingScheme, "\n"))
		if err := anytls.ValidatePaddingScheme(config.PaddingScheme); err != nil {
			return nil, err
		}
	}

	seenPasswords := make(map[string]struct{}, len(users))
	seenEmails := make(map[string]struct{}, len(users))
	for _, u := range users {
		if u == nil || u.Password == "" {
			return nil, errors.New("anytls: user password required")
		}
		if _, dup := seenPasswords[u.Password]; dup {
			return nil, errors.New("anytls: duplicate password")
		}
		seenPasswords[u.Password] = struct{}{}
		if u.Email != "" {
			emailKey := strings.ToLower(u.Email)
			if _, dup := seenEmails[emailKey]; dup {
				return nil, errors.New("anytls: duplicate email ", u.Email)
			}
			seenEmails[emailKey] = struct{}{}
		}
		config.Users = append(config.Users, &protocol.User{
			Level: u.Level,
			Email: u.Email,
			Account: serial.ToTypedMessage(&anytls.Account{
				Password: u.Password,
			}),
		})
	}

	for _, fb := range c.Fallbacks {
		var i uint16
		var s string
		if err := json.Unmarshal(fb.Dest, &i); err == nil {
			s = strconv.Itoa(int(i))
		} else {
			_ = json.Unmarshal(fb.Dest, &s)
		}
		config.Fallbacks = append(config.Fallbacks, &anytls.Fallback{
			Name: fb.Name,
			Alpn: fb.Alpn,
			Path: fb.Path,
			Type: fb.Type,
			Dest: s,
			Xver: fb.Xver,
		})
	}
	for _, fb := range config.Fallbacks {
		if fb.Path != "" && fb.Path[0] != '/' {
			return nil, errors.New(`AnyTLS fallbacks: "path" must be empty or start with "/"`)
		}
		if fb.Type == "" && fb.Dest != "" {
			if filepath.IsAbs(fb.Dest) || fb.Dest[0] == '@' {
				fb.Type = "unix"
				if strings.HasPrefix(fb.Dest, "@@") && (runtime.GOOS == "linux" || runtime.GOOS == "android") {
					fullAddr := make([]byte, len(syscall.RawSockaddrUnix{}.Path))
					copy(fullAddr, fb.Dest[1:])
					fb.Dest = string(fullAddr)
				}
			} else {
				if _, err := strconv.Atoi(fb.Dest); err == nil {
					fb.Dest = "localhost:" + fb.Dest
				}
				if _, _, err := net.SplitHostPort(fb.Dest); err == nil {
					fb.Type = "tcp"
				}
			}
		}
		if fb.Type == "" {
			return nil, errors.New(`AnyTLS fallbacks: please fill in a valid value for every "dest"`)
		}
		if fb.Xver > 2 {
			return nil, errors.New(`AnyTLS fallbacks: invalid PROXY protocol version, "xver" only accepts 0, 1, 2`)
		}
	}

	return config, nil
}

type AnyTLSClientConfig struct {
	Address                  *Address `json:"address"`
	Port                     uint16   `json:"port"`
	Password                 string   `json:"password"`
	Email                    string   `json:"email"`
	Level                    uint32   `json:"level"`
	IdleSessionCheckInterval uint32   `json:"idleSessionCheckInterval"`
	IdleSessionTimeout       uint32   `json:"idleSessionTimeout"`
	MinIdleSession           int32    `json:"minIdleSession"`
}

func (c *AnyTLSClientConfig) Build() (proto.Message, error) {
	if c.Address == nil || c.Port == 0 {
		return nil, errors.New("anytls: server address and port are required")
	}
	if c.Password == "" {
		return nil, errors.New("anytls: password is required")
	}
	if c.MinIdleSession < 0 {
		return nil, errors.New("anytls: minIdleSession must be non-negative")
	}

	return &anytls.ClientConfig{
		Server: &protocol.ServerEndpoint{
			Address: c.Address.Build(),
			Port:    uint32(c.Port),
			User: &protocol.User{
				Level: c.Level,
				Email: c.Email,
				Account: serial.ToTypedMessage(&anytls.Account{
					Password: c.Password,
				}),
			},
		},
		IdleSessionCheckInterval: c.IdleSessionCheckInterval,
		IdleSessionTimeout:       c.IdleSessionTimeout,
		MinIdleSession:           uint32(c.MinIdleSession),
	}, nil
}
