package vc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"time"

	"github.com/vmware/govmomi/vim25/soap"
)

type CertInfo struct {
	Host        string
	Subject     string
	Issuer      string
	NotAfter    time.Time
	Fingerprint string
	Trusted     bool
}

func Probe(ctx context.Context, host string) (*CertInfo, error) {
	addr := hostPort(host)
	d := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("server presented no certificate")
	}
	leaf := certs[0]
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	name, _, _ := net.SplitHostPort(addr)
	_, verr := leaf.Verify(x509.VerifyOptions{DNSName: name, Intermediates: inter})
	return &CertInfo{
		Host:        addr,
		Subject:     leaf.Subject.String(),
		Issuer:      leaf.Issuer.String(),
		NotAfter:    leaf.NotAfter,
		Fingerprint: soap.ThumbprintSHA256(leaf),
		Trusted:     verr == nil,
	}, nil
}

func hostPort(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "443")
}
