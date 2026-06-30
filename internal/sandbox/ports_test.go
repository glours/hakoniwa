package sandbox_test

import (
	"testing"

	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

func TestParsePortSpecBasic(t *testing.T) {
	req, err := sandbox.ParsePortSpec("8080:8080")
	if err != nil {
		t.Fatal(err)
	}
	if req.HostPort != 8080 || req.SandboxPort != 8080 {
		t.Errorf("ports = %d:%d", req.HostPort, req.SandboxPort)
	}
	if req.Protocol == nil || *req.Protocol != sandboxapi.PortPublishRequestProtocolTcp {
		t.Error("expected tcp protocol")
	}
	if req.HostIp != nil {
		t.Errorf("expected no host IP, got %q", *req.HostIp)
	}
}

func TestParsePortSpecUDP(t *testing.T) {
	req, err := sandbox.ParsePortSpec("5353:5353/udp")
	if err != nil {
		t.Fatal(err)
	}
	if req.Protocol == nil || *req.Protocol != sandboxapi.PortPublishRequestProtocolUdp {
		t.Error("expected udp protocol")
	}
}

func TestParsePortSpecHostIP(t *testing.T) {
	req, err := sandbox.ParsePortSpec("127.0.0.1:3000:3000")
	if err != nil {
		t.Fatal(err)
	}
	if req.HostIp == nil || *req.HostIp != "127.0.0.1" {
		t.Errorf("host IP = %v", req.HostIp)
	}
	if req.HostPort != 3000 || req.SandboxPort != 3000 {
		t.Errorf("ports = %d:%d", req.HostPort, req.SandboxPort)
	}
}

func TestParsePortSpecAutoAssign(t *testing.T) {
	req, err := sandbox.ParsePortSpec("0:8080")
	if err != nil {
		t.Fatal(err)
	}
	if req.HostPort != 0 {
		t.Errorf("expected host port 0 (auto), got %d", req.HostPort)
	}
	if req.SandboxPort != 8080 {
		t.Errorf("expected sandbox port 8080, got %d", req.SandboxPort)
	}
}

func TestParsePortSpecHostIPWithProto(t *testing.T) {
	req, err := sandbox.ParsePortSpec("0.0.0.0:443:443/tcp")
	if err != nil {
		t.Fatal(err)
	}
	if req.HostIp == nil || *req.HostIp != "0.0.0.0" {
		t.Errorf("host IP = %v", req.HostIp)
	}
	if req.Protocol == nil || *req.Protocol != sandboxapi.PortPublishRequestProtocolTcp {
		t.Error("expected tcp")
	}
}

func TestParsePortSpecErrors(t *testing.T) {
	cases := []struct {
		spec string
		desc string
	}{
		{"notaport:8080", "non-numeric host port"},
		{"8080:notaport", "non-numeric sandbox port"},
		{"8080", "missing colon"},
		{"8080:8080/sctp", "sctp unsupported"},
		{"8080:8080/ftp", "unknown protocol"},
		{"65536:8080", "host port out of range"},
		{"8080:0", "sandbox port zero"},
		{"8080:65536", "sandbox port out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := sandbox.ParsePortSpec(tc.spec)
			if err == nil {
				t.Errorf("expected error for %q, got nil", tc.spec)
			}
		})
	}
}
