package ssh

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Dart147/SMC/deploy/internal/config"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// hostKeyLine builds a single known_hosts line for the given host label using a
// freshly generated key of the requested type. Returns the line and the SSH
// public-key algorithm name (e.g. "ssh-ed25519", "ecdsa-sha2-nistp256").
func hostKeyLine(t *testing.T, hostLabel, keyType string) (string, string) {
	t.Helper()

	var pub ssh.PublicKey
	switch keyType {
	case "ed25519":
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate ed25519: %v", err)
		}
		pub, err = ssh.NewPublicKey(priv.Public())
		if err != nil {
			t.Fatalf("ssh ed25519 pubkey: %v", err)
		}
	case "ecdsa":
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate ecdsa: %v", err)
		}
		pub, err = ssh.NewPublicKey(&priv.PublicKey)
		if err != nil {
			t.Fatalf("ssh ecdsa pubkey: %v", err)
		}
	default:
		t.Fatalf("unsupported key type %q", keyType)
	}

	// "<host> <keytype> <base64>\n"
	line := hostLabel + " " + string(ssh.MarshalAuthorizedKey(pub))
	return line, pub.Type()
}

func writeKnownHosts(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	content := strings.Join(lines, "")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	return path
}

func newTestClient(t *testing.T, sshCfg config.SSHConfig) *Client {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	return NewClient(sshCfg, logger)
}

func TestCreateHostKeyCallback_StrictDisabled(t *testing.T) {
	c := newTestClient(t, config.SSHConfig{StrictHostKeyChecking: false})

	cb, algos, err := c.createHostKeyCallback("example.com:22")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cb == nil {
		t.Fatal("expected a non-nil host key callback")
	}
	// With strict checking off we let the SSH client use its defaults.
	if algos != nil {
		t.Fatalf("expected nil HostKeyAlgorithms, got %v", algos)
	}
}

// The regression this fixes: a host that offers multiple key types but whose
// known_hosts only records one. HostKeyAlgorithms must restrict negotiation to
// the recorded type so the handshake never picks an unrecorded key (which the
// stdlib surfaced as the misleading "knownhosts: key mismatch").
func TestCreateHostKeyCallback_AlgorithmsMatchRecordedKeys(t *testing.T) {
	tests := []struct {
		name      string
		keyTypes  []string
		wantAlgos []string // SSH public-key algorithm names that must be present
		denyAlgos []string // algorithm names that must NOT be present
	}{
		{
			name:      "only ecdsa recorded",
			keyTypes:  []string{"ecdsa"},
			wantAlgos: []string{ssh.KeyAlgoECDSA256},
			denyAlgos: []string{ssh.KeyAlgoED25519},
		},
		{
			name:      "only ed25519 recorded",
			keyTypes:  []string{"ed25519"},
			wantAlgos: []string{ssh.KeyAlgoED25519},
			denyAlgos: []string{ssh.KeyAlgoECDSA256},
		},
		{
			name:      "both recorded",
			keyTypes:  []string{"ecdsa", "ed25519"},
			wantAlgos: []string{ssh.KeyAlgoECDSA256, ssh.KeyAlgoED25519},
		},
	}

	const hostLabel = "example.com" // port 22 is stored bare in known_hosts
	const dialAddr = "example.com:22"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lines []string
			for _, kt := range tt.keyTypes {
				line, _ := hostKeyLine(t, hostLabel, kt)
				lines = append(lines, line)
			}
			khPath := writeKnownHosts(t, lines...)

			c := newTestClient(t, config.SSHConfig{
				StrictHostKeyChecking: true,
				KnownHostsFile:        khPath,
			})

			cb, algos, err := c.createHostKeyCallback(dialAddr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cb == nil {
				t.Fatal("expected a non-nil host key callback")
			}
			for _, want := range tt.wantAlgos {
				if !slices.Contains(algos, want) {
					t.Errorf("expected algorithms %v to contain %q", algos, want)
				}
			}
			for _, deny := range tt.denyAlgos {
				if slices.Contains(algos, deny) {
					t.Errorf("algorithms %v must not contain unrecorded type %q", algos, deny)
				}
			}
		})
	}
}

// wrapCommand must keep the inherited PATH so host-specific binary locations
// (e.g. /snap/bin for snap-installed docker) stay reachable — the regression
// that caused `docker: command not found` (exit 127).
func TestWrapCommand(t *testing.T) {
	c := newTestClient(t, config.SSHConfig{})

	got := c.wrapCommand("docker compose up -d")

	// PATH is prepended with the base dirs AND keeps the inherited $PATH.
	wantPATH := `export PATH=` + basePathPrefix + `:"$PATH" &&`
	if !strings.Contains(got, wantPATH) {
		t.Errorf("expected wrapped command to preserve inherited PATH via %q, got: %s", wantPATH, got)
	}
	// The inherited PATH must not be discarded.
	if !strings.Contains(got, `:"$PATH"`) {
		t.Errorf("inherited PATH was clobbered, got: %s", got)
	}
	// The original command survives, inside an sh -c invocation.
	if !strings.HasPrefix(got, "sh -c '") {
		t.Errorf("expected sh -c wrapping, got: %s", got)
	}
	if !strings.Contains(got, "docker compose up -d") {
		t.Errorf("original command missing, got: %s", got)
	}
}

func TestWrapCommand_EscapesSingleQuotes(t *testing.T) {
	c := newTestClient(t, config.SSHConfig{})

	// A command containing a single quote must be safely escaped so the
	// outer sh -c '...' wrapping is not broken out of.
	got := c.wrapCommand(`echo 'hi'`)

	if !strings.HasPrefix(got, "sh -c '") || !strings.HasSuffix(got, "'") {
		t.Errorf("expected balanced sh -c quoting, got: %s", got)
	}
	if !strings.Contains(got, `'\''`) {
		t.Errorf("expected single quotes to be escaped as '\\'', got: %s", got)
	}
}

func TestCreateHostKeyCallback_CreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")

	c := newTestClient(t, config.SSHConfig{
		StrictHostKeyChecking: true,
		KnownHostsFile:        khPath,
	})

	if _, _, err := c.createHostKeyCallback("example.com:22"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(khPath); err != nil {
		t.Fatalf("expected known_hosts file to be created: %v", err)
	}
}
