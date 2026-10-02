package certs

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"runtime"
	"testing"
)

func TestEnsureCreatesPersistentCAAndServerSANs(t *testing.T) {
	baseDir := t.TempDir()
	paths, err := Ensure(baseDir, "192.168.1.77")
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	caBefore, err := os.ReadFile(paths.CACert)
	if err != nil {
		t.Fatal(err)
	}
	serverBefore, err := os.ReadFile(paths.ServerCert)
	if err != nil {
		t.Fatal(err)
	}

	ca := mustReadCert(t, paths.CACert)
	server := mustReadCert(t, paths.ServerCert)
	if err := server.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("server certificate not signed by CA: %v", err)
	}
	if !containsString(server.DNSNames, "localhost") {
		t.Fatalf("server certificate missing localhost SAN")
	}
	for _, expected := range []string{"127.0.0.1", "192.168.1.77"} {
		if !containsIP(server.IPAddresses, net.ParseIP(expected)) {
			t.Fatalf("server certificate missing IP SAN %s", expected)
		}
	}

	pathsAgain, err := Ensure(baseDir, "192.168.1.77")
	if err != nil {
		t.Fatalf("second Ensure() error = %v", err)
	}
	caAgain, _ := os.ReadFile(pathsAgain.CACert)
	serverAgain, _ := os.ReadFile(pathsAgain.ServerCert)
	if string(caBefore) != string(caAgain) {
		t.Fatal("CA changed even though it was still valid")
	}
	if string(serverBefore) != string(serverAgain) {
		t.Fatal("server certificate changed even though SANs were unchanged")
	}
}

func TestEnsureRegeneratesOnlyServerCertificateWhenLANIPChanges(t *testing.T) {
	baseDir := t.TempDir()
	paths, err := Ensure(baseDir, "192.168.1.77")
	if err != nil {
		t.Fatal(err)
	}
	caBefore, _ := os.ReadFile(paths.CACert)
	serverBefore, _ := os.ReadFile(paths.ServerCert)

	paths, err = Ensure(baseDir, "192.168.1.80")
	if err != nil {
		t.Fatal(err)
	}
	caAfter, _ := os.ReadFile(paths.CACert)
	serverAfter, _ := os.ReadFile(paths.ServerCert)

	if string(caBefore) != string(caAfter) {
		t.Fatal("CA changed after LAN IP change")
	}
	if string(serverBefore) == string(serverAfter) {
		t.Fatal("server certificate did not change after LAN IP change")
	}

	server := mustReadCert(t, paths.ServerCert)
	if !containsIP(server.IPAddresses, net.ParseIP("192.168.1.80")) {
		t.Fatal("regenerated server certificate missing new LAN IP SAN")
	}
	if containsIP(server.IPAddresses, net.ParseIP("192.168.1.77")) {
		t.Fatal("regenerated server certificate still contains old LAN IP SAN")
	}
}

func TestEnsureUsesExpectedPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits consistently")
	}

	paths, err := Ensure(t.TempDir(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		perm os.FileMode
	}{
		{paths.CAKey, 0o600},
		{paths.ServerKey, 0o600},
		{paths.CACert, 0o644},
		{paths.ServerCert, 0o644},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tc.perm {
			t.Fatalf("%s permissions = %o, want %o", tc.path, got, tc.perm)
		}
	}
}

func mustReadCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("invalid PEM in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
