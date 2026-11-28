package anytls

import (
	std_bufio "bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/log"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/retry"
	sessionctx "github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/internet/tls"
)

// Server is the inbound handler for the AnyTLS protocol.
type Server struct {
	policyManager policy.Manager
	validator     *Validator
	padding       atomic.Pointer[paddingScheme]
	fallbacks     map[string]map[string]map[string]*Fallback
}

// NewServer creates a new AnyTLS inbound handler.
func NewServer(ctx context.Context, config *ServerConfig) (*Server, error) {
	if config == nil {
		return nil, errors.New("anytls: nil server config")
	}

	validator := NewValidator()
	for _, u := range config.Users {
		mu, err := u.ToMemoryUser()
		if err != nil {
			return nil, errors.New("anytls: failed to parse user").Base(err)
		}
		if err := validator.Add(mu); err != nil {
			return nil, errors.New("anytls: failed to add user").Base(err)
		}
	}

	scheme := defaultPaddingScheme
	if len(config.PaddingScheme) > 0 {
		parsed, err := parsePaddingScheme(config.PaddingScheme)
		if err != nil {
			return nil, err
		}
		if len(parsed.rawScheme) > maxFramePayload {
			return nil, errors.New("anytls: padding scheme exceeds max frame size")
		}
		scheme = parsed
	}

	v := core.MustFromContext(ctx)
	s := &Server{
		policyManager: v.GetFeature(policy.ManagerType()).(policy.Manager),
		validator:     validator,
	}
	s.padding.Store(scheme)

	if len(config.Fallbacks) > 0 {
		s.fallbacks = buildFallbackTable(config.Fallbacks)
	}

	return s, nil
}

func buildFallbackTable(fbs []*Fallback) map[string]map[string]map[string]*Fallback {
	table := make(map[string]map[string]map[string]*Fallback)
	for _, fb := range fbs {
		if table[fb.Name] == nil {
			table[fb.Name] = make(map[string]map[string]*Fallback)
		}
		if table[fb.Name][fb.Alpn] == nil {
			table[fb.Name][fb.Alpn] = make(map[string]*Fallback)
		}
		table[fb.Name][fb.Alpn][fb.Path] = fb
	}
	if table[""] != nil {
		for name, apfb := range table {
			if name != "" {
				for alpn := range table[""] {
					if apfb[alpn] == nil {
						apfb[alpn] = make(map[string]*Fallback)
					}
				}
			}
		}
	}
	for _, apfb := range table {
		if apfb[""] != nil {
			for alpn, pfb := range apfb {
				if alpn != "" {
					for path, fb := range apfb[""] {
						if pfb[path] == nil {
							pfb[path] = fb
						}
					}
				}
			}
		}
	}
	if table[""] != nil {
		for name, apfb := range table {
			if name != "" {
				for alpn, pfb := range table[""] {
					for path, fb := range pfb {
						if apfb[alpn][path] == nil {
							apfb[alpn][path] = fb
						}
					}
				}
			}
		}
	}
	return table
}

// AddUser implements proxy.UserManager.AddUser.
func (s *Server) AddUser(ctx context.Context, u *protocol.MemoryUser) error {
	return s.validator.Add(u)
}

// RemoveUser implements proxy.UserManager.RemoveUser.
func (s *Server) RemoveUser(ctx context.Context, email string) error {
	return s.validator.Del(email)
}

// GetUser implements proxy.UserManager.GetUser.
func (s *Server) GetUser(ctx context.Context, email string) *protocol.MemoryUser {
	return s.validator.GetByEmail(email)
}

// GetUsers implements proxy.UserManager.GetUsers.
func (s *Server) GetUsers(ctx context.Context) []*protocol.MemoryUser {
	return s.validator.GetAll()
}

// GetUsersCount implements proxy.UserManager.GetUsersCount.
func (s *Server) GetUsersCount(ctx context.Context) int64 {
	return s.validator.GetCount()
}

// Network implements proxy.Inbound.Network.
func (s *Server) Network() []xnet.Network {
	return []xnet.Network{xnet.Network_TCP, xnet.Network_UNIX}
}

// Process implements proxy.Inbound.Process.
func (s *Server) Process(ctx context.Context, network xnet.Network, conn stat.Connection, dispatcher routing.Dispatcher) error {
	iConn := stat.TryUnwrapStatsConn(conn)

	sessPol := s.policyManager.ForLevel(0)
	handshakeTimeout := sessPol.Timeouts.Handshake
	if handshakeTimeout <= 0 {
		handshakeTimeout = 60 * time.Second
	}
	if err := conn.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return errors.New("anytls: unable to set read deadline").Base(err)
	}

	var br *std_bufio.Reader
	var user *protocol.MemoryUser
	var padLen uint16

	if len(s.fallbacks) > 0 {
		first := buf.New()
		firstLen, err := first.ReadFrom(conn)
		if err != nil {
			first.Release()
			return errors.New("anytls: failed to read initial request").Base(err)
		}

		var authErr error
		if firstLen < passwordLen {
			authErr = errors.New("anytls: invalid auth prologue")
		} else {
			var sum [passwordLen]byte
			copy(sum[:], first.BytesTo(passwordLen))
			user = s.validator.GetByHash(sum)
			if user == nil {
				authErr = errors.New("anytls: invalid user")
			}
		}

		if user == nil {
			log.Record(&log.AccessMessage{
				From:   conn.RemoteAddr(),
				Status: log.AccessRejected,
				Reason: authErr,
			})
			cached := newCachedConn(conn, first)
			return s.fallback(ctx, authErr, sessPol, conn, iConn, cached, first, firstLen)
		}

		first.Advance(passwordLen)
		if first.Len() < 2 {
			if _, err := first.ReadFullFrom(conn, 2-first.Len()); err != nil {
				first.Release()
				return errors.New("anytls: failed to read padding length").Base(err)
			}
		}
		lenBytes, _ := first.ReadBytes(2)
		padLen = binary.BigEndian.Uint16(lenBytes)

		var netConn net.Conn = conn
		if !first.IsEmpty() {
			netConn = newCachedConn(conn, first)
		} else {
			first.Release()
		}
		br = std_bufio.NewReaderSize(netConn, sessionReadBufferSize)
	} else {
		var hdr [passwordLen + 2]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return errors.New("anytls: read auth").Base(err)
		}
		var sum [passwordLen]byte
		copy(sum[:], hdr[:passwordLen])
		user = s.validator.GetByHash(sum)
		if user == nil {
			authErr := errors.New("anytls: invalid user")
			log.Record(&log.AccessMessage{
				From:   conn.RemoteAddr(),
				Status: log.AccessRejected,
				Reason: authErr,
			})
			return authErr
		}
		padLen = binary.BigEndian.Uint16(hdr[passwordLen:])
		br = std_bufio.NewReaderSize(conn, sessionReadBufferSize)
	}

	if padLen > 0 {
		if _, err := br.Discard(int(padLen)); err != nil {
			padErr := errors.New("anytls: read padding0").Base(err)
			log.Record(&log.AccessMessage{
				From:   conn.RemoteAddr(),
				Status: log.AccessRejected,
				Reason: padErr,
				Email:  user.Email,
			})
			return padErr
		}
	}
	_ = conn.SetReadDeadline(time.Time{})

	if inb := sessionctx.InboundFromContext(ctx); inb != nil {
		cp := *inb
		cp.Name = protocolName
		cp.CanSpliceCopy = 3
		cp.User = user
		ctx = sessionctx.ContextWithInbound(ctx, &cp)
	}

	sess := newServerSession(s, conn, br, dispatcher, s.policyManager)
	sess.userLevel = user.Level
	sess.userEmail = user.Email
	sess.connectionIdle = s.policyManager.ForLevel(user.Level).Timeouts.ConnectionIdle
	defer sess.close(nil)

	idleStop := make(chan struct{})
	defer close(idleStop)
	go sess.watchServerIdle(ctx, idleStop)

	return sess.readLoop(ctx)
}

func (s *Server) fallback(ctx context.Context, authErr error, sessionPolicy policy.Session, connection stat.Connection, iConn stat.Connection, cached net.Conn, first *buf.Buffer, firstLen int64) error {
	defer cached.Close()
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		errors.LogWarningInner(ctx, err, "unable to set back read deadline")
	}
	errors.LogInfoInner(ctx, authErr, "fallback starts")

	name := ""
	alpn := ""
	switch tc := iConn.(type) {
	case *tls.Conn:
		cs := tc.ConnectionState()
		name = cs.ServerName
		alpn = cs.NegotiatedProtocol
	case *tls.UConn:
		cs := tc.ConnectionState()
		name = cs.ServerName
		alpn = cs.NegotiatedProtocol
	case *reality.Conn:
		cs := tc.ConnectionState()
		name = cs.ServerName
		alpn = cs.NegotiatedProtocol
	case *reality.UConn:
		cs := tc.ConnectionState()
		name = cs.ServerName
		alpn = cs.NegotiatedProtocol
	}
	name = strings.ToLower(name)
	alpn = strings.ToLower(alpn)

	napfb := s.fallbacks
	if len(napfb) > 1 || napfb[""] == nil {
		if name != "" && napfb[name] == nil {
			match := ""
			for n := range napfb {
				if n != "" && strings.Contains(name, n) && len(n) > len(match) {
					match = n
				}
			}
			name = match
		}
	}
	if napfb[name] == nil {
		name = ""
	}
	apfb := napfb[name]
	if apfb == nil {
		return errors.New(`failed to find the default "name" config`)
	}
	if apfb[alpn] == nil {
		alpn = ""
	}
	pfb := apfb[alpn]
	if pfb == nil {
		return errors.New(`failed to find the default "alpn" config`)
	}

	path := ""
	if len(pfb) > 1 || pfb[""] == nil {
		if firstLen >= 18 && first.Byte(4) != '*' {
			firstBytes := first.Bytes()
			for i := 4; i <= 8 && i < len(firstBytes); i++ {
				if firstBytes[i] == '/' && firstBytes[i-1] == ' ' {
					search := min(len(firstBytes), 64)
					for j := i + 1; j < search; j++ {
						k := firstBytes[j]
						if k == '\r' || k == '\n' {
							break
						}
						if k == '?' || k == ' ' {
							path = string(firstBytes[i:j])
							if pfb[path] == nil {
								path = ""
							}
							break
						}
					}
					break
				}
			}
		}
	}
	fb := pfb[path]
	if fb == nil {
		return errors.New(`failed to find the default "path" config`)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := signal.CancelAfterInactivity(ctx, cancel, sessionPolicy.Timeouts.ConnectionIdle)
	defer timer.SetTimeout(0)

	var fbConn net.Conn
	if err := retry.ExponentialBackoff(5, 100).On(func() error {
		var dialer net.Dialer
		var derr error
		fbConn, derr = dialer.DialContext(ctx, fb.Type, fb.Dest)
		return derr
	}); err != nil {
		return errors.New("failed to dial to " + fb.Dest).Base(err)
	}
	defer fbConn.Close()

	clientReader := buf.NewReader(cached)
	clientWriter := buf.NewWriter(cached)
	serverReader := buf.NewReader(fbConn)
	serverWriter := buf.NewWriter(fbConn)

	postRequest := func() error {
		defer timer.SetTimeout(sessionPolicy.Timeouts.DownlinkOnly)
		if fb.Xver != 0 {
			if err := writeProxyProtocolHeader(serverWriter, fb.Xver, connection.RemoteAddr(), connection.LocalAddr()); err != nil {
				return errors.New("failed to set PROXY protocol v", fb.Xver).Base(err)
			}
		}
		if err := buf.Copy(clientReader, serverWriter, buf.UpdateActivity(timer)); err != nil {
			return errors.New("failed to fallback request payload").Base(err)
		}
		return nil
	}
	getResponse := func() error {
		defer timer.SetTimeout(sessionPolicy.Timeouts.UplinkOnly)
		if err := buf.Copy(serverReader, clientWriter, buf.UpdateActivity(timer)); err != nil {
			return errors.New("failed to deliver response payload").Base(err)
		}
		return nil
	}

	if err := task.Run(ctx, task.OnSuccess(postRequest, task.Close(serverWriter)), task.OnSuccess(getResponse, task.Close(clientWriter))); err != nil {
		common.Interrupt(serverReader)
		common.Interrupt(serverWriter)
		return errors.New("fallback ends").Base(err)
	}
	return nil
}

func writeProxyProtocolHeader(w buf.Writer, xver uint64, remoteAddr, localAddr net.Addr) error {
	ipType := 4
	rHost, rPort, err := net.SplitHostPort(remoteAddr.String())
	if err != nil {
		ipType = 0
	}
	lHost, lPort, err := net.SplitHostPort(localAddr.String())
	if err != nil {
		ipType = 0
	}
	if ipType == 4 && strings.Contains(rHost, ":") {
		ipType = 6
	}
	pro := buf.New()
	switch xver {
	case 1:
		switch ipType {
		case 0:
			_, _ = pro.Write([]byte("PROXY UNKNOWN\r\n"))
		case 4:
			_, _ = pro.Write([]byte("PROXY TCP4 " + rHost + " " + lHost + " " + rPort + " " + lPort + "\r\n"))
		default:
			_, _ = pro.Write([]byte("PROXY TCP6 " + rHost + " " + lHost + " " + rPort + " " + lPort + "\r\n"))
		}
	case 2:
		_, _ = pro.Write([]byte("\x0D\x0A\x0D\x0A\x00\x0D\x0A\x51\x55\x49\x54\x0A"))
		switch ipType {
		case 0:
			_, _ = pro.Write([]byte("\x20\x00\x00\x00"))
		case 4:
			_, _ = pro.Write([]byte("\x21\x11\x00\x0C"))
			_, _ = pro.Write(net.ParseIP(rHost).To4())
			_, _ = pro.Write(net.ParseIP(lHost).To4())
		default:
			_, _ = pro.Write([]byte("\x21\x21\x00\x24"))
			_, _ = pro.Write(net.ParseIP(rHost).To16())
			_, _ = pro.Write(net.ParseIP(lHost).To16())
		}
		if ipType != 0 {
			p1, _ := strconv.ParseUint(rPort, 10, 16)
			p2, _ := strconv.ParseUint(lPort, 10, 16)
			_, _ = pro.Write([]byte{byte(p1 >> 8), byte(p1), byte(p2 >> 8), byte(p2)})
		}
	}
	return w.WriteMultiBuffer(buf.MultiBuffer{pro})
}

type cachedConn struct {
	net.Conn
	mu     sync.Mutex
	cached *buf.Buffer
	empty  atomic.Bool
}

func newCachedConn(conn net.Conn, cached *buf.Buffer) *cachedConn {
	cc := &cachedConn{
		Conn:   conn,
		cached: cached,
	}
	if cached == nil || cached.IsEmpty() {
		cc.empty.Store(true)
	}
	return cc
}

func (c *cachedConn) Read(p []byte) (int, error) {
	if !c.empty.Load() {
		c.mu.Lock()
		if c.cached != nil && !c.cached.IsEmpty() {
			n, _ := c.cached.Read(p)
			if c.cached.IsEmpty() {
				c.cached.Release()
				c.cached = nil
				c.empty.Store(true)
			}
			c.mu.Unlock()
			return n, nil
		}
		c.empty.Store(true)
		c.mu.Unlock()
	}
	return c.Conn.Read(p)
}

func (c *cachedConn) Close() error {
	c.mu.Lock()
	if c.cached != nil {
		c.cached.Release()
		c.cached = nil
		c.empty.Store(true)
	}
	c.mu.Unlock()
	return c.Conn.Close()
}
