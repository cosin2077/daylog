package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"debug/macho"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
type Feed struct {
	Version   string              `json:"version"`
	Artifacts map[string]Artifact `json:"artifacts"`
}
type Check struct {
	Current   string `json:"current"`
	Available string `json:"available"`
	Platform  string `json:"platform"`
	Upgrade   bool   `json:"upgrade_available"`
}

// Production builds pin the complete public certificate. Neither a remote
// manifest nor runtime environment variables can replace this identity.
//
//go:embed signing-certificate.der
var signingCertificate []byte

// Set only by an explicit development build; never in release builds.
var developmentBuild = "false"

var semver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func newer(a, b string) bool {
	var ax, ay, az, bx, by, bz int
	fmt.Sscanf(a, "%d.%d.%d", &ax, &ay, &az)
	fmt.Sscanf(b, "%d.%d.%d", &bx, &by, &bz)
	return ax > bx || ax == bx && (ay > by || ay == by && az > bz)
}
func Inspect(ctx context.Context, source, current string) (Feed, Check, error) {
	var feed Feed
	check := Check{Current: current, Platform: runtime.GOOS + "-" + runtime.GOARCH}
	reader, err := openSource(ctx, source)
	if err != nil {
		return feed, check, err
	}
	defer reader.Close()
	d := json.NewDecoder(io.LimitReader(reader, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&feed); err != nil {
		return feed, check, errors.New("invalid update manifest")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return feed, check, errors.New("invalid update manifest")
	}
	if !semver.MatchString(feed.Version) || !semver.MatchString(current) {
		return feed, check, errors.New("update versions require numeric major.minor.patch")
	}
	artifact, ok := feed.Artifacts[check.Platform]
	if !ok {
		return feed, check, errors.New("update has no binary for this platform")
	}
	hash, err := hex.DecodeString(artifact.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return feed, check, errors.New("update requires a SHA-256 digest")
	}
	check.Available = feed.Version
	check.Upgrade = newer(feed.Version, current)
	return feed, check, nil
}

func openSource(ctx context.Context, source string) (io.ReadCloser, error) {
	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
		u, err := url.Parse(source)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" {
			return nil, errors.New("invalid public update URL")
		}
		loopback := func(host string) bool {
			ip := net.ParseIP(host)
			return host == "localhost" || ip != nil && ip.IsLoopback()
		}
		if u.Scheme != "https" && !loopback(u.Hostname()) {
			return nil, errors.New("updates require HTTPS, except loopback verification")
		}
		client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("too many update redirects")
			}
			if req.URL.Scheme == "https" {
				return nil
			}
			if u.Scheme == "http" && req.URL.Host == u.Host {
				return nil
			}
			return errors.New("insecure update redirect")
		}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, errors.New("update download failed")
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return nil, fmt.Errorf("update HTTP status %d", response.StatusCode)
		}
		return response.Body, nil
	}
	return os.Open(source)
}

func artifactSource(manifest, artifact string) (string, error) {
	if strings.HasPrefix(artifact, "https://") || strings.HasPrefix(artifact, "http://") {
		return artifact, nil
	}
	if filepath.Base(artifact) != artifact || !(strings.HasSuffix(artifact, ".tar.gz") || strings.HasSuffix(artifact, ".zip")) {
		return "", errors.New("relative artifact must be a tar.gz or zip filename")
	}
	if strings.HasPrefix(manifest, "https://") || strings.HasPrefix(manifest, "http://") {
		u, err := url.Parse(manifest)
		if err != nil {
			return "", err
		}
		return u.ResolveReference(&url.URL{Path: artifact}).String(), nil
	}
	return filepath.Join(filepath.Dir(manifest), artifact), nil
}

// Validation is complete before the caller stops recording. The two renames
// preserve an app backup until replacement succeeds, on the same filesystem.
func Install(ctx context.Context, source, currentApp, currentVersion string, force bool, beforeSwap func() error) (Check, error) {
	if runtime.GOOS == "windows" {
		return installWindows(ctx, source, currentApp, currentVersion, force, beforeSwap)
	}
	feed, check, err := Inspect(ctx, source, currentVersion)
	if err != nil {
		return check, err
	}
	if !force && !check.Upgrade {
		return check, nil
	}
	if runtime.GOOS != "darwin" {
		return check, errors.New("native app update currently requires macOS")
	}
	if !strings.HasSuffix(currentApp, ".app") {
		return check, errors.New("updates require an installed app bundle")
	}
	temp, err := os.MkdirTemp(filepath.Dir(currentApp), ".daylog-update-")
	if err != nil {
		return check, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(temp)
		}
	}()
	artifact := feed.Artifacts[check.Platform]
	download, err := artifactSource(source, artifact.URL)
	if err != nil {
		return check, err
	}
	input, err := openSource(ctx, download)
	if err != nil {
		return check, err
	}
	defer input.Close()
	archive := filepath.Join(temp, "update.tar.gz")
	file, err := os.OpenFile(archive, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return check, err
	}
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input, (32<<20)+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || count > 32<<20 {
		return check, errors.New("update archive exceeds 32 MiB or is unreadable")
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(artifact.SHA256) {
		return check, errors.New("update checksum mismatch")
	}
	if err = extract(archive, temp); err != nil {
		return check, err
	}
	newApp := filepath.Join(temp, "Daylog.app")
	if err = verifyApp(ctx, newApp, feed.Version, developmentBuild == "true" && developmentSource(source)); err != nil {
		return check, err
	}
	if beforeSwap != nil {
		if err = beforeSwap(); err != nil {
			return check, err
		}
	}
	oldApp := filepath.Join(temp, "previous.app")
	if err = os.Rename(currentApp, oldApp); err != nil {
		return check, errors.New("cannot stage the installed app")
	}
	if err = os.Rename(newApp, currentApp); err != nil {
		if rollback := os.Rename(oldApp, currentApp); rollback != nil {
			keep = true
			return check, errors.New("replacement and rollback failed; app backup retained in update staging directory")
		}
		return check, errors.New("replacement failed; installed app restored")
	}
	return check, nil
}

func extract(archive, directory string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return errors.New("invalid gzip archive")
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	count := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("invalid tar archive")
		}
		count++
		if count > 1024 {
			return errors.New("too many archive entries")
		}
		name := filepath.Clean(header.Name)
		if name != "Daylog.app" && !strings.HasPrefix(name, "Daylog.app/") {
			return errors.New("archive path escaped app bundle")
		}
		if filepath.IsAbs(name) || strings.Contains(header.Name, "\\") {
			return errors.New("unsafe archive path")
		}
		path := filepath.Join(directory, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(path, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			total += header.Size
			if header.Size < 0 || total > 64<<20 {
				return errors.New("expanded app exceeds 64 MiB")
			}
			if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			mode := os.FileMode(0644)
			if header.Mode&0111 != 0 {
				mode = 0755
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return errors.New("duplicate or invalid archive file")
			}
			_, err = io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				return errors.New("incomplete archive file")
			}
		default:
			return errors.New("archive links and special files are not allowed")
		}
	}
}

func verifyApp(ctx context.Context, path, version string, allowAdHoc bool) error {
	if exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", path).Run() != nil {
		return errors.New("app code signature verification failed")
	}
	if err := verifyPublisher(ctx, path, allowAdHoc); err != nil {
		return err
	}
	plist := filepath.Join(path, "Contents", "Info.plist")
	for key, expected := range map[string]string{"CFBundleIdentifier": "io.daylog.agent", "CFBundleShortVersionString": version} {
		data, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", plist).Output()
		if err != nil || strings.TrimSpace(string(data)) != expected {
			return errors.New("update bundle identity or version mismatch")
		}
	}
	binary, err := macho.Open(filepath.Join(path, "Contents", "MacOS", "daylog"))
	if err != nil {
		return errors.New("update executable is not a Mach-O binary")
	}
	defer binary.Close()
	if runtime.GOARCH == "arm64" && binary.Cpu != macho.CpuArm64 || runtime.GOARCH == "amd64" && binary.Cpu != macho.CpuAmd64 {
		return errors.New("update executable architecture mismatch")
	}
	return nil
}

func developmentSource(source string) bool {
	u, err := url.Parse(source)
	if err != nil {
		return false
	}
	if u.Scheme == "" {
		return true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
}

func verifyPublisher(ctx context.Context, app string, allowAdHoc bool) error {
	if allowAdHoc {
		details, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--verbose=2", app).CombinedOutput()
		if err == nil && strings.Contains("\n"+string(details), "\nSignature=adhoc\n") {
			return nil
		}
	}
	// Apple requirement syntax uses SHA-1 certificate identifiers; the full
	// DER comparison below additionally pins the exact certificate bytes.
	fingerprint := sha1.Sum(signingCertificate)
	requirement := `=identifier "io.daylog.agent" and certificate leaf = H"` + hex.EncodeToString(fingerprint[:]) + `"`
	if exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R", requirement, app).Run() != nil {
		return errors.New("update publisher identity mismatch; installed app was not changed")
	}
	dir, err := os.MkdirTemp("", "daylog-signer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	prefix := filepath.Join(dir, "cert")
	if exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--extract-certificates="+prefix, app).Run() != nil {
		return errors.New("update signing certificate unavailable")
	}
	cert, err := os.ReadFile(prefix + "0")
	if err != nil || !bytes.Equal(cert, signingCertificate) {
		return errors.New("update signing certificate does not match pinned publisher")
	}
	return nil
}
