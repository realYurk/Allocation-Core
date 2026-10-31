package adapter

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
)

// verifiedMySQLTLS builds the same strict TLS policy for every MySQL connection.
// Kept independent of the optional MySQL driver so the PKI checks run during
// ordinary, offline unit tests on Windows/Linux/macOS.
func verifiedMySQLTLS(serverName string, caPEM []byte) (*tls.Config, error) {
	if strings.TrimSpace(serverName) == "" || strings.TrimSpace(serverName) != serverName {
		return nil, fmt.Errorf("verified TLS requires an explicit server name")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no valid PEM root CAs")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: serverName,
		// InsecureSkipVerify is intentionally never set.
	}, nil
}
