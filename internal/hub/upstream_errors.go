package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"syscall"
)

// Return fixed diagnostics only: raw errors can contain URLs, credentials, or response bodies.
func upstreamConnectionError(service string, err error, status int) error {
	var hostname x509.HostnameError
	var authority x509.UnknownAuthorityError
	var certificate x509.CertificateInvalidError
	var dns *net.DNSError
	var record tls.RecordHeaderError
	var network net.Error
	switch {
	case errors.As(err, &hostname):
		return fmt.Errorf("%s TLS certificate hostname mismatch; use a URL matching its certificate", service)
	case errors.As(err, &authority):
		return fmt.Errorf("%s TLS certificate authority is not trusted; install the correct CA", service)
	case errors.As(err, &certificate):
		return fmt.Errorf("%s TLS certificate is invalid or expired; check certificate and system time", service)
	case errors.As(err, &record):
		return fmt.Errorf("%s TLS handshake failed; verify whether the upstream port uses TLS or plaintext", service)
	case errors.As(err, &dns):
		return fmt.Errorf("%s DNS lookup failed; check the container name and shared Docker network", service)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%s connection refused; check listener address and port", service)
	case errors.As(err, &network) && network.Timeout():
		return fmt.Errorf("%s connection timed out; check routing, firewall, and upstream availability", service)
	case status != 0:
		return fmt.Errorf("%s HTTP %d; check upstream authentication, address, and proxy routing", service, status)
	default:
		return fmt.Errorf("%s connection failed; check protocol, listener, network, and TLS configuration", service)
	}
}
