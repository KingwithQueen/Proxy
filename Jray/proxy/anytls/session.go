package anytls

import (
	std_bufio "bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/log"
	xnet "github.com/xtls/xray-core/common/net"
	xsession "github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
)

const (
	sessionReadBufferSize    = 16384
	streamOpenTimeout        = 3 * time.Second
	controlFrameWriteTimeout = 5 * time.Second
	serverHeartbeatTimeout   = 30 * time.Second
)

type anytlsSessionKey struct{}

type session struct {
	conn net.Conn
	br   *std_bufio.Reader

	isClient bool
	seq      uint64

	writeAccess chan struct{}
	pending     *buf.Buffer // initial cmdSettings frame buffered until first stream open
	sendPadding atomic.Bool
	pktCounter  uint32 // protected by writeAccess

	padding *atomic.Pointer[paddingScheme]

	streamsMu        sync.RWMutex
	streams          map[uint32]*stream
	nextSID          atomic.Uint32
	peerVersion      atomic.Uint32
	activeStreams    atomic.Int32
	settingsReceived bool

	timeoutMu        sync.Mutex
	openTimeout      *time.Timer
	openTimeoutArmed atomic.Bool

	closeOnce sync.Once
	closed    atomic.Bool
	done      chan struct{}
	closeErr  atomic.Pointer[error]
	dieHook   func()

	// client pool fields
	client        *Client
	inIdlePool    atomic.Bool
	idleSinceNano atomic.Int64

	// server fields
	server         *Server
	dispatcher     routing.Dispatcher
	policyManager  policy.Manager
	userLevel      uint32
	userEmail      string
	connectionIdle time.Duration
	lastReadNano   atomic.Int64
}

func newClientSession(client *Client, conn net.Conn, seq uint64) *session {
	scheme := client.padding.Load()
	settingsData := encodeSettings(map[string]string{
		settingVersion:    strconv.Itoa(protocolVersion),
		settingClient:     "jray/" + core.Version(),
		settingPaddingMD5: scheme.md5Hex,
	})
	pending := buf.NewWithSize(int32(frameOverhead + len(settingsData)))
	putFrameHeader(pending.Extend(frameOverhead), cmdSettings, 0, len(settingsData))
	_, _ = pending.Write(settingsData)

	s := &session{
		conn:        conn,
		br:          std_bufio.NewReaderSize(conn, sessionReadBufferSize),
		isClient:    true,
		seq:         seq,
		writeAccess: make(chan struct{}, 1),
		pending:     pending,
		padding:     &client.padding,
		streams:     make(map[uint32]*stream),
		done:        make(chan struct{}),
		client:      client,
	}
	s.sendPadding.Store(true)
	s.peerVersion.Store(1)
	return s
}

func newServerSession(server *Server, conn net.Conn, br *std_bufio.Reader, dispatcher routing.Dispatcher, pm policy.Manager) *session {
	s := &session{
		conn:           conn,
		br:             br,
		isClient:       false,
		writeAccess:    make(chan struct{}, 1),
		padding:        &server.padding,
		streams:        make(map[uint32]*stream),
		done:           make(chan struct{}),
		server:         server,
		dispatcher:     dispatcher,
		policyManager:  pm,
		connectionIdle: pm.ForLevel(0).Timeouts.ConnectionIdle,
	}
	s.peerVersion.Store(1)
	s.lastReadNano.Store(time.Now().UnixNano())
	return s
}

func (s *session) isClosed() bool {
	return s.closed.Load()
}

func (s *session) close(err error) {
	if err != nil {
		s.closeErr.CompareAndSwap(nil, &err)
	}
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.done)
		_ = s.conn.SetDeadline(time.Now())
		s.timeoutMu.Lock()
		if s.openTimeout != nil {
			s.openTimeout.Stop()
		}
		s.timeoutMu.Unlock()

		_ = s.conn.Close()

		s.streamsMu.Lock()
		streams := make([]*stream, 0, len(s.streams))
		for _, st := range s.streams {
			streams = append(streams, st)
		}
		clear(s.streams)
		s.streamsMu.Unlock()

		streamErr := err
		if streamErr == nil {
			streamErr = net.ErrClosed
		}
		for _, st := range streams {
			st.closeWithError(streamErr, false)
		}

		// Clean up pending buffer if writeAccess is free.
		select {
		case s.writeAccess <- struct{}{}:
			if s.pending != nil {
				s.pending.Release()
				s.pending = nil
			}
			<-s.writeAccess
		default:
		}

		if s.dieHook != nil {
			s.dieHook()
		}
	})
}

func (s *session) removeStream(sid uint32) {
	s.streamsMu.Lock()
	_, existed := s.streams[sid]
	if existed {
		delete(s.streams, sid)
		if s.isClient {
			s.activeStreams.Add(-1)
		}
	}
	s.streamsMu.Unlock()
}

func (s *session) lockWriteDeadline(deadline time.Time) error {
	if s.isClosed() {
		return net.ErrClosed
	}
	select {
	case s.writeAccess <- struct{}{}:
		return nil
	case <-s.done:
		return net.ErrClosed
	default:
	}
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return os.ErrDeadlineExceeded
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case s.writeAccess <- struct{}{}:
		if s.isClosed() {
			<-s.writeAccess
			return net.ErrClosed
		}
		return nil
	case <-s.done:
		return net.ErrClosed
	case <-timer.C:
		return os.ErrDeadlineExceeded
	}
}

func (s *session) lockWriteContext(ctx context.Context) error {
	if s.isClosed() {
		return net.ErrClosed
	}
	select {
	case s.writeAccess <- struct{}{}:
		if s.isClosed() {
			<-s.writeAccess
			return net.ErrClosed
		}
		return nil
	case <-s.done:
		return net.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) unlockWrite() {
	<-s.writeAccess
}

func writeAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (s *session) writeControlFrame(cmd byte, sid uint32, data []byte) error {
	if len(data) > maxFramePayload {
		return errors.New("anytls: control frame too large: ", len(data))
	}
	frameBuf := buf.NewWithSize(int32(frameOverhead + len(data)))
	putFrameHeader(frameBuf.Extend(frameOverhead), cmd, sid, len(data))
	if len(data) > 0 {
		_, _ = frameBuf.Write(data)
	}
	deadline := time.Now().Add(controlFrameWriteTimeout)
	if err := s.lockWriteDeadline(deadline); err != nil {
		frameBuf.Release()
		return err
	}
	_ = s.conn.SetWriteDeadline(deadline)
	err := s.writeBufferLocked(frameBuf, s.isClient)
	if err == nil {
		_ = s.conn.SetWriteDeadline(time.Time{})
	}
	s.unlockWrite()
	if err != nil {
		s.close(err)
	}
	return err
}

func (s *session) writeStreamData(sid uint32, mb buf.MultiBuffer, applyPadding bool) error {
	defer buf.ReleaseMulti(mb)
	totalLen := int(mb.Len())
	if totalLen == 0 {
		return nil
	}

	frameCount := (totalLen + maxFramePayload - 1) / maxFramePayload
	out := buf.NewWithSize(int32(totalLen + frameCount*frameOverhead))

	// Pack into <= 65535-byte cmdPSH frames without extra MultiBuffer splits.
	hdrPos := int32(-1)
	chunkLen := 0
	beginFrame := func() {
		hdrPos = out.Len()
		_ = out.Extend(frameOverhead)
		chunkLen = 0
	}
	finishFrame := func() {
		if hdrPos >= 0 {
			putFrameHeader(out.BytesRange(hdrPos, hdrPos+frameOverhead), cmdPSH, sid, chunkLen)
			hdrPos = -1
			chunkLen = 0
		}
	}

	for _, b := range mb {
		if b == nil || b.IsEmpty() {
			continue
		}
		data := b.Bytes()
		for len(data) > 0 {
			if hdrPos < 0 {
				beginFrame()
			}
			space := maxFramePayload - chunkLen
			if space == 0 {
				finishFrame()
				beginFrame()
				space = maxFramePayload
			}
			n := min(len(data), space)
			_, _ = out.Write(data[:n])
			chunkLen += n
			data = data[n:]
		}
	}
	finishFrame()

	if err := s.lockWriteContext(s.doneContext()); err != nil {
		out.Release()
		return err
	}
	err := s.writeBufferLocked(out, applyPadding)
	s.unlockWrite()
	if err != nil {
		s.close(err)
	}
	return err
}

func (s *session) doneContext() context.Context {
	return doneChanContext{ch: s.done}
}

type doneChanContext struct {
	ch <-chan struct{}
}

func (doneChanContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (d doneChanContext) Done() <-chan struct{}     { return d.ch }
func (doneChanContext) Err() error                  { return net.ErrClosed }
func (doneChanContext) Value(any) any               { return nil }

// writeBufferLocked takes ownership of b and releases it before returning.
func (s *session) writeBufferLocked(b *buf.Buffer, applyPadding bool) error {
	defer b.Release()

	if s.pending != nil {
		pending := s.pending
		s.pending = nil
		combined := buf.NewWithSize(pending.Len() + b.Len())
		_, _ = combined.Write(pending.Bytes())
		_, _ = combined.Write(b.Bytes())
		pending.Release()
		err := s.writeRawWithPaddingLocked(combined.Bytes(), applyPadding)
		combined.Release()
		return err
	}

	return s.writeRawWithPaddingLocked(b.Bytes(), applyPadding)
}

func (s *session) writeRawWithPaddingLocked(data []byte, applyPadding bool) error {
	if !applyPadding || !s.sendPadding.Load() {
		return writeAll(s.conn, data)
	}
	s.pktCounter++
	scheme := s.padding.Load()
	if scheme == nil || s.pktCounter >= scheme.stop {
		s.sendPadding.Store(false)
		return writeAll(s.conn, data)
	}

	pktSizes := scheme.generateRecordPayloadSizes(s.pktCounter)
	if len(pktSizes) == 0 {
		return writeAll(s.conn, data)
	}

	for _, targetSize := range pktSizes {
		if targetSize == paddingCheckMark {
			if len(data) == 0 {
				return nil
			}
			continue
		}
		if len(data) > targetSize {
			if err := writeAll(s.conn, data[:targetSize]); err != nil {
				return err
			}
			data = data[targetSize:]
			continue
		}

		if len(data) > 0 {
			wasteCount := max(1, (targetSize+maxFramePayload-1)/maxFramePayload)
			rec := buf.NewWithSize(int32(targetSize + wasteCount*frameOverhead))
			_, _ = rec.Write(data)
			data = nil
			for rem := targetSize - int(rec.Len()); rem > frameOverhead; {
				wasteLen := min(rem-frameOverhead, maxFramePayload)
				putFrameHeader(rec.Extend(frameOverhead), cmdWaste, 0, wasteLen)
				_ = rec.Extend(int32(wasteLen))
				rem -= frameOverhead + wasteLen
			}
			err := writeAll(s.conn, rec.Bytes())
			rec.Release()
			if err != nil {
				return err
			}
		} else {
			wasteCount := max(1, (targetSize+maxFramePayload-1)/maxFramePayload)
			rec := buf.NewWithSize(int32(targetSize + wasteCount*frameOverhead))
			for rem := targetSize; rem > 0; {
				wasteLen := min(rem, maxFramePayload)
				putFrameHeader(rec.Extend(frameOverhead), cmdWaste, 0, wasteLen)
				_ = rec.Extend(int32(wasteLen))
				rem -= wasteLen
			}
			err := writeAll(s.conn, rec.Bytes())
			rec.Release()
			if err != nil {
				return err
			}
		}
	}

	if len(data) > 0 {
		return writeAll(s.conn, data)
	}
	return nil
}

func (s *session) openStream(ctx context.Context, dest xnet.Destination, dieHook func()) (*stream, error) {
	if s.isClosed() {
		return nil, net.ErrClosed
	}

	sid := s.nextSID.Add(1)
	st := newStream(context.Background(), sid, s)
	st.dieHook = dieHook

	s.streamsMu.Lock()
	if s.isClosed() {
		s.streamsMu.Unlock()
		st.closeWithError(net.ErrClosed, false)
		return nil, net.ErrClosed
	}
	s.streams[sid] = st
	s.activeStreams.Add(1)
	s.streamsMu.Unlock()

	addrLen := socksAddrLen(dest)
	if err := s.lockWriteContext(ctx); err != nil {
		s.removeStream(sid)
		st.closeWithError(err, false)
		return nil, err
	}

	s.armOpenTimeout(sid)
	var err error
	if s.pending != nil || !s.sendPadding.Load() {
		// On stream 1 (with buffered cmdSettings) or after padding stops,
		// coalesce cmdSYN and cmdPSH(SocksAddr) into one TLS record.
		reqBuf := buf.NewWithSize(int32(frameOverhead*2 + addrLen))
		putFrameHeader(reqBuf.Extend(frameOverhead), cmdSYN, sid, 0)
		putFrameHeader(reqBuf.Extend(frameOverhead), cmdPSH, sid, addrLen)
		if werr := writeSocksAddr(reqBuf, dest); werr != nil {
			reqBuf.Release()
			s.unlockWrite()
			s.removeStream(sid)
			st.closeWithError(werr, false)
			return nil, werr
		}
		err = s.writeBufferLocked(reqBuf, true)
	} else {
		// During padding window on stream 2+, emit cmdSYN and cmdPSH(SocksAddr) as separate records.
		synBuf := buf.NewWithSize(frameOverhead)
		putFrameHeader(synBuf.Extend(frameOverhead), cmdSYN, sid, 0)
		err = s.writeBufferLocked(synBuf, true)
		if err == nil {
			addrBuf := buf.NewWithSize(int32(frameOverhead + addrLen))
			putFrameHeader(addrBuf.Extend(frameOverhead), cmdPSH, sid, addrLen)
			if werr := writeSocksAddr(addrBuf, dest); werr != nil {
				addrBuf.Release()
				err = werr
			} else {
				err = s.writeBufferLocked(addrBuf, true)
			}
		}
	}
	s.unlockWrite()

	if err != nil {
		s.removeStream(sid)
		st.closeWithError(err, false)
		s.close(err)
		return nil, err
	}

	return st, nil
}

func (s *session) armOpenTimeout(sid uint32) {
	if !s.isClient || sid < 2 || s.peerVersion.Load() < protocolVersion {
		return
	}
	s.timeoutMu.Lock()
	if s.openTimeout == nil {
		s.openTimeout = time.AfterFunc(streamOpenTimeout, func() {
			s.close(errors.New("anytls: stream open timeout"))
		})
	} else {
		s.openTimeout.Reset(streamOpenTimeout)
	}
	s.openTimeoutArmed.Store(true)
	s.timeoutMu.Unlock()
}

func (s *session) disarmOpenTimeout() {
	if !s.openTimeoutArmed.Load() {
		return
	}
	s.timeoutMu.Lock()
	if s.openTimeout != nil {
		s.openTimeout.Stop()
	}
	s.openTimeoutArmed.Store(false)
	s.timeoutMu.Unlock()
}

func (s *session) readPayloadBuffer(length int) (*buf.Buffer, error) {
	b := buf.NewWithSize(int32(length))
	if _, err := b.ReadFullFrom(s.br, int32(length)); err != nil {
		b.Release()
		return nil, err
	}
	return b, nil
}

func (s *session) readPayloadMultiBuffer(length int) (buf.MultiBuffer, error) {
	if length <= buf.Size {
		b := buf.New()
		if _, err := b.ReadFullFrom(s.br, int32(length)); err != nil {
			b.Release()
			return nil, err
		}
		return buf.MultiBuffer{b}, nil
	}

	var mb buf.MultiBuffer
	remaining := length
	for remaining > 0 {
		chunk := min(remaining, buf.Size)
		b := buf.New()
		if _, err := b.ReadFullFrom(s.br, int32(chunk)); err != nil {
			b.Release()
			buf.ReleaseMulti(mb)
			return nil, err
		}
		mb = append(mb, b)
		remaining -= chunk
	}
	return mb, nil
}

func (s *session) readLoop(ctx context.Context) error {
	var head [frameOverhead]byte
	for {
		if _, err := io.ReadFull(s.br, head[:]); err != nil {
			if s.isClosed() {
				return nil
			}
			return err
		}
		if !s.isClient {
			s.lastReadNano.Store(time.Now().UnixNano())
		} else if s.openTimeoutArmed.Load() {
			s.disarmOpenTimeout()
		}

		cmd := head[0]
		sid := binary.BigEndian.Uint32(head[1:5])
		length := int(binary.BigEndian.Uint16(head[5:7]))

		var err error
		switch cmd {
		case cmdPSH:
			err = s.handlePSH(sid, length)
		case cmdSYN:
			err = s.handleSYN(ctx, sid, length)
		case cmdFIN:
			err = s.handleFIN(sid, length)
		case cmdSYNACK:
			err = s.handleSYNACK(sid, length)
		case cmdSettings:
			err = s.handleSettings(length)
		case cmdServerSettings:
			err = s.handleServerSettings(length)
		case cmdUpdatePaddingScheme:
			err = s.handleUpdatePaddingScheme(length)
		case cmdAlert:
			err = s.handleAlert(length)
		case cmdHeartRequest:
			if length > 0 {
				_, err = s.br.Discard(length)
			}
			if err == nil {
				err = s.writeControlFrame(cmdHeartResponse, sid, nil)
			}
		case cmdHeartResponse:
			if length > 0 {
				_, err = s.br.Discard(length)
			}
		case cmdWaste:
			if length > 0 {
				_, err = s.br.Discard(length)
			}
		default:
			if length > 0 {
				_, err = s.br.Discard(length)
			}
		}
		if err != nil {
			return err
		}
	}
}

func (s *session) handlePSH(sid uint32, length int) error {
	if length == 0 {
		return nil
	}
	s.streamsMu.RLock()
	st := s.streams[sid]
	s.streamsMu.RUnlock()
	if st == nil {
		_, err := s.br.Discard(length)
		return err
	}
	mb, err := s.readPayloadMultiBuffer(length)
	if err != nil {
		return err
	}
	// If this individual stream already closed, drop its in-flight data without closing the session.
	_ = st.pushFromPeer(mb)
	return nil
}

func (s *session) handleSYN(ctx context.Context, sid uint32, length int) error {
	if s.isClient {
		if length > 0 {
			_, err := s.br.Discard(length)
			return err
		}
		return errors.New("anytls: unexpected cmdSYN from server")
	}
	if !s.settingsReceived {
		_ = s.writeControlFrame(cmdAlert, 0, []byte("client did not send its settings"))
		return errors.New("anytls: client did not send its settings")
	}
	if length > 0 {
		if _, err := s.br.Discard(length); err != nil {
			return err
		}
		if s.peerVersion.Load() >= protocolVersion {
			_ = s.writeControlFrame(cmdSYNACK, sid, []byte("unexpected syn payload"))
		}
		return nil
	}
	if sid == 0 {
		if s.peerVersion.Load() >= protocolVersion {
			_ = s.writeControlFrame(cmdSYNACK, sid, []byte("invalid stream id"))
		}
		return nil
	}

	s.streamsMu.Lock()
	if s.isClosed() {
		s.streamsMu.Unlock()
		return net.ErrClosed
	}
	if _, exists := s.streams[sid]; exists {
		s.streamsMu.Unlock()
		return nil
	}
	streamCtx := subContextFromMuxInbound(ctx)
	st := newStream(streamCtx, sid, s)
	s.streams[sid] = st
	s.streamsMu.Unlock()

	go s.handleServerStream(st)
	return nil
}

func (s *session) handleFIN(sid uint32, length int) error {
	if length > 0 {
		if _, err := s.br.Discard(length); err != nil {
			return err
		}
	}
	s.streamsMu.Lock()
	st := s.streams[sid]
	if st != nil {
		delete(s.streams, sid)
		if s.isClient {
			s.activeStreams.Add(-1)
		}
	}
	s.streamsMu.Unlock()
	if st != nil {
		st.closeByPeer()
	}
	return nil
}

func (s *session) handleSYNACK(sid uint32, length int) error {
	if !s.isClient {
		if length > 0 {
			_, err := s.br.Discard(length)
			return err
		}
		return errors.New("anytls: unexpected cmdSYNACK from client")
	}
	if length == 0 {
		return nil
	}
	b, err := s.readPayloadBuffer(length)
	if err != nil {
		return err
	}
	msg := b.String()
	b.Release()

	s.streamsMu.Lock()
	st := s.streams[sid]
	if st != nil {
		delete(s.streams, sid)
		s.activeStreams.Add(-1)
	}
	s.streamsMu.Unlock()
	if st != nil {
		st.closeWithError(errors.New("anytls: remote: ", msg), false)
	}
	return nil
}

func (s *session) handleSettings(length int) error {
	if s.isClient {
		if length > 0 {
			_, err := s.br.Discard(length)
			return err
		}
		return errors.New("anytls: unexpected cmdSettings from server")
	}
	var peerSettings map[string]string
	if length > 0 {
		b, err := s.readPayloadBuffer(length)
		if err != nil {
			return err
		}
		peerSettings = parseSettings(b.Bytes())
		b.Release()
	}
	s.settingsReceived = true

	scheme := s.padding.Load()
	if scheme != nil && len(scheme.rawScheme) <= maxFramePayload {
		clientMD5 := peerSettings[settingPaddingMD5]
		if clientMD5 != scheme.md5Hex {
			if err := s.writeControlFrame(cmdUpdatePaddingScheme, 0, scheme.rawScheme); err != nil {
				return err
			}
		}
	}
	if v, err := strconv.Atoi(peerSettings[settingVersion]); err == nil && v >= protocolVersion {
		s.peerVersion.Store(uint32(v))
		serverSettings := encodeSettings(map[string]string{
			settingVersion: strconv.Itoa(protocolVersion),
		})
		if err := s.writeControlFrame(cmdServerSettings, 0, serverSettings); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) handleServerSettings(length int) error {
	if !s.isClient {
		if length > 0 {
			_, err := s.br.Discard(length)
			return err
		}
		return errors.New("anytls: unexpected cmdServerSettings from client")
	}
	if length == 0 {
		return nil
	}
	b, err := s.readPayloadBuffer(length)
	if err != nil {
		return err
	}
	kv := parseSettings(b.Bytes())
	b.Release()
	if v, err := strconv.Atoi(kv[settingVersion]); err == nil && v > 0 {
		s.peerVersion.Store(uint32(v))
	}
	return nil
}

func (s *session) handleUpdatePaddingScheme(length int) error {
	if !s.isClient || length == 0 {
		if length > 0 {
			_, err := s.br.Discard(length)
			return err
		}
		return nil
	}
	b, err := s.readPayloadBuffer(length)
	if err != nil {
		return err
	}
	raw := make([]byte, b.Len())
	copy(raw, b.Bytes())
	b.Release()

	scheme, perr := parsePaddingScheme(raw)
	if perr != nil {
		errors.LogWarningInner(context.Background(), perr, "anytls: ignored invalid server padding scheme")
		return nil
	}
	// Store on Client's atomic padding pointer so all subsequent sessions use it.
	s.padding.Store(scheme)
	errors.LogDebug(context.Background(), "anytls: padding scheme updated md5=", scheme.md5Hex)
	return nil
}

func (s *session) handleAlert(length int) error {
	var msg string
	if length > 0 {
		b, err := s.readPayloadBuffer(length)
		if err != nil {
			return err
		}
		msg = b.String()
		b.Release()
	}
	if !s.isClient {
		return errors.New("anytls: unexpected cmdAlert from client")
	}
	if msg != "" {
		return errors.New("anytls: server alert: ", msg)
	}
	return errors.New("anytls: server alert")
}

func (s *session) handleServerStream(st *stream) {
	defer func() {
		s.removeStream(st.id)
		_ = st.Close()
	}()

	sessPolicy := s.policyManager.ForLevel(s.userLevel)
	handshakeTimeout := sessPolicy.Timeouts.Handshake
	if handshakeTimeout <= 0 {
		handshakeTimeout = 60 * time.Second
	}

	handshakeTimer := time.AfterFunc(handshakeTimeout, func() {
		st.closeWithError(errors.New("anytls: stream handshake timeout"), true)
	})

	br := st.bufferedReader()
	dest, err := readSocksAddr(br, xnet.Network_TCP, false)
	if err != nil {
		handshakeTimer.Stop()
		st.handshakeFailure(err)
		return
	}

	dispatchCtx := st.ctx
	if dest.Address.Family().IsDomain() {
		domain := dest.Address.Domain()
		if domain == uotMagicAddress || domain == uotLegacyMagicAddress {
			st.handshakeSuccess()

			isV2 := domain == uotMagicAddress
			var uotReq uotRequest
			if isV2 {
				uotReq, err = readUoTRequest(br)
				if err != nil {
					handshakeTimer.Stop()
					return
				}
			}

			reader := newUoTReader(br, uotReq.isConnect, uotReq.destination)
			target := uotReq.destination
			if !target.IsValid() || target.Port == 0 {
				var first buf.MultiBuffer
				first, err = reader.ReadMultiBuffer()
				if err != nil || first.IsEmpty() || first[0].UDP == nil {
					handshakeTimer.Stop()
					buf.ReleaseMulti(first)
					return
				}
				target = *first[0].UDP
				reader.setFirst(first)
			}
			handshakeTimer.Stop()

			if !target.IsValid() || target.Port == 0 {
				return
			}

			dispatchCtx = log.ContextWithAccessMessage(dispatchCtx, &log.AccessMessage{
				From:   s.conn.RemoteAddr(),
				To:     target,
				Status: log.AccessAccepted,
				Email:  s.userEmail,
			})
			errors.LogInfo(dispatchCtx, "tunnelling UoT request to ", target)

			writer := newUoTWriter(st, uotReq.isConnect, target)
			if err := s.dispatcher.DispatchLink(dispatchCtx, target, &transport.Link{Reader: reader, Writer: writer}); err != nil {
				errors.LogInfoInner(dispatchCtx, err, "anytls: UoT dispatch failed")
			}
			return
		}
	}

	handshakeTimer.Stop()
	if !dest.IsValid() || dest.Port == 0 {
		st.handshakeFailure(errors.New("anytls: invalid destination"))
		return
	}

	st.handshakeSuccess()

	dispatchCtx = log.ContextWithAccessMessage(dispatchCtx, &log.AccessMessage{
		From:   s.conn.RemoteAddr(),
		To:     dest,
		Status: log.AccessAccepted,
		Email:  s.userEmail,
	})
	errors.LogInfo(dispatchCtx, "tunnelling request to ", dest)

	var reader buf.TimeoutReader = st
	if br.BufferedBytes() > 0 {
		reader = newBufferedTimeoutReader(br, st)
	}
	if err := s.dispatcher.DispatchLink(dispatchCtx, dest, &transport.Link{Reader: reader, Writer: st}); err != nil {
		errors.LogInfoInner(dispatchCtx, err, "anytls: TCP dispatch failed")
	}
}

func (s *session) watchServerIdle(ctx context.Context, stop <-chan struct{}) {
	idleTimeout := s.connectionIdle
	if idleTimeout <= 0 {
		idleTimeout = 300 * time.Second
	}
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	var probeReadNano int64
	var sendDone chan error

	for {
		select {
		case <-stop:
			return
		case <-s.done:
			return
		case <-ctx.Done():
			s.close(ctx.Err())
			return
		case err := <-sendDone:
			sendDone = nil
			if err != nil {
				s.close(err)
				return
			}
		case <-timer.C:
			lastReadNano := s.lastReadNano.Load()
			if probeReadNano != 0 && lastReadNano == probeReadNano {
				s.close(errors.New("anytls: heartbeat timeout"))
				return
			}
			probeReadNano = 0

			elapsed := time.Since(time.Unix(0, lastReadNano))
			if remaining := idleTimeout - elapsed; remaining > 0 {
				timer.Reset(remaining)
				continue
			}

			if s.peerVersion.Load() < protocolVersion {
				// Protocol v1 does not support heartbeats; close only when no streams are active.
				s.streamsMu.RLock()
				nStreams := len(s.streams)
				s.streamsMu.RUnlock()
				if nStreams == 0 {
					s.close(errors.New("anytls: idle session timeout"))
					return
				}
				timer.Reset(idleTimeout)
				continue
			}

			probeReadNano = lastReadNano
			timer.Reset(serverHeartbeatTimeout)
			if sendDone == nil {
				sendDone = make(chan error, 1)
				go func(ch chan<- error) {
					ch <- s.writeControlFrame(cmdHeartRequest, 0, nil)
				}(sendDone)
			}
		}
	}
}

func subContextFromMuxInbound(ctx context.Context) context.Context {
	newOutbounds := []*xsession.Outbound{{}}
	content := xsession.ContentFromContext(ctx)
	newContent := xsession.Content{}
	if content != nil {
		newContent = *content
		if content.Attributes != nil {
			newContent.Attributes = make(map[string]string, len(content.Attributes))
			for k, v := range content.Attributes {
				newContent.Attributes[k] = v
			}
		}
	}
	return xsession.ContextWithContent(xsession.ContextWithOutbounds(ctx, newOutbounds), &newContent)
}

type bufferedTimeoutReader struct {
	mu     sync.Mutex
	br     *buf.BufferedReader
	source buf.TimeoutReader
}

func newBufferedTimeoutReader(br *buf.BufferedReader, source buf.TimeoutReader) *bufferedTimeoutReader {
	return &bufferedTimeoutReader{br: br, source: source}
}

func (r *bufferedTimeoutReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	r.mu.Lock()
	if !r.br.Buffer.IsEmpty() {
		mb := r.br.Buffer
		r.br.Buffer = nil
		r.mu.Unlock()
		return mb, nil
	}
	r.mu.Unlock()
	return r.source.ReadMultiBuffer()
}

func (r *bufferedTimeoutReader) ReadMultiBufferTimeout(d time.Duration) (buf.MultiBuffer, error) {
	r.mu.Lock()
	if !r.br.Buffer.IsEmpty() {
		mb := r.br.Buffer
		r.br.Buffer = nil
		r.mu.Unlock()
		return mb, nil
	}
	r.mu.Unlock()
	return r.source.ReadMultiBufferTimeout(d)
}

func (r *bufferedTimeoutReader) Interrupt() {
	r.mu.Lock()
	if !r.br.Buffer.IsEmpty() {
		r.br.Buffer = buf.ReleaseMulti(r.br.Buffer)
	}
	r.mu.Unlock()
	if i, ok := r.source.(interface{ Interrupt() }); ok {
		i.Interrupt()
	}
}
