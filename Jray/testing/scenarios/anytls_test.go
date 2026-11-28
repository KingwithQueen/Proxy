package scenarios

import (
	"bufio"
	"crypto/rand"
	gotls "crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"io"
	stdnet "net"
	"net/http"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common"
	clog "github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/protocol/tls/cert"
	"github.com/xtls/xray-core/common/serial"
	core "github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy/anytls"
	"github.com/xtls/xray-core/proxy/dokodemo"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/testing/servers/tcp"
	"github.com/xtls/xray-core/testing/servers/udp"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/reality"
	transtls "github.com/xtls/xray-core/transport/internet/tls"
	"golang.org/x/sync/errgroup"
)

func startCoreInstance(t *testing.T, cfg *core.Config) *core.Instance {
	t.Helper()
	inst, err := core.New(withDefaultApps(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close() })
	return inst
}

func testCertConfig() (*transtls.Config, *transtls.Config) {
	c, certHash := cert.MustGenerate(nil, cert.DNSNames("anytls.test"))
	xCert := transtls.ParseCertificate(c)
	srvTLS := &transtls.Config{
		Certificate: []*transtls.Certificate{xCert},
	}
	cliTLS := &transtls.Config{
		ServerName:           "anytls.test",
		PinnedPeerCertSha256: [][]byte{certHash[:]},
	}
	return srvTLS, cliTLS
}

func TestAnyTLSEndToEndTCPAndReuse(t *testing.T) {
	// Echo-and-close server for deterministic sequential session reuse checks
	reuseLn, err := stdnet.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	defer reuseLn.Close()
	go func() {
		for {
			c, err := reuseLn.Accept()
			if err != nil {
				return
			}
			go func(conn stdnet.Conn) {
				defer conn.Close()
				buf := make([]byte, 64)
				if _, err := io.ReadFull(conn, buf); err != nil {
					return
				}
				_, _ = conn.Write(xor(buf))
			}(c)
		}
	}()
	reuseAddr := reuseLn.Addr().(*stdnet.TCPAddr)

	tcpServer := tcp.Server{MsgProcessor: xor}
	dest, err := tcpServer.Start()
	common.Must(err)
	defer tcpServer.Close()

	srvTLS, cliTLS := testCertConfig()
	serverPort := tcp.PickPort()
	proxyPort, carrierCount := countTCPCarriers(t, serverPort)

	serverCfg := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&log.Config{ErrorLogLevel: clog.Severity_Warning, ErrorLogType: log.LogType_Console}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
					SniffingSettings: &proxyman.SniffingConfig{
						Enabled: true,
					},
					StreamSettings: &internet.StreamConfig{
						ProtocolName: "tcp",
						SecurityType: serial.GetMessageType(srvTLS),
						SecuritySettings: []*serial.TypedMessage{
							serial.ToTypedMessage(srvTLS),
						},
					},
				}),
				ProxySettings: serial.ToTypedMessage(&anytls.ServerConfig{
					Users: []*protocol.User{
						{
							Email: "alice@example.com",
							Account: serial.ToTypedMessage(&anytls.Account{
								Password: "strong-password-123",
							}),
						},
					},
					PaddingScheme: []byte("stop=4\n0=24-24\n1=120-200\n2=300-400,c,300-400\n3=200-300"),
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&freedom.Config{
					Firewall: []*freedom.FirewallConfig{{Action: freedom.RuleAction_Allow}},
				}),
			},
		},
	}
	startCoreInstance(t, serverCfg)

	reuseClientPort := tcp.PickPort()
	throughputClientPort := tcp.PickPort()
	clientCfg := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&log.Config{ErrorLogLevel: clog.Severity_Warning, ErrorLogType: log.LogType_Console}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(reuseClientPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(net.IPAddress(reuseAddr.IP)),
					RewritePort:     uint32(reuseAddr.Port),
					AllowedNetworks: []net.Network{net.Network_TCP},
				}),
			},
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(throughputClientPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(dest.Address),
					RewritePort:     uint32(dest.Port),
					AllowedNetworks: []net.Network{net.Network_TCP},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&anytls.ClientConfig{
					Server: &protocol.ServerEndpoint{
						Address: net.NewIPOrDomain(net.LocalHostIP),
						Port:    uint32(proxyPort),
						User: &protocol.User{
							Account: serial.ToTypedMessage(&anytls.Account{
								Password: "strong-password-123",
							}),
						},
					},
					IdleSessionCheckInterval: 30,
					IdleSessionTimeout:       60,
					MinIdleSession:           1,
				}),
				SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
					StreamSettings: &internet.StreamConfig{
						ProtocolName: "tcp",
						SecurityType: serial.GetMessageType(cliTLS),
						SecuritySettings: []*serial.TypedMessage{
							serial.ToTypedMessage(cliTLS),
						},
					},
				}),
			},
		},
	}
	startCoreInstance(t, clientCfg)

	// Run 5 sequential connections and verify carrier reuse once each stream completes its close handshake
	for i := 0; i < 5; i++ {
		if err := testTCPConn(reuseClientPort, 64, 5*time.Second)(); err != nil {
			t.Fatalf("sequential TCP #%d failed: %v", i, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := carrierCount.Load(); n != 1 {
		t.Fatalf("expected 1 reused carrier TLS connection for sequential requests, got %d", n)
	}

	// Run concurrent connections
	var eg errgroup.Group
	for i := 0; i < 4; i++ {
		eg.Go(testTCPConn(throughputClientPort, 64*1024, 10*time.Second))
	}
	if err := eg.Wait(); err != nil {
		t.Fatalf("concurrent TCP failed: %v", err)
	}
}

func TestAnyTLSEndToEndUDP(t *testing.T) {
	udpServer := udp.Server{MsgProcessor: xor}
	dest, err := udpServer.Start()
	common.Must(err)
	defer udpServer.Close()

	srvTLS, cliTLS := testCertConfig()
	serverPort := tcp.PickPort()
	startCoreInstance(t, &core.Config{
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
					StreamSettings: &internet.StreamConfig{
						ProtocolName:     "tcp",
						SecurityType:     serial.GetMessageType(srvTLS),
						SecuritySettings: []*serial.TypedMessage{serial.ToTypedMessage(srvTLS)},
					},
				}),
				ProxySettings: serial.ToTypedMessage(&anytls.ServerConfig{
					Users: []*protocol.User{
						{Account: serial.ToTypedMessage(&anytls.Account{Password: "udp-pw"})},
					},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&freedom.Config{
					Firewall: []*freedom.FirewallConfig{{Action: freedom.RuleAction_Allow}},
				}),
			},
		},
	})

	clientPort := udp.PickPort()
	startCoreInstance(t, &core.Config{
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(clientPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(dest.Address),
					RewritePort:     uint32(dest.Port),
					AllowedNetworks: []net.Network{net.Network_UDP},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&anytls.ClientConfig{
					Server: &protocol.ServerEndpoint{
						Address: net.NewIPOrDomain(net.LocalHostIP),
						Port:    uint32(serverPort),
						User:    &protocol.User{Account: serial.ToTypedMessage(&anytls.Account{Password: "udp-pw"})},
					},
				}),
				SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
					StreamSettings: &internet.StreamConfig{
						ProtocolName:     "tcp",
						SecurityType:     serial.GetMessageType(cliTLS),
						SecuritySettings: []*serial.TypedMessage{serial.ToTypedMessage(cliTLS)},
					},
				}),
			},
		},
	})

	if err := testUDPConn(clientPort, 4096, 5*time.Second)(); err != nil {
		t.Fatalf("UDP roundtrip failed: %v", err)
	}
}

func TestAnyTLSEndToEndREALITY(t *testing.T) {
	// Local HTTPS cover site so REALITY test is completely deterministic offline
	coverCert, err := cert.Generate(nil, cert.DNSNames("cover.example"))
	common.Must(err)
	certPEM, keyPEM := coverCert.ToPEM()
	tlsPair, err := gotls.X509KeyPair(certPEM, keyPEM)
	common.Must(err)
	coverLn, err := gotls.Listen("tcp", "127.0.0.1:0", &gotls.Config{
		Certificates: []gotls.Certificate{tlsPair},
		NextProtos:   []string{"h2", "http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer coverLn.Close()
	coverSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cover-site"))
	})}
	go func() { _ = coverSrv.Serve(coverLn) }()
	defer coverSrv.Close()

	tcpServer := tcp.Server{MsgProcessor: xor}
	dest, err := tcpServer.Start()
	common.Must(err)
	defer tcpServer.Close()

	privateKey, _ := base64.RawURLEncoding.DecodeString("aGSYystUbf59_9_6LKRxD27rmSW_-2_nyd9YG_Gwbks")
	publicKey, _ := base64.RawURLEncoding.DecodeString("E59WjnvZcQMu7tR7_BgyhycuEdBS-CtKxfImRCdAvFM")
	shortID := make([]byte, 8)
	_, _ = hex.Decode(shortID, []byte("0123456789abcdef"))

	serverPort := tcp.PickPort()
	startCoreInstance(t, &core.Config{
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
					StreamSettings: &internet.StreamConfig{
						ProtocolName: "tcp",
						SecurityType: serial.GetMessageType(&reality.Config{}),
						SecuritySettings: []*serial.TypedMessage{
							serial.ToTypedMessage(&reality.Config{
								Dest:        coverLn.Addr().String(),
								ServerNames: []string{"cover.example"},
								PrivateKey:  privateKey,
								ShortIds:    [][]byte{shortID},
								Type:        "tcp",
							}),
						},
					},
				}),
				ProxySettings: serial.ToTypedMessage(&anytls.ServerConfig{
					Users: []*protocol.User{
						{Account: serial.ToTypedMessage(&anytls.Account{Password: "reality-pw"})},
					},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&freedom.Config{
					Firewall: []*freedom.FirewallConfig{{Action: freedom.RuleAction_Allow}},
				}),
			},
		},
	})

	clientPort := tcp.PickPort()
	startCoreInstance(t, &core.Config{
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(clientPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(dest.Address),
					RewritePort:     uint32(dest.Port),
					AllowedNetworks: []net.Network{net.Network_TCP},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&anytls.ClientConfig{
					Server: &protocol.ServerEndpoint{
						Address: net.NewIPOrDomain(net.LocalHostIP),
						Port:    uint32(serverPort),
						User:    &protocol.User{Account: serial.ToTypedMessage(&anytls.Account{Password: "reality-pw"})},
					},
				}),
				SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
					StreamSettings: &internet.StreamConfig{
						ProtocolName: "tcp",
						SecurityType: serial.GetMessageType(&reality.Config{}),
						SecuritySettings: []*serial.TypedMessage{
							serial.ToTypedMessage(&reality.Config{
								Fingerprint: "chrome",
								ServerName:  "cover.example",
								PublicKey:   publicKey,
								ShortId:     shortID,
								SpiderX:     "/",
							}),
						},
					},
				}),
			},
		},
	})

	// Allow REALITY server's background cover probe to record initial state before dialing.
	time.Sleep(150 * time.Millisecond)
	if err := testTCPConn(clientPort, 32*1024, 15*time.Second)(); err != nil {
		t.Fatalf("AnyTLS over REALITY failed: %v", err)
	}
}

func TestAnyTLSPrivateTargetBlockedAndFallback(t *testing.T) {
	// 1. Verify freedom blocks private target (127.0.0.1) by default for anytls inbound.
	tcpServer := tcp.Server{MsgProcessor: xor}
	dest, err := tcpServer.Start()
	common.Must(err)
	defer tcpServer.Close()

	fbLn, err := stdnet.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	defer fbLn.Close()
	fbSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fb-response"))
	})}
	go func() { _ = fbSrv.Serve(fbLn) }()
	defer fbSrv.Close()

	srvTLS, cliTLS := testCertConfig()
	serverPort := tcp.PickPort()
	startCoreInstance(t, &core.Config{
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
					StreamSettings: &internet.StreamConfig{
						ProtocolName:     "tcp",
						SecurityType:     serial.GetMessageType(srvTLS),
						SecuritySettings: []*serial.TypedMessage{serial.ToTypedMessage(srvTLS)},
					},
				}),
				ProxySettings: serial.ToTypedMessage(&anytls.ServerConfig{
					Users: []*protocol.User{
						{Account: serial.ToTypedMessage(&anytls.Account{Password: "pw"})},
					},
					Fallbacks: []*anytls.Fallback{
						{Type: "tcp", Dest: fbLn.Addr().String()},
					},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				// Default freedom config (no Allow rule): must block private IPs for anytls inbound
				ProxySettings: serial.ToTypedMessage(&freedom.Config{}),
			},
		},
	})

	clientPort := tcp.PickPort()
	startCoreInstance(t, &core.Config{
		Inbound: []*core.InboundHandlerConfig{
			{
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(clientPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(dest.Address),
					RewritePort:     uint32(dest.Port),
					AllowedNetworks: []net.Network{net.Network_TCP},
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&anytls.ClientConfig{
					Server: &protocol.ServerEndpoint{
						Address: net.NewIPOrDomain(net.LocalHostIP),
						Port:    uint32(serverPort),
						User:    &protocol.User{Account: serial.ToTypedMessage(&anytls.Account{Password: "pw"})},
					},
				}),
				SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
					StreamSettings: &internet.StreamConfig{
						ProtocolName:     "tcp",
						SecurityType:     serial.GetMessageType(cliTLS),
						SecuritySettings: []*serial.TypedMessage{serial.ToTypedMessage(cliTLS)},
					},
				}),
			},
		},
	})

	// Private target to 127.0.0.1 must be blocked by freedom's default rule
	conn, err := stdnet.Dial("tcp", stdnet.JoinHostPort("127.0.0.1", net.Port(clientPort).String()))
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 16)
	_, _ = rand.Read(payload)
	_, _ = conn.Write(payload)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	_ = conn.Close()
	if err == nil && n > 0 {
		t.Fatal("expected private target 127.0.0.1 to be blocked by default freedom rule")
	}

	// 2. Unauthenticated HTTPS client connecting to AnyTLS TLS port must fall back to fbSrv
	tlsConn, err := gotls.Dial("tcp", stdnet.JoinHostPort("127.0.0.1", net.Port(serverPort).String()), &gotls.Config{
		ServerName:         "anytls.test",
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tlsConn.Close()
	_, _ = tlsConn.Write([]byte("GET / HTTP/1.1\r\nHost: anytls.test\r\nConnection: close\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatalf("TLS fallback failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "fb-response" {
		t.Fatalf("unexpected fallback body: %q", string(body))
	}
}
