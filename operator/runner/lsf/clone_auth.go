package lsf

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// createCloneAuth keeps credentials out of shell source, the job environment,
// remote URLs and .netrc (whose old libcurl parser truncates long tokens).
// The directory is private to one clone and must be removed even in Debug mode.
func createCloneAuth(dir, remote, machine, username, password string) error {
	u, err := url.Parse(remote)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || !strings.EqualFold(u.Hostname(), machine) {
		return fmt.Errorf("lsf: clone credentials require a matching HTTP(S) remote without embedded credentials")
	}
	if strings.ContainsAny(username+password, "\x00\r\n") {
		return fmt.Errorf("lsf: clone credentials cannot contain NUL or line breaks")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(dir, "home"), 0700); err != nil {
		return err
	}
	for name, value := range map[string]string{"username": username, "password": password} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0600); err != nil {
			return err
		}
	}
	// Git uses English prompts under LC_ALL=C and omits the path when
	// credential.useHttpPath=false. Refuse prompts for unrelated hosts.
	userPrompt := "Username for '" + u.Scheme + "://" + u.Host + "': "
	passwordStart := "Password for '" + u.Scheme + "://"
	passwordEnd := "@" + u.Host + "': "
	script := "#!/bin/sh\nset +x\nset +v\nauth_dir=${0%/*}\ncase \"${1:-}\" in\n" +
		quote(userPrompt) + ") exec /bin/cat \"$auth_dir/username\" ;;\n" +
		quote(passwordStart) + "*" + quote(passwordEnd) + ") exec /bin/cat \"$auth_dir/password\" ;;\n" +
		"*) exit 1 ;;\nesac\n"
	return os.WriteFile(filepath.Join(dir, "askpass"), []byte(script), 0700)
}
