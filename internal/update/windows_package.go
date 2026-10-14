package update

import (
	"archive/zip"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"debug/pe"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type windowsSignature struct {
	Version   string `json:"version"`
	Platform  string `json:"platform"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

func signedMessage(version, platform, digest string) []byte {
	return []byte("daylog-windows-v1\n" + version + "\n" + platform + "\n" + digest + "\n")
}
func verifyWindowsSignature(meta windowsSignature, version, platform, digest string, certificate []byte) error {
	if meta.Version != version || meta.Platform != platform || meta.SHA256 != digest {
		return errors.New("Windows package version, platform or digest mismatch")
	}
	cert, e := x509.ParseCertificate(certificate)
	if e != nil {
		return e
	}
	key, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("unsupported publisher key")
	}
	sig, e := base64.StdEncoding.DecodeString(meta.Signature)
	if e != nil {
		return errors.New("invalid publisher signature")
	}
	h := sha256.Sum256(signedMessage(version, platform, digest))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, h[:], sig) != nil {
		return errors.New("Windows package publisher signature mismatch")
	}
	return nil
}

// The ZIP is data until both the executable hash and the fixed publisher's
// signature are checked. Only two exact entry names are allowed.
func extractWindows(archive, directory, version, arch string, allowDevelopment bool) error {
	r, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	defer r.Close()
	if len(r.File) != 2 {
		return errors.New("Windows package must contain executable and signature only")
	}
	seen := map[string]bool{}
	for _, f := range r.File {
		limit := uint64(32 << 20)
		if f.Name == "signature.json" {
			limit = 8192
		} else if f.Name != "daylog.exe" {
			return errors.New("unexpected Windows package entry")
		}
		if seen[f.Name] || f.Mode()&os.ModeSymlink != 0 || f.FileInfo().IsDir() || f.UncompressedSize64 > limit {
			return errors.New("unsafe Windows package entry")
		}
		seen[f.Name] = true
		in, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(filepath.Join(directory, f.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e != nil {
			in.Close()
			return e
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, int64(limit)+1))
		closeErr := out.Close()
		in.Close()
		if copyErr != nil || closeErr != nil || uint64(n) > limit {
			return errors.New("Windows package extraction failed")
		}
	}
	path := filepath.Join(directory, "daylog.exe")
	raw, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	digest := sha256.Sum256(raw)
	data, e := os.ReadFile(filepath.Join(directory, "signature.json"))
	if e != nil {
		return e
	}
	var meta windowsSignature
	if e = json.Unmarshal(data, &meta); e != nil {
		return e
	}
	if !allowDevelopment || meta.Signature != "development" {
		if e = verifyWindowsSignature(meta, version, "windows-"+arch, hex.EncodeToString(digest[:]), signingCertificate); e != nil {
			return e
		}
	} else if meta.Version != version || meta.Platform != "windows-"+arch || meta.SHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("development package identity mismatch")
	}
	binary, e := pe.Open(path)
	if e != nil {
		return errors.New("invalid Windows executable")
	}
	defer binary.Close()
	if arch != "amd64" || binary.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return errors.New("Windows executable architecture mismatch")
	}
	return nil
}
