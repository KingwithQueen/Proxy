package anytls

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/retry"
	sessionctx "github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

const (
	defaultIdleSessionCheckInterval = 30 * time.Second
	defaultIdleSessionTimeout       = 30 * time.Second
	minimumIdleSessionInterval      = 5 * time.Second
)

// Client is the outbound handler for the AnyTLS protocol.
type Client struct {
	server        *protocol.ServerSpec
	policyManager policy.Manager
	userLevel     uint32
	authHash      [passwordLen]byte

	idleSessionCheckInterval time.Duration
	idleSessionTimeout       time.Duration
	minIdleSession           int

	padding atomic.Pointer[paddingScheme]

	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	closed       bool
	sessions     map[uint64]*session
	idleSessions []*session
	sessionSeq   atomic.Uint64
	idleTimer    *time.Timer
}

// NewClient creates a new AnyTLS outbound handler.
func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	if config == nil || config.Server == nil {
		return nil, errors.New("anytls: no server specified")
	}
	server, err := protocol.NewServerSpecFromPB(config.Server)
	if err != nil {
		return nil, errors.New("anytls: failed to get server spec").Base(err)
	}
	if server.User == nil {
		return nil, errors.New("anytls: no user specified")
	}
	acc, ok := server.User.Account.(*MemoryAccount)
	if !ok || acc.Password == "" {
		return nil, errors.New("anytls: invalid or empty password")
	}

	checkInterval := time.Duration(config.GetIdleSessionCheckInterval()) * time.Second
	if checkInterval <= minimumIdleSessionInterval {
		checkInterval = defaultIdleSessionCheckInterval
	}
	idleTimeout := time.Duration(config.GetIdleSessionTimeout()) * time.Second
	if idleTimeout <= minimumIdleSessionInterval {
		idleTimeout = defaultIdleSessionTimeout
	}

	v := core.MustFromContext(ctx)
	c := &Client{
		server:                   server,
		policyManager:            v.GetFeature(policy.ManagerType()).(policy.Manager),
		userLevel:                server.User.Level,
		authHash:                 sha256.Sum256([]byte(acc.Password)),
		idleSessionCheckInterval: checkInterval,
		idleSessionTimeout:       idleTimeout,
		minIdleSession:           int(config.GetMinIdleSession()),
		sessions:                 make(map[uint64]*session),
	}
	c.padding.Store(defaultPaddingScheme)
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c, nil
}

// Close implements common.Closable.Close and tears down all pooled sessions.
func (c *Client) Close() error {
	c.cancel()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
	toClose := make([]*session, 0, len(c.sessions))
	for _, sess := range c.sessions {
		toClose = append(toClose, sess)
	}
	clear(c.sessions)
	c.idleSessions = nil
	c.mu.Unlock()

	for _, sess := range toClose {
		sess.close(netErrClosed())
	}
	return nil
}

func netErrClosed() error {
	return errors.New("anytls: client closed")
}

// Process implements proxy.Outbound.Process.
func (c *Client) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	outbounds := sessionctx.OutboundsFromContext(ctx)
	if len(outbounds) == 0 {
		return errors.New("anytls: target not specified")
	}
	ob := outbounds[len(outbounds)-1]
	target := ob.Target
	if !target.IsValid() || (target.Network != xnet.Network_TCP && target.Network != xnet.Network_UDP) {
		return errors.New("anytls: unsupported target")
	}
	ob.Name = protocolName
	ob.CanSpliceCopy = 3

	sessionPolicy := c.policyManager.ForLevel(c.userLevel)
	actualDest := target
	if target.Network == xnet.Network_UDP {
		actualDest = xnet.TCPDestination(xnet.DomainAddress(uotMagicAddress), 0)
	}

	_, st, err := c.openStream(ctx, dialer, sessionPolicy.Timeouts.Handshake, actualDest)
	if err != nil {
		return errors.New("anytls: failed to open stream").Base(err)
	}
	defer func() {
		_ = st.Close()
	}()

	errors.LogInfo(ctx, "tunneling request to ", target, " via anytls ", c.server.Destination.NetAddr())

	var reader buf.Reader = st
	var writer buf.Writer = st
	if target.Network == xnet.Network_UDP {
		reqBuf := buf.New()
		if err := writeUoTRequest(reqBuf, false, target); err != nil {
			reqBuf.Release()
			return errors.New("anytls: failed to encode UoT request").Base(err)
		}
		if err := st.WriteMultiBuffer(buf.MultiBuffer{reqBuf}); err != nil {
			return errors.New("anytls: failed to write UoT request").Base(err)
		}
		reader = newUoTReader(st.bufferedReader(), false, target)
		writer = newUoTWriter(st, false, target)
	}

	var newCtx context.Context
	var newCancel context.CancelFunc
	if sessionctx.TimeoutOnlyFromContext(ctx) {
		newCtx, newCancel = context.WithCancel(context.Background())
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := signal.CancelAfterInactivity(runCtx, func() {
		cancel()
		if newCancel != nil {
			newCancel()
		}
	}, sessionPolicy.Timeouts.ConnectionIdle)
	defer timer.SetTimeout(0)

	if newCtx != nil {
		runCtx = newCtx
	}

	stopStreamOnCancel := context.AfterFunc(runCtx, func() {
		_ = st.Close()
	})
	defer stopStreamOnCancel()

	requestDone := func() error {
		defer timer.SetTimeout(sessionPolicy.Timeouts.DownlinkOnly)
		return buf.Copy(link.Reader, writer, buf.UpdateActivity(timer))
	}
	responseDone := func() error {
		defer timer.SetTimeout(sessionPolicy.Timeouts.UplinkOnly)
		return buf.Copy(reader, link.Writer, buf.UpdateActivity(timer))
	}

	responseDoneAndCloseWriter := task.OnSuccess(responseDone, task.Close(link.Writer))
	if err := task.Run(runCtx, requestDone, responseDoneAndCloseWriter); err != nil {
		common.Interrupt(link.Reader)
		common.Interrupt(link.Writer)
		return errors.New("anytls: connection ends").Base(err)
	}
	return nil
}

func (c *Client) openStream(ctx context.Context, dialer internet.Dialer, handshakeTimeout time.Duration, dest xnet.Destination) (*session, *stream, error) {
	makeHook := func(s *session) func() {
		return func() {
			if s.isClosed() || s.activeStreams.Load() != 0 {
				return
			}
			c.releaseSession(s)
		}
	}

	// First try reusing the newest idle session(s), draining any stale dead sessions.
	for {
		idle := c.takeIdleSession()
		if idle == nil {
			break
		}
		st, err := idle.openStream(ctx, dest, makeHook(idle))
		if err == nil && !idle.isClosed() {
			return idle, st, nil
		}
		if st != nil {
			_ = st.Close()
		}
		idle.close(err)
	}

	sess, err := c.createSession(ctx, dialer, handshakeTimeout)
	if err != nil {
		return nil, nil, err
	}
	st, err := sess.openStream(ctx, dest, makeHook(sess))
	if err != nil {
		sess.close(err)
		return nil, nil, err
	}
	return sess, st, nil
}

func (c *Client) createSession(ctx context.Context, dialer internet.Dialer, handshakeTimeout time.Duration) (*session, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, netErrClosed()
	}

	if handshakeTimeout <= 0 {
		handshakeTimeout = 60 * time.Second
	}

	// Decouple pooled connection lifecycle from individual request cancellation.
	dialCtx, dialCancel := context.WithTimeout(context.WithoutCancel(ctx), handshakeTimeout)
	defer dialCancel()
	stopClientWatch := context.AfterFunc(c.ctx, dialCancel)
	defer stopClientWatch()

	// Also abort dial if the initiating caller cancels before dial completes.
	stopReqWatch := context.AfterFunc(ctx, dialCancel)

	var conn stat.Connection
	err := retry.ExponentialBackoff(5, 100).On(func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rawConn, derr := dialer.Dial(dialCtx, c.server.Destination)
		if derr != nil {
			return derr
		}
		conn = rawConn
		return nil
	})
	stopReqWatch()
	if err != nil {
		return nil, err
	}

	padLen := c.padding.Load().handshakePaddingSize()
	authBuf := buf.NewWithSize(int32(passwordLen + 2 + padLen))
	_, _ = authBuf.Write(c.authHash[:])
	binary.BigEndian.PutUint16(authBuf.Extend(2), uint16(padLen))
	if padLen > 0 {
		_ = authBuf.Extend(int32(padLen))
	}
	_ = conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
	err = writeAll(conn, authBuf.Bytes())
	authBuf.Release()
	if err != nil {
		_ = conn.Close()
		return nil, errors.New("anytls: write auth failed").Base(err)
	}
	_ = conn.SetWriteDeadline(time.Time{})

	seq := c.sessionSeq.Add(1)
	sess := newClientSession(c, conn, seq)
	sess.dieHook = func() {
		c.removeSession(sess)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		sess.close(netErrClosed())
		return nil, netErrClosed()
	}
	c.sessions[seq] = sess
	c.mu.Unlock()

	go func() {
		loopErr := sess.readLoop(c.ctx)
		sess.close(loopErr)
	}()

	return sess, nil
}

func (c *Client) takeIdleSession() *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	for len(c.idleSessions) > 0 {
		last := len(c.idleSessions) - 1
		sess := c.idleSessions[last]
		c.idleSessions[last] = nil
		c.idleSessions = c.idleSessions[:last]
		sess.inIdlePool.Store(false)
		if sess.isClosed() {
			continue
		}
		return sess
	}
	return nil
}

func (c *Client) releaseSession(sess *session) {
	if sess == nil || sess.isClosed() {
		return
	}
	c.mu.Lock()
	if c.closed || sess.isClosed() || sess.activeStreams.Load() != 0 {
		c.mu.Unlock()
		return
	}
	if sess.inIdlePool.CompareAndSwap(false, true) {
		sess.idleSinceNano.Store(time.Now().UnixNano())
		c.idleSessions = append(c.idleSessions, sess)
		if c.idleTimer == nil {
			c.idleTimer = time.AfterFunc(c.idleSessionCheckInterval, c.cleanupIdleSessions)
		}
	}
	c.mu.Unlock()
}

func (c *Client) removeSession(sess *session) {
	c.mu.Lock()
	delete(c.sessions, sess.seq)
	if sess.inIdlePool.Swap(false) {
		for i, item := range c.idleSessions {
			if item == sess {
				c.idleSessions = append(c.idleSessions[:i], c.idleSessions[i+1:]...)
				break
			}
		}
	}
	c.mu.Unlock()
}

func (c *Client) cleanupIdleSessions() {
	now := time.Now()
	var expired []*session

	c.mu.Lock()
	if c.closed {
		c.idleTimer = nil
		c.mu.Unlock()
		return
	}

	// Compact out closed or non-idle entries while preserving newest-at-end order.
	valid := c.idleSessions[:0]
	for _, sess := range c.idleSessions {
		if sess == nil || sess.isClosed() || !sess.inIdlePool.Load() {
			continue
		}
		valid = append(valid, sess)
	}
	c.idleSessions = valid

	keepFrom := max(0, len(c.idleSessions)-c.minIdleSession)
	kept := c.idleSessions[:0]
	for i, sess := range c.idleSessions {
		if i >= keepFrom {
			kept = append(kept, sess)
			continue
		}
		idleSince := sess.idleSinceNano.Load()
		if idleSince == 0 || now.Sub(time.Unix(0, idleSince)) <= c.idleSessionTimeout {
			kept = append(kept, sess)
			continue
		}
		sess.inIdlePool.Store(false)
		expired = append(expired, sess)
	}
	c.idleSessions = kept
	if len(c.idleSessions) > 0 {
		c.idleTimer.Reset(c.idleSessionCheckInterval)
	} else {
		c.idleTimer = nil
	}
	c.mu.Unlock()

	for _, sess := range expired {
		sess.close(nil)
	}
}
