package github

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// GHTimeout bounds the call to the gh CLI, so a prompt or a hung helper cannot block a review.
const GHTimeout = 10 * time.Second

// Errors of the gh CLI, which the caller turns into the message a person acts on (REQ-129).
var (
	// ErrGHMissing says the gh CLI is not on the PATH.
	ErrGHMissing = errors.New("the gh CLI is not installed")
	// ErrGHLoggedOut says gh has no login for that host.
	ErrGHLoggedOut = errors.New("gh is not logged in for that host")
)

// GHToken returns the token that the machine's gh login holds for host. Speccy stores nothing
// it returns, so a token revoked or switched in gh takes effect at once (DEC-018, REQ-129).
// It is the one thing Speccy runs gh for: never git, never gh api, never gh repo clone.
func GHToken(ctx context.Context, host string) (string, error) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return "", ErrGHMissing
	}
	if host == "" {
		host = "github.com"
	}
	ctx, cancel := context.WithTimeout(ctx, GHTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "token", "--hostname", host)
	// gh must not stop on a prompt: it has no terminal here.
	cmd.Stdin = nil
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrGHLoggedOut
	}
	token := strings.TrimSpace(out.String())
	if token == "" {
		return "", ErrGHLoggedOut
	}
	return token, nil
}
