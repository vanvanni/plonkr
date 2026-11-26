package profile

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/hashicorp/hcl/v2"
)

var (
	repositoryLabel  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	sshLogin         = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`)
	scpURL           = regexp.MustCompile(`^git@(\[[^]]+\]|[^:@/]+):(.+)$`)
	remotePath       = regexp.MustCompile(`^[a-zA-Z0-9._~@+/-]+$`)
	dnsLabel         = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
	knownHostPattern = regexp.MustCompile(`^[a-zA-Z0-9_.*?-]+$`)
	windowsDrive     = regexp.MustCompile(`^[A-Za-z]:[/\\]`)
)

func decodeRepository(block *hcl.Block) (Repository, error) {
	name := block.Labels[0]
	if !repositoryLabel.MatchString(name) {
		return Repository{}, fmt.Errorf("invalid repository name %q", name)
	}
	fields, err := literalFields(block.Body, "url", "path", "user", "key", "ref", "known_hosts")
	if err != nil {
		return Repository{}, err
	}
	repo := Repository{
		Name: name, URL: fields.requiredString("url"), Path: fields.requiredString("path"),
		User: fields.requiredString("user"), Key: fields.optionalString("key", ""),
		Ref: fields.optionalString("ref", ""), KnownHosts: fields.strings("known_hosts"),
	}
	if fields.err != nil {
		return Repository{}, fields.err
	}
	if !validRepositoryURL(repo.URL) {
		return Repository{}, fmt.Errorf("repository url must be a credential-free ssh://, https:// or git@host:path URL")
	}
	if !safePOSIXPath(repo.Path) {
		return Repository{}, fmt.Errorf("repository path must be a safe absolute POSIX path")
	}
	if !ValidLinuxName(repo.User) {
		return Repository{}, fmt.Errorf("invalid Linux repository user %q", repo.User)
	}
	if _, set := fields.values["key"]; set {
		if !validKeyPath(repo.Key) {
			return Repository{}, fmt.Errorf("the private-key store was removed in v0.1.0; key must be an absolute local file path without traversal or control characters")
		}
	} else if !strings.HasPrefix(repo.URL, "https://") {
		return Repository{}, fmt.Errorf("key is required for SSH repository URLs")
	}
	if _, set := fields.values["ref"]; set && !validRepositoryRef(repo.Ref) {
		return Repository{}, fmt.Errorf("invalid repository ref %q", repo.Ref)
	}
	for i, line := range repo.KnownHosts {
		if !validKnownHostLine(line) {
			return Repository{}, fmt.Errorf("invalid known_hosts[%d]", i)
		}
	}
	return repo, nil
}

func validKeyPath(value string) bool {
	if strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	windows := windowsDrive.MatchString(value)
	if windows {
		value = strings.ReplaceAll(value[2:], "\\", "/")
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.HasSuffix(value, "/") || strings.ContainsRune(value, '\\') {
		return false
	}
	for part := range strings.SplitSeq(value[1:], "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
		if windows && (strings.ContainsAny(part, `<>:"|?*`) || strings.TrimRight(part, " .") != part) {
			return false
		}
	}
	return true
}

func validRepositoryURL(value string) bool {
	if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return false
	}
	if !strings.Contains(value, "://") {
		parts := scpURL.FindStringSubmatch(value)
		return len(parts) == 3 && validRepositoryHost(strings.Trim(parts[1], "[]")) && validRemotePath(parts[2])
	}
	if strings.ContainsRune(value, '#') {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "ssh" && parsed.Scheme != "https") ||
		!strings.HasPrefix(value, parsed.Scheme+"://") || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || !validRepositoryHost(parsed.Hostname()) ||
		!validRemotePath(parsed.EscapedPath()) {
		return false
	}
	if parsed.User != nil {
		_, password := parsed.User.Password()
		if parsed.Scheme != "ssh" || password || !sshLogin.MatchString(parsed.User.Username()) {
			return false
		}
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return false
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}

func validRepositoryHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) > 63 || !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func validRemotePath(value string) bool {
	if value == "/" || strings.HasPrefix(value, "-") || !remotePath.MatchString(value) {
		return false
	}
	for part := range strings.SplitSeq(value, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validRepositoryRef(ref string) bool {
	if ref == "" || ref == "@" || strings.HasPrefix(ref, "refs/") || strings.HasPrefix(ref, "-") || strings.HasSuffix(ref, ".") ||
		strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.ContainsAny(ref, ` ~^:?*[\`) ||
		strings.ContainsFunc(ref, unicode.IsControl) {
		return false
	}
	for part := range strings.SplitSeq(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func validKnownHostLine(line string) bool {
	if strings.ContainsFunc(line, func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) {
		return false
	}
	fields := strings.Fields(line)
	if len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
		if fields[0] != "@cert-authority" && fields[0] != "@revoked" {
			return false
		}
		fields = fields[1:]
	}
	if len(fields) < 3 || !validPublicKeyLine(strings.Join(fields[1:], " ")) {
		return false
	}

	pattern := fields[0]
	if strings.HasPrefix(pattern, "|") {
		parts := strings.Split(pattern, "|")
		if len(parts) != 4 || parts[1] != "1" {
			return false
		}
		for _, value := range parts[2:] {
			data, err := base64.StdEncoding.DecodeString(value)
			if err != nil || len(data) != 20 { // OpenSSH v1 uses HMAC-SHA1.
				return false
			}
		}
		return true
	}
	for host := range strings.SplitSeq(pattern, ",") {
		host = strings.TrimPrefix(host, "!")
		if strings.HasPrefix(host, "[") {
			name, port, err := net.SplitHostPort(host)
			number, portErr := strconv.Atoi(port)
			if err != nil || portErr != nil || number < 1 || number > 65535 {
				return false
			}
			host = name
		}
		if net.ParseIP(host) == nil && (host == "" || !knownHostPattern.MatchString(host)) {
			return false
		}
	}
	return true
}
