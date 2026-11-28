package anytls

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	stdnet "net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	sessionctx "github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/pipe"
)

func TestPaddingSchemeValidationAndGeneration(t *testing.T) {
	if _, err := parsePaddingScheme(nil); err == nil {
		t.Fatal("expected error for empty padding scheme")
	}
	if _, err := parsePaddingScheme([]byte("stop=-1\n0=30-30")); err == nil {
		t.Fatal("expected error for negative stop")
	}
	if _, err := parsePaddingScheme([]byte("stop=999999\n0=30-30")); err == nil {
		t.Fatal("expected error for excessive stop")
	}

	ps, err := parsePaddingScheme([]byte("stop=4\r\n0=30-30\r\n1=100-200\r\n2=300-400,c,500-600"))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ps.stop != 4 {
		t.Fatalf("stop = %d, want 4", ps.stop)
	}
	if ps.handshakePaddingSize() != 30 {
		t.Fatalf("handshakePaddingSize = %d, want 30", ps.handshakePaddingSize())
	}
	for i := 0; i < 20; i++ {
		sizes := ps.generateRecordPayloadSizes(2)
		if len(sizes) != 3 || sizes[1] != paddingCheckMark {
			t.Fatalf("unexpected sizes: %v", sizes)
		}
		if sizes[0] < 300 || sizes[0] >= 400 || sizes[2] < 500 || sizes[2] >= 600 {
			t.Fatalf("out of range sizes: %v", sizes)
		}
	}
}

func TestSocksAndUoTCodecs(t *testing.T) {
	dests := []xnet.Destination{
		xnet.TCPDestination(xnet.IPAddress([]byte{1, 2, 3, 4}), 443),
		xnet.TCPDestination(xnet.ParseAddress("2001:db8::1"), 8443),
		xnet.TCPDestination(xnet.DomainAddress("example.com"), 80),
	}
	for _, d := range dests {
		b := buf.New()
		if err := writeSocksAddr(b, d); err != nil {
			t.Fatal(err)
		}
		got, err := readSocksAddr(bytes.NewReader(b.Bytes()), xnet.Network_TCP, false)
		b.Release()
		if err != nil {
			t.Fatal(err)
		}
		if got != d {
			t.Fatalf("SocksAddr roundtrip mismatch: got %v want %v", got, d)
		}
	}

	// Empty domain length must return error cleanly instead of panicking
	if _, err := readSocksAddr(bytes.NewReader([]byte{socksAtypDomain, 0, 0, 80}), xnet.Network_TCP, false); err == nil {
		t.Fatal("expected error for zero-length domain")
	}

	udpDests := []xnet.Destination{
		xnet.UDPDestination(xnet.IPAddress([]byte{8, 8, 8, 8}), 53),
		xnet.UDPDestination(xnet.ParseAddress("2001:4860:4860::8888"), 53),
		xnet.UDPDestination(xnet.DomainAddress("dns.google"), 53),
	}
	for _, d := range udpDests {
		b := buf.New()
		if err := writeUoTAddr(b, d); err != nil {
			t.Fatal(err)
		}
		got, err := readUoTAddr(bytes.NewReader(b.Bytes()))
		b.Release()
		if err != nil {
			t.Fatal(err)
		}
		if got != d {
			t.Fatalf("UoTAddr roundtrip mismatch: got %v want %v", got, d)
		}
	}
}

func TestValidatorUserManager(t *testing.T) {
	v := NewValidator()
	u1 := &protocol.MemoryUser{Email: "User1@Example.com", Level: 1, Account: &MemoryAccount{Password: "pw1"}}
	u2 := &protocol.MemoryUser{Email: "", Level: 0, Account: &MemoryAccount{Password: "pw2"}}
	if err := v.Add(u1); err != nil {
		t.Fatal(err)
	}
	if err := v.Add(u2); err != nil {
		t.Fatal(err)
	}
	if v.GetCount() != 2 {
		t.Fatalf("count = %d, want 2", v.GetCount())
	}
	if v.GetByEmail("user1@example.com") != u1 {
		t.Fatal("GetByEmail failed case-insensitive lookup")
	}
	if v.GetByHash(sha256.Sum256([]byte("pw2"))) != u2 {
		t.Fatal("GetByHash failed for empty-email user")
	}
	if len(v.GetAll()) != 2 {
		t.Fatalf("GetAll len = %d, want 2", len(v.GetAll()))
	}
	// Duplicate email and duplicate password rejected
	if err := v.Add(&protocol.MemoryUser{Email: "user1@example.com", Account: &MemoryAccount{Password: "pw3"}}); err == nil {
		t.Fatal("expected duplicate email error")
	}
	if err := v.Add(&protocol.MemoryUser{Email: "user3@example.com", Account: &MemoryAccount{Password: "pw1"}}); err == nil {
		t.Fatal("expected duplicate password error")
	}
	if err := v.Del("user1@example.com"); err != nil {
		t.Fatal(err)
	}
	if v.GetByHash(sha256.Sum256([]byte("pw1"))) != nil {
		t.Fatal("deleted user still matched by hash")
	}
	if v.GetCount() != 1 {
		t.Fatalf("count = %d, want 1", v.GetCount())
	}
}

type mockEchoDispatcher struct {
	mu          sync.Mutex
	udpObserved []xnet.Destination
}

func (*mockEchoDispatcher) Type() interface{} { return routing.DispatcherType() }
func (*mockEchoDispatcher) Start() error      { return nil }
func (*mockEchoDispatcher) Close() error      { return nil }

func (*mockEchoDispatcher) Dispatch(ctx context.Context, dest xnet.Destination) (*transport.Link, error) {
	panic("not used")
}

func (d *mockEchoDispatcher) DispatchLink(ctx context.Context, dest xnet.Destination, link *transport.Link) error {
	defer common.Close(link.Writer)
	defer common.Interrupt(link.Reader)

	if dest.Network == xnet.Network_UDP {
		for {
			mb, err := link.Reader.ReadMultiBuffer()
			if err != nil {
				return nil
			}
			for _, b := range mb {
				target := dest
				if b.UDP != nil {
					target = *b.UDP
				}
				d.mu.Lock()
				d.udpObserved = append(d.udpObserved, target)
				d.mu.Unlock()
			}
			if err := link.Writer.WriteMultiBuffer(mb); err != nil {
				return nil
			}
		}
	}

	// Echo TCP stream back to client
	return buf.Copy(link.Reader, link.Writer)
}

type pipeDialer struct {
	server     *Server
	dispatcher routing.Dispatcher
	dials      atomic.Int32
	active     atomic.Int32
	chunkOne   bool
}

type oneByteConn struct {
	stdnet.Conn
}

func (c oneByteConn) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return c.Conn.Read(p)
}

func (d *pipeDialer) Dial(ctx context.Context, dest xnet.Destination) (stat.Connection, error) {
	c1, c2 := stdnet.Pipe()
	d.dials.Add(1)
	d.active.Add(1)

	go func() {
		defer d.active.Add(-1)
		defer c2.Close()
		sCtx := sessionctx.ContextWithInbound(context.Background(), &sessionctx.Inbound{
			Source: xnet.TCPDestination(xnet.LocalHostIP, 12345),
		})
		sCtx = sessionctx.ContextWithOutbounds(sCtx, []*sessionctx.Outbound{{}})
		sCtx = sessionctx.ContextWithContent(sCtx, &sessionctx.Content{})
		var sc stat.Connection = c2
		if d.chunkOne {
			sc = oneByteConn{Conn: c2}
		}
		_ = d.server.Process(sCtx, xnet.Network_TCP, sc, d.dispatcher)
	}()

	return c1, nil
}
func (*pipeDialer) DestIpAddress() xnet.IP                                   { return nil }
func (*pipeDialer) SetOutboundGateway(context.Context, *sessionctx.Outbound) {}

type testPolicyManager struct{}

func (testPolicyManager) Type() interface{}        { return policy.ManagerType() }
func (testPolicyManager) Start() error             { return nil }
func (testPolicyManager) Close() error             { return nil }
func (testPolicyManager) ForSystem() policy.System { return policy.System{} }
func (testPolicyManager) ForLevel(uint32) policy.Session {
	p := policy.SessionDefault()
	p.Timeouts.Handshake = 3 * time.Second
	p.Timeouts.ConnectionIdle = 30 * time.Second
	p.Timeouts.UplinkOnly = 500 * time.Millisecond
	p.Timeouts.DownlinkOnly = 500 * time.Millisecond
	return p
}

func newUnitPair(t *testing.T, clientPassword string, serverPadding []byte, fallbacks []*Fallback) (*Client, *Server, *pipeDialer, *mockEchoDispatcher) {
	t.Helper()
	v := NewValidator()
	common.Must(v.Add(&protocol.MemoryUser{
		Email:   "test@example.com",
		Level:   0,
		Account: &MemoryAccount{Password: "test-password"},
	}))
	scheme := defaultPaddingScheme
	if len(serverPadding) > 0 {
		var err error
		scheme, err = parsePaddingScheme(serverPadding)
		if err != nil {
			t.Fatal(err)
		}
	}
	srv := &Server{
		policyManager: testPolicyManager{},
		validator:     v,
	}
	srv.padding.Store(scheme)
	if len(fallbacks) > 0 {
		srv.fallbacks = buildFallbackTable(fallbacks)
	}

	cli := &Client{
		server: &protocol.ServerSpec{
			Destination: xnet.TCPDestination(xnet.LocalHostIP, 443),
			User: &protocol.MemoryUser{
				Account: &MemoryAccount{Password: clientPassword},
			},
		},
		policyManager:            testPolicyManager{},
		authHash:                 sha256.Sum256([]byte(clientPassword)),
		idleSessionCheckInterval: defaultIdleSessionCheckInterval,
		idleSessionTimeout:       defaultIdleSessionTimeout,
		sessions:                 make(map[uint64]*session),
	}
	cli.padding.Store(defaultPaddingScheme)
	cli.ctx, cli.cancel = context.WithCancel(context.Background())

	disp := &mockEchoDispatcher{}
	dialer := &pipeDialer{server: srv, dispatcher: disp}

	t.Cleanup(func() {
		cli.Close()
		deadline := time.Now().Add(3 * time.Second)
		for dialer.active.Load() != 0 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		if dialer.active.Load() != 0 {
			t.Errorf("server connection leaked: active=%d", dialer.active.Load())
		}
	})

	return cli, srv, dialer, disp
}

func roundtripTCPAndUDP(t *testing.T, cli *Client, dialer *pipeDialer, udp bool) {
	t.Helper()
	target := xnet.TCPDestination(xnet.DomainAddress("echo.example"), 443)
	if udp {
		target = xnet.UDPDestination(xnet.DomainAddress("dns.example"), 53)
	}
	ctx, cancel := context.WithCancel(sessionctx.ContextWithOutbounds(context.Background(), []*sessionctx.Outbound{{Target: target}}))
	uplink, input := pipe.New()
	output, downlink := pipe.New()

	done := make(chan error, 1)
	go func() {
		done <- cli.Process(ctx, &transport.Link{Reader: uplink, Writer: downlink}, dialer)
	}()
	defer func() {
		cancel()
		input.Close()
		output.Interrupt()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Client.Process did not exit promptly")
		}
	}()

	// Exercise both small payload and > 64KB multi-frame payload for TCP (or > 8KB for UDP)
	sizes := []int{19, 70000}
	if udp {
		sizes = []int{24, 9000}
	}
	for i, sz := range sizes {
		payload := bytes.Repeat([]byte{byte(i + 0x41)}, sz)
		b := buf.NewWithSize(int32(len(payload)))
		_, _ = b.Write(payload)
		addr := target
		if udp && i == 1 {
			addr = xnet.UDPDestination(xnet.ParseAddress("2001:db8::9"), 12345)
		}
		if udp {
			b.UDP = &addr
		}
		if err := input.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
			t.Fatal(err)
		}

		received := make([]byte, 0, len(payload))
		for len(received) < len(payload) {
			mb, err := output.ReadMultiBufferTimeout(3 * time.Second)
			if err != nil {
				t.Fatalf("read timeout/err at %d/%d: %v", len(received), len(payload), err)
			}
			for _, pkt := range mb {
				if udp && (pkt.UDP == nil || *pkt.UDP != addr) {
					buf.ReleaseMulti(mb)
					t.Fatalf("UDP addr mismatch: got %v want %v", pkt.UDP, addr)
				}
				received = append(received, pkt.Bytes()...)
			}
			buf.ReleaseMulti(mb)
		}
		if !bytes.Equal(received, payload) {
			t.Fatalf("payload mismatch for size %d", sz)
		}
	}
}

func TestAnyTLSTCPAndUDPRoundtripAndReuse(t *testing.T) {
	customPadding := []byte("stop=4\n0=16-16\n1=128-256\n2=256-512,c,512-1024\n3=256-512")
	cli, _, dialer, disp := newUnitPair(t, "test-password", customPadding, nil)

	for i := 0; i < 5; i++ {
		roundtripTCPAndUDP(t, cli, dialer, false)
	}
	roundtripTCPAndUDP(t, cli, dialer, true)

	if dials := dialer.dials.Load(); dials != 1 {
		t.Fatalf("expected 1 pooled session across sequential requests, got %d dials", dials)
	}

	// Verify Client's padding scheme was updated from the server's cmdUpdatePaddingScheme
	if cli.padding.Load().md5Hex == defaultPaddingScheme.md5Hex {
		t.Fatal("client padding scheme was not updated by server")
	}

	disp.mu.Lock()
	defer disp.mu.Unlock()
	if len(disp.udpObserved) < 2 || disp.udpObserved[1] != xnet.UDPDestination(xnet.ParseAddress("2001:db8::9"), 12345) {
		t.Fatalf("full-cone UDP destination not preserved: %+v", disp.udpObserved)
	}
}

func TestAnyTLSWrongPasswordAndFragmentedPrologue(t *testing.T) {
	cli, _, dialer, _ := newUnitPair(t, "wrong-password", nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = sessionctx.ContextWithOutbounds(ctx, []*sessionctx.Outbound{{
		Target: xnet.TCPDestination(xnet.DomainAddress("echo.example"), 443),
	}})
	uplink, input := pipe.New()
	output, downlink := pipe.New()
	defer input.Close()
	defer output.Interrupt()

	done := make(chan error, 1)
	go func() {
		done <- cli.Process(ctx, &transport.Link{Reader: uplink, Writer: downlink}, dialer)
	}()

	_ = input.WriteMultiBuffer(buf.MultiBuffer{buf.FromBytes([]byte("secret"))})
	mb, err := output.ReadMultiBufferTimeout(2 * time.Second)
	buf.ReleaseMulti(mb)
	if err == nil && len(mb) > 0 {
		t.Fatal("wrong password should not receive echoed data")
	}

	// Now test fragmented 1-byte-at-a-time prologue with valid password
	cli2, _, dialer2, _ := newUnitPair(t, "test-password", nil, nil)
	dialer2.chunkOne = true
	roundtripTCPAndUDP(t, cli2, dialer2, false)
}

func TestAnyTLSFallbackHTTP(t *testing.T) {
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fallback-ok:" + r.URL.Path))
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	_, anySrv, _, _ := newUnitPair(t, "test-password", nil, []*Fallback{
		{Type: "tcp", Dest: ln.Addr().String()},
	})

	c1, c2 := stdnet.Pipe()
	defer c1.Close()

	errCh := make(chan error, 1)
	go func() {
		defer c2.Close()
		sCtx := sessionctx.ContextWithInbound(context.Background(), &sessionctx.Inbound{
			Source: xnet.TCPDestination(xnet.LocalHostIP, 50000),
		})
		sCtx = sessionctx.ContextWithOutbounds(sCtx, []*sessionctx.Outbound{{}})
		sCtx = sessionctx.ContextWithContent(sCtx, &sessionctx.Content{})
		errCh <- anySrv.Process(sCtx, xnet.Network_TCP, c2, &mockEchoDispatcher{})
	}()

	req := "GET /hello HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	if _, err := c1.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c1), nil)
	if err != nil {
		t.Fatalf("fallback HTTP response failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	_ = c1.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "fallback-ok:/hello" {
		t.Fatalf("unexpected fallback body: %q", string(body))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server fallback returned error: %v", err)
	}
}

func TestAnyTLSV1ClientCompat(t *testing.T) {
	// Simulate a protocol v1 client connecting directly to Server.
	_, anySrv, _, _ := newUnitPair(t, "test-password", nil, nil)
	c1, c2 := stdnet.Pipe()
	defer c1.Close()

	go func() {
		defer c2.Close()
		sCtx := sessionctx.ContextWithInbound(context.Background(), &sessionctx.Inbound{
			Source: xnet.TCPDestination(xnet.LocalHostIP, 50001),
		})
		sCtx = sessionctx.ContextWithOutbounds(sCtx, []*sessionctx.Outbound{{}})
		sCtx = sessionctx.ContextWithContent(sCtx, &sessionctx.Content{})
		_ = anySrv.Process(sCtx, xnet.Network_TCP, c2, &mockEchoDispatcher{})
	}()

	hash := sha256.Sum256([]byte("test-password"))
	var auth [34]byte
	copy(auth[:32], hash[:])
	binary.BigEndian.PutUint16(auth[32:], 0)
	if _, err := c1.Write(auth[:]); err != nil {
		t.Fatal(err)
	}

	// Send cmdSettings v=1
	settings := []byte(fmt.Sprintf("v=1\npadding-md5=%s", defaultPaddingScheme.md5Hex))
	var frame bytes.Buffer
	var hdr [frameOverhead]byte
	putFrameHeader(hdr[:], cmdSettings, 0, len(settings))
	frame.Write(hdr[:])
	frame.Write(settings)

	// Send cmdSYN(1) + cmdPSH(1, addr + "ping")
	putFrameHeader(hdr[:], cmdSYN, 1, 0)
	frame.Write(hdr[:])

	addrBuf := buf.New()
	common.Must(writeSocksAddr(addrBuf, xnet.TCPDestination(xnet.DomainAddress("v1.example"), 80)))
	addrBuf.Write([]byte("ping"))
	putFrameHeader(hdr[:], cmdPSH, 1, int(addrBuf.Len()))
	frame.Write(hdr[:])
	frame.Write(addrBuf.Bytes())
	addrBuf.Release()

	if _, err := c1.Write(frame.Bytes()); err != nil {
		t.Fatal(err)
	}

	// Server MUST NOT send cmdServerSettings or cmdSYNACK to a v1 client; first frame must be cmdPSH("ping")
	if _, err := io.ReadFull(c1, hdr[:]); err != nil {
		t.Fatal(err)
	}
	if hdr[0] != cmdPSH {
		t.Fatalf("expected cmdPSH for v1 client, got cmd=%d", hdr[0])
	}
	n := int(binary.BigEndian.Uint16(hdr[5:7]))
	got := make([]byte, n)
	if _, err := io.ReadFull(c1, got); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(string(got), "ping") {
		t.Fatalf("got %q, want ping", string(got))
	}
}
