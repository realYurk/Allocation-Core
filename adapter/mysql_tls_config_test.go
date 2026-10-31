package adapter

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// Synthetic, short-lived CA/cert. No network services or secrets required.
func ephemeralTestCAAndServer(t *testing.T) ([]byte, tls.Certificate) {
	t.Helper()
	now := time.Now()
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Allocation CI synthetic CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, IsCA: true, BasicConstraintsValid: true}
	caDER, e := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	ca, e := x509.ParseCertificate(caDER)
	if e != nil {
		t.Fatal(e)
	}
	serverKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "mysql.test.internal"}, DNSNames: []string{"mysql.test.internal"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	serverDER, e := x509.CreateCertificate(rand.Reader, leaf, ca, &serverKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), tls.Certificate{Certificate: [][]byte{serverDER, caDER}, PrivateKey: serverKey}
}

func tlsProbe(t *testing.T, certificate tls.Certificate, cfg *tls.Config) error {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := listener.Accept()
		if e == nil {
			_ = c.(*tls.Conn).Handshake()
			_ = c.Close()
		}
	}()
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	c, e := tls.DialWithDialer(dialer, "tcp", listener.Addr().String(), cfg)
	if e == nil {
		_ = c.Close()
	}
	_ = listener.Close()
	<-done
	return e
}

func TestV8MySQLTLSValidChainAndSAN(t *testing.T) {
	ca, server := ephemeralTestCAAndServer(t)
	cfg, e := verifiedMySQLTLS("mysql.test.internal", ca)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.MinVersion != tls.VersionTLS12 || cfg.InsecureSkipVerify || cfg.ServerName != "mysql.test.internal" {
		t.Fatalf("weak TLS policy: %+v", cfg)
	}
	if e := tlsProbe(t, server, cfg); e != nil {
		t.Fatalf("valid certificate rejected: %v", e)
	}
}
func TestV8MySQLTLSRejectsWrongSANAndUntrustedCA(t *testing.T) {
	ca, server := ephemeralTestCAAndServer(t)
	wrongName, e := verifiedMySQLTLS("other.test.internal", ca)
	if e != nil {
		t.Fatal(e)
	}
	if e := tlsProbe(t, server, wrongName); e == nil {
		t.Fatal("wrong server name was accepted")
	}
	otherCA, _ := ephemeralTestCAAndServer(t)
	wrongCA, e := verifiedMySQLTLS("mysql.test.internal", otherCA)
	if e != nil {
		t.Fatal(e)
	}
	if e := tlsProbe(t, server, wrongCA); e == nil {
		t.Fatal("untrusted CA was accepted")
	}
	if _, e := verifiedMySQLTLS("mysql.test.internal", []byte("not a CA")); e == nil {
		t.Fatal("invalid CA accepted")
	}
	if _, e := verifiedMySQLTLS("", ca); e == nil {
		t.Fatal("empty server name accepted")
	}
}
