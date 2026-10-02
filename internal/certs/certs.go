package certs

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	caValidityDays     = 3650
	serverValidityDays = 397
)

type Paths struct {
	Dir        string
	CACert     string
	CAKey      string
	ServerCert string
	ServerKey  string
}

func Ensure(baseDir, lanIP string) (Paths, error) {
	paths := Paths{
		Dir:        filepath.Join(baseDir, "certs"),
		CACert:     filepath.Join(baseDir, "certs", "epos-proxy-ca.crt"),
		CAKey:      filepath.Join(baseDir, "certs", "epos-proxy-ca.key"),
		ServerCert: filepath.Join(baseDir, "certs", "epos-proxy-server.crt"),
		ServerKey:  filepath.Join(baseDir, "certs", "epos-proxy-server.key"),
	}

	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		return paths, fmt.Errorf("create certificate directory: %w", err)
	}

	caCert, caKey, err := ensureCA(paths)
	if err != nil {
		return paths, err
	}

	if err := ensureServerCertificate(paths, caCert, caKey, lanIP); err != nil {
		return paths, err
	}

	return paths, nil
}

func ensureCA(paths Paths) (*x509.Certificate, *rsa.PrivateKey, error) {
	certExists := fileExists(paths.CACert)
	keyExists := fileExists(paths.CAKey)

	if certExists != keyExists {
		return nil, nil, errors.New("local HTTPS CA is incomplete; restore both epos-proxy-ca.crt and epos-proxy-ca.key instead of silently rotating the trusted CA")
	}

	if certExists {
		cert, err := readCertificate(paths.CACert)
		if err != nil {
			return nil, nil, fmt.Errorf("read local HTTPS CA certificate: %w", err)
		}
		key, err := readRSAKey(paths.CAKey)
		if err != nil {
			return nil, nil, fmt.Errorf("read local HTTPS CA key: %w", err)
		}
		if !cert.IsCA {
			return nil, nil, errors.New("stored HTTPS CA certificate is not a CA")
		}
		if time.Now().After(cert.NotAfter) {
			return nil, nil, errors.New("stored HTTPS CA certificate has expired; manual CA replacement is required")
		}
		if !certificateMatchesKey(cert, key) {
			return nil, nil, errors.New("stored HTTPS CA certificate and private key do not match")
		}
		if err := os.Chmod(paths.CAKey, 0o600); err != nil {
			return nil, nil, fmt.Errorf("secure local HTTPS CA key permissions: %w", err)
		}
		_ = os.Chmod(paths.CACert, 0o644)
		return cert, key, nil
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate local HTTPS CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("generate local HTTPS CA serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "ePOS Proxy Local CA",
			Organization: []string{"ePOS Proxy"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(0, 0, caValidityDays),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create local HTTPS CA certificate: %w", err)
	}

	if err := writeCertificate(paths.CACert, der, 0o644); err != nil {
		return nil, nil, err
	}
	if err := writeRSAKey(paths.CAKey, key, 0o600); err != nil {
		_ = os.Remove(paths.CACert)
		return nil, nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated local HTTPS CA certificate: %w", err)
	}
	return cert, key, nil
}

func ensureServerCertificate(paths Paths, caCert *x509.Certificate, caKey *rsa.PrivateKey, lanIP string) error {
	if serverCertificateIsCurrent(paths, caCert, lanIP) {
		_ = os.Chmod(paths.ServerKey, 0o600)
		_ = os.Chmod(paths.ServerCert, 0o644)
		return nil
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate HTTPS server key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return fmt.Errorf("generate HTTPS server serial: %w", err)
	}

	now := time.Now()
	notAfter := now.AddDate(0, 0, serverValidityDays)
	if !caCert.NotAfter.IsZero() && notAfter.After(caCert.NotAfter) {
		notAfter = caCert.NotAfter.Add(-time.Hour)
	}
	if !notAfter.After(now) {
		return errors.New("local HTTPS CA expires too soon to issue a server certificate")
	}

	ips := []net.IP{net.ParseIP("127.0.0.1")}
	if ip := net.ParseIP(lanIP); ip != nil && !ip.IsLoopback() {
		ips = append(ips, ip)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "ePOS Proxy Local Server",
			Organization: []string{"ePOS Proxy"},
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    notAfter,
		DNSNames:    []string{"localhost"},
		IPAddresses: ips,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create HTTPS server certificate: %w", err)
	}

	if err := writeCertificate(paths.ServerCert, der, 0o644); err != nil {
		return err
	}
	if err := writeRSAKey(paths.ServerKey, key, 0o600); err != nil {
		return err
	}
	return nil
}

func serverCertificateIsCurrent(paths Paths, caCert *x509.Certificate, lanIP string) bool {
	if !fileExists(paths.ServerCert) || !fileExists(paths.ServerKey) {
		return false
	}

	cert, err := readCertificate(paths.ServerCert)
	if err != nil {
		return false
	}
	key, err := readRSAKey(paths.ServerKey)
	if err != nil || !certificateMatchesKey(cert, key) {
		return false
	}
	if err := cert.CheckSignatureFrom(caCert); err != nil {
		return false
	}
	if time.Now().Add(30 * 24 * time.Hour).After(cert.NotAfter) {
		return false
	}
	if !containsString(cert.DNSNames, "localhost") || !containsIP(cert.IPAddresses, net.ParseIP("127.0.0.1")) {
		return false
	}
	if ip := net.ParseIP(lanIP); ip != nil && !ip.IsLoopback() && !containsIP(cert.IPAddresses, ip) {
		return false
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			return true
		}
	}
	return false
}

func readCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func readRSAKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		return nil, errors.New("invalid RSA private key PEM")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func writeCertificate(path string, der []byte, perm os.FileMode) error {
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if data == nil {
		return errors.New("encode certificate PEM")
	}
	return writeFile(path, data, perm)
}

func writeRSAKey(path string, key *rsa.PrivateKey, perm os.FileMode) error {
	data := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if data == nil {
		return errors.New("encode RSA private key PEM")
	}
	return writeFile(path, data, perm)
}

func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(filepath.Clean(path), data, perm); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Chmod(filepath.Clean(path), perm); err != nil {
		return fmt.Errorf("set permissions on %s: %w", filepath.Base(path), err)
	}
	return nil
}

func certificateMatchesKey(cert *x509.Certificate, key *rsa.PrivateKey) bool {
	certPub, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return false
	}
	keyPub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return false
	}
	return bytes.Equal(certPub, keyPub)
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, err
	}
	if serial.Sign() == 0 {
		return big.NewInt(1), nil
	}
	return serial, nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsIP(values []net.IP, expected net.IP) bool {
	if expected == nil {
		return false
	}
	for _, value := range values {
		if value.Equal(expected) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(filepath.Clean(path))
	return err == nil
}
