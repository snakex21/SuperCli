package termux

import (
	"context"
	"net"
	"os"
	"path/filepath"
)

func init() { setup(os.Getenv("PREFIX")) }

func setup(prefix string) {
	if !isTermux(prefix) {
		return
	}
	if _, err := os.Stat("/etc/resolv.conf"); err != nil {
		conf, _ := os.ReadFile(filepath.Join(prefix, "etc", "resolv.conf"))
		servers := nameservers(string(conf))
		net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			var err error
			for _, server := range servers {
				var conn net.Conn
				if conn, err = dialer.DialContext(ctx, network, server); err == nil {
					return conn, nil
				}
			}
			return nil, err
		}}
	}
	// Go looks for CA certificates only in desktop Linux locations.
	if os.Getenv("SSL_CERT_FILE") == "" && os.Getenv("SSL_CERT_DIR") == "" {
		if cert := filepath.Join(prefix, "etc", "tls", "cert.pem"); fileExists(cert) {
			os.Setenv("SSL_CERT_FILE", cert)
		} else if fileExists("/system/etc/security/cacerts") {
			os.Setenv("SSL_CERT_DIR", "/system/etc/security/cacerts")
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
