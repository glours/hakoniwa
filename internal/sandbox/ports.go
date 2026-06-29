package sandbox

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// ParsePortSpec parses a Hakoniwa port-spec string into a PortPublishRequest
// suitable for passing to Client.PublishPorts.
//
// Accepted formats (same as the config validator in internal/config/validate.go):
//
//	HOST_PORT:SANDBOX_PORT[/PROTO]
//	HOST_IP:HOST_PORT:SANDBOX_PORT[/PROTO]   (IPv4 only)
//
// PROTO is "tcp" (default), "udp", or "tcp4"/"tcp6"/"udp4"/"udp6".
// HOST_PORT=0 requests automatic host-port assignment by the daemon.
func ParsePortSpec(spec string) (PortPublishRequest, error) {
	orig := spec

	// Split optional /proto suffix.
	var proto sandboxapi.PortPublishRequestProtocol
	if idx := strings.LastIndex(spec, "/"); idx >= 0 {
		protoStr := spec[idx+1:]
		spec = spec[:idx]
		switch protoStr {
		case "tcp":
			proto = sandboxapi.PortPublishRequestProtocolTcp
		case "tcp4":
			proto = sandboxapi.PortPublishRequestProtocolTcp4
		case "tcp6":
			proto = sandboxapi.PortPublishRequestProtocolTcp6
		case "udp":
			proto = sandboxapi.PortPublishRequestProtocolUdp
		case "udp4":
			proto = sandboxapi.PortPublishRequestProtocolUdp4
		case "udp6":
			proto = sandboxapi.PortPublishRequestProtocolUdp6
		case "sctp":
			return PortPublishRequest{}, fmt.Errorf("port spec %q: sctp is not supported by sandboxd", orig)
		default:
			return PortPublishRequest{}, fmt.Errorf("port spec %q: unknown protocol %q", orig, protoStr)
		}
	} else {
		proto = sandboxapi.PortPublishRequestProtocolTcp
	}

	parts := strings.Split(spec, ":")
	var hostIPStr, hostPortStr, sbxPortStr string
	switch len(parts) {
	case 2:
		hostPortStr = parts[0]
		sbxPortStr = parts[1]
	case 3:
		hostIPStr = parts[0]
		hostPortStr = parts[1]
		sbxPortStr = parts[2]
	default:
		return PortPublishRequest{}, fmt.Errorf(
			"port spec %q: expected HOST_PORT:SANDBOX_PORT or HOST_IP:HOST_PORT:SANDBOX_PORT", orig)
	}

	hostPort, err := strconv.Atoi(hostPortStr)
	if err != nil || hostPort < 0 || hostPort > 65535 {
		return PortPublishRequest{}, fmt.Errorf("port spec %q: invalid host port %q", orig, hostPortStr)
	}
	sbxPort, err := strconv.Atoi(sbxPortStr)
	if err != nil || sbxPort < 1 || sbxPort > 65535 {
		return PortPublishRequest{}, fmt.Errorf("port spec %q: invalid sandbox port %q", orig, sbxPortStr)
	}

	req := PortPublishRequest{
		HostPort:    hostPort,
		SandboxPort: sbxPort,
		Protocol:    &proto,
	}
	if hostIPStr != "" {
		req.HostIp = &hostIPStr
	}
	return req, nil
}
