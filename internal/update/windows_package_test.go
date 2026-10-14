package update

import (
	"archive/zip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsPublisherSignatureBindsVersionPlatformAndDigest(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test publisher"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	cert, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	meta := windowsSignature{Version: "9.0.0", Platform: "windows-amd64", SHA256: "fixture digest"}
	h := sha256.Sum256(signedMessage(meta.Version, meta.Platform, meta.SHA256))
	sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if e != nil {
		t.Fatal(e)
	}
	meta.Signature = base64.StdEncoding.EncodeToString(sig)
	if e = verifyWindowsSignature(meta, meta.Version, meta.Platform, meta.SHA256, cert); e != nil {
		t.Fatal(e)
	}
	for _, change := range []string{"version", "platform", "digest", "signature", "publisher"} {
		t.Run(change, func(t *testing.T) {
			m := meta
			expectedCert := cert
			switch change {
			case "version":
				m.Version = "10.0.0"
			case "platform":
				m.Platform = "windows-arm64"
			case "digest":
				m.SHA256 = "tampered"
			case "signature":
				m.Signature = "development"
			case "publisher":
				expectedCert = signingCertificate
			}
			if verifyWindowsSignature(m, m.Version, m.Platform, m.SHA256, expectedCert) == nil {
				t.Fatal("untrusted identity was accepted")
			}
		})
	}
}

func TestWindowsZipRejectsTraversalLinksAndDuplicates(t *testing.T) {
	for _, name := range []string{"../daylog.exe", "daylog.exe:stream", "C:\\daylog.exe", "link", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "update.zip")
			f, _ := os.Create(archive)
			z := zip.NewWriter(f)
			header := &zip.FileHeader{Name: name}
			if name == "link" {
				header.Name = "daylog.exe"
				header.SetMode(os.ModeSymlink | 0777)
			}
			if name == "duplicate" {
				header.Name = "signature.json"
			}
			w, _ := z.CreateHeader(header)
			w.Write([]byte("untrusted"))
			for _, n := range []string{"signature.json", "LICENSE", "THIRD_PARTY_NOTICES.txt"} {
				w, _ = z.Create(n)
				w.Write([]byte("{}"))
			}
			z.Close()
			f.Close()
			target := filepath.Join(dir, "extracted")
			os.Mkdir(target, 0700)
			if extractWindows(archive, target, "9.0.0", "amd64", false) == nil {
				t.Fatal("unsafe ZIP accepted")
			}
			if _, e := os.Stat(filepath.Join(dir, "daylog.exe")); !os.IsNotExist(e) {
				t.Fatal("archive escaped extraction root")
			}
		})
	}
}
