package session

import (
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/ratelimiter"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"
)

// NewSSH builds the SSH server around s. The host key at hostKeyPath is
// created on first start and reused afterwards. connRate is the number of new
// sessions per second allowed per client address (burst 10).
func NewSSH(s *Server, addr, hostKeyPath string, connRate float64) (*ssh.Server, error) {
	return wish.NewServer(
		wish.WithAddress(addr),
		wish.WithHostKeyPath(hostKeyPath),
		// Accept everyone: a public key becomes the account, and clients with
		// no key fall through to a prompt-free keyboard-interactive login.
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		wish.WithKeyboardInteractiveAuth(func(ssh.Context, gossh.KeyboardInteractiveChallenge) bool { return true }),
		wish.WithIdleTimeout(10*time.Minute),
		wish.WithMaxTimeout(2*time.Hour),
		wish.WithMiddleware(
			func(ssh.Handler) ssh.Handler { return s.Handle },
			ratelimiter.Middleware(ratelimiter.NewRateLimiter(rate.Limit(connRate), 10, 4096)),
		),
	)
}
