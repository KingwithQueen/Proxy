package conf_test

import (
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/anytls"
	"google.golang.org/protobuf/proto"
)

func TestAnyTLSClientConfig(t *testing.T) {
	cases := []struct {
		input string
		valid bool
	}{
		{`{"address":"example.com","port":443,"password":"secret","email":"u@example.com","level":1,"idleSessionCheckInterval":30,"idleSessionTimeout":60,"minIdleSession":2}`, true},
		{`{"port":443,"password":"secret"}`, false},
		{`{"address":"example.com","password":"secret"}`, false},
		{`{"address":"example.com","port":443}`, false},
		{`{"address":"example.com","port":443,"password":"secret","minIdleSession":-1}`, false},
	}

	for _, tc := range cases {
		var cfg conf.AnyTLSClientConfig
		if err := json.Unmarshal([]byte(tc.input), &cfg); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.input, err)
		}
		built, err := cfg.Build()
		if (err == nil) != tc.valid {
			t.Fatalf("Build(%s) err=%v, wantValid=%v", tc.input, err, tc.valid)
		}
		if !tc.valid {
			continue
		}
		data, err := proto.Marshal(built)
		if err != nil {
			t.Fatal(err)
		}
		var roundtrip anytls.ClientConfig
		if err := proto.Unmarshal(data, &roundtrip); err != nil {
			t.Fatal(err)
		}
		if roundtrip.Server == nil || roundtrip.Server.Port != 443 || roundtrip.IdleSessionCheckInterval != 30 || roundtrip.IdleSessionTimeout != 60 || roundtrip.MinIdleSession != 2 {
			t.Fatalf("unexpected roundtrip config: %+v", &roundtrip)
		}
	}
}

func TestAnyTLSServerConfig(t *testing.T) {
	cases := []struct {
		input string
		valid bool
	}{
		{`{"users":[{"password":"p1","email":"a@b.c","level":1}],"paddingScheme":["stop=8","0=30-30","1=100-400"]}`, true},
		{`{"clients":[{"password":"p1"}],"paddingScheme":"stop=4\n0=30-30\n1=100-200"}`, true},
		{`{"users":[{"password":"p1"}],"fallbacks":[{"dest":"8080","xver":1}]}`, true},
		{`{"users":[]}`, false},
		{`{"users":[{"password":""}]}`, false},
		{`{"users":[{"password":"dup","email":"a@b"},{"password":"dup","email":"c@d"}]}`, false},
		{`{"users":[{"password":"p1","email":"Same@x"},{"password":"p2","email":"same@x"}]}`, false},
		{`{"users":[{"password":"p1"}],"paddingScheme":["stop=-1","0=30-30"]}`, false},
	}

	for _, tc := range cases {
		var cfg conf.AnyTLSServerConfig
		if err := json.Unmarshal([]byte(tc.input), &cfg); err != nil {
			t.Fatalf("unmarshal %s: %v", tc.input, err)
		}
		built, err := cfg.Build()
		if (err == nil) != tc.valid {
			t.Fatalf("Build(%s) err=%v, wantValid=%v", tc.input, err, tc.valid)
		}
		if !tc.valid {
			continue
		}
		data, err := proto.Marshal(built)
		if err != nil {
			t.Fatal(err)
		}
		var roundtrip anytls.ServerConfig
		if err := proto.Unmarshal(data, &roundtrip); err != nil {
			t.Fatal(err)
		}
		if len(roundtrip.Users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(roundtrip.Users))
		}
	}
}

func TestAnyTLSOutboundSecurityValidation(t *testing.T) {
	// Public server without TLS/REALITY must fail
	noTLS := `{
		"protocol": "anytls",
		"settings": {"address": "example.com", "port": 443, "password": "secret"}
	}`
	var ob1 conf.OutboundDetourConfig
	if err := json.Unmarshal([]byte(noTLS), &ob1); err != nil {
		t.Fatal(err)
	}
	if _, err := ob1.Build(); err == nil {
		t.Fatal("expected error for public anytls outbound without TLS/REALITY")
	}

	// Private loopback server without TLS is allowed
	localNoTLS := `{
		"protocol": "anytls",
		"settings": {"address": "127.0.0.1", "port": 443, "password": "secret"}
	}`
	var ob2 conf.OutboundDetourConfig
	if err := json.Unmarshal([]byte(localNoTLS), &ob2); err != nil {
		t.Fatal(err)
	}
	if _, err := ob2.Build(); err != nil {
		t.Fatalf("expected private anytls outbound to succeed, got %v", err)
	}

	// Public server with TLS succeeds
	withTLS := `{
		"protocol": "anytls",
		"settings": {"address": "example.com", "port": 443, "password": "secret"},
		"streamSettings": {"network": "tcp", "security": "tls"}
	}`
	var ob3 conf.OutboundDetourConfig
	if err := json.Unmarshal([]byte(withTLS), &ob3); err != nil {
		t.Fatal(err)
	}
	if _, err := ob3.Build(); err != nil {
		t.Fatalf("expected TLS anytls outbound to succeed, got %v", err)
	}
}
