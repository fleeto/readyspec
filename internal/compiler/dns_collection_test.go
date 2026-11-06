package compiler_test

import (
	"encoding/binary"
	"net"
	"runtime"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

func dnsName(name string) []byte {
	out := []byte{}
	for _, part := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		out = append(out, byte(len(part)))
		out = append(out, part...)
	}
	return append(out, 0)
}

func TestDNSCollectorRecordTypesAndFailures(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	for _, tt := range []struct {
		name, kind, query, expected, status string
		rtype                               uint16
		data                                []byte
		rcode                               uint16
		drop                                bool
	}{
		{"A", "A", "fixture.test", "192.0.2.1", "PASS", 1, []byte{192, 0, 2, 1}, 0, false},
		{"AAAA", "AAAA", "fixture.test", "2001:db8::1", "PASS", 28, net.ParseIP("2001:db8::1").To16(), 0, false},
		{"CNAME", "CNAME", "fixture.test", "target.test.", "PASS", 5, dnsName("target.test"), 0, false},
		{"MX", "MX", "fixture.test", "10 mail.test.", "PASS", 15, append([]byte{0, 10}, dnsName("mail.test")...), 0, false},
		{"NS", "NS", "fixture.test", "ns.test.", "PASS", 2, dnsName("ns.test"), 0, false},
		{"PTR", "PTR", "192.0.2.1", "target.test.", "PASS", 12, dnsName("target.test"), 0, false},
		{"TXT", "TXT", "fixture.test", "healthy", "PASS", 16, append([]byte{7}, []byte("healthy")...), 0, false},
		{"SRV", "SRV", "_app._tcp.fixture.test", "1 2 8080 target.test.", "PASS", 33, append([]byte{0, 1, 0, 2, 31, 144}, dnsName("target.test")...), 0, false},
		{"mismatch", "A", "fixture.test", "192.0.2.2", "FAIL", 1, []byte{192, 0, 2, 1}, 0, false},
		{"nxdomain", "A", "fixture.test", "192.0.2.1", "FAIL", 1, nil, 3, false},
		{"timeout", "A", "fixture.test", "192.0.2.1", "ERROR", 1, nil, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dns, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer dns.Close()
			go func() {
				buffer := make([]byte, 4096)
				for {
					n, address, err := dns.ReadFrom(buffer)
					if err != nil {
						return
					}
					if n < 17 || tt.drop {
						continue
					}
					end := 12
					for end < n && buffer[end] != 0 {
						end += int(buffer[end]) + 1
					}
					end += 5
					if end > n {
						continue
					}
					response := make([]byte, 12)
					copy(response, buffer[:2])
					binary.BigEndian.PutUint16(response[2:4], 0x8180|tt.rcode)
					binary.BigEndian.PutUint16(response[4:6], 1)
					if tt.data != nil {
						binary.BigEndian.PutUint16(response[6:8], 1)
					}
					response = append(response, buffer[12:end]...)
					if tt.data != nil {
						answer := []byte{0xc0, 0x0c, byte(tt.rtype >> 8), byte(tt.rtype), 0, 1, 0, 0, 0, 0, byte(len(tt.data) >> 8), byte(len(tt.data))}
						response = append(response, answer...)
						response = append(response, tt.data...)
					}
					_, _ = dns.WriteTo(response, address)
				}
			}()
			p := catalogPlan(t, "N04")
			p.Roles[0].Items[0].Params = map[string]any{"name": tt.query, "server": dns.LocalAddr().String(), "record_type": tt.kind, "addrs": []any{tt.expected}, "timeout_ms": float64(50)}
			a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
			results := runLocalArtifact(t, a)
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
}

func TestDNSCollectorDistinguishesServerFailures(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	for _, tt := range []struct {
		name, query, expected, status string
		rcode                         uint16
		truncate, malformed           bool
	}{
		{"servfail", "fixture.test", "192.0.2.1", "ERROR", 2, false, false},
		{"refused", "fixture.test", "192.0.2.1", "ERROR", 5, false, false},
		{"truncated", "fixture.test", "192.0.2.1", "ERROR", 0, true, false},
		{"malformed", "fixture.test", "192.0.2.1", "ERROR", 0, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dns, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer dns.Close()
			go func() {
				buffer := make([]byte, 4096)
				for {
					n, address, err := dns.ReadFrom(buffer)
					if err != nil {
						return
					}
					if n < 17 {
						continue
					}
					response := make([]byte, 12)
					copy(response, buffer[:2])
					flags := uint16(0x8180) | tt.rcode
					if tt.truncate {
						flags |= 0x0200
					}
					binary.BigEndian.PutUint16(response[2:4], flags)
					binary.BigEndian.PutUint16(response[4:6], 1)
					if tt.malformed {
						response = response[:6]
					}
					_, _ = dns.WriteTo(response, address)
				}
			}()
			p := catalogPlan(t, "N04")
			p.Roles[0].Items[0].Params = map[string]any{"name": tt.query, "server": dns.LocalAddr().String(), "record_type": "A", "addrs": []any{tt.expected}, "timeout_ms": float64(200)}
			a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
			results := runLocalArtifact(t, a)
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
}
