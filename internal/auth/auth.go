// Package auth handles OAuth 2.0 for installed apps (RFC 8252): a loopback
// redirect with PKCE, plus on-disk token persistence.
package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	tasks "google.golang.org/api/tasks/v1"

	"github.com/xnmp/gtasks-cli/internal/fsutil"
)

var ErrNotLoggedIn = errors.New("not logged in; run `gtasks auth login`")

// Paths locates the OAuth client credentials and the cached token.
type Paths struct {
	Credentials string
	Token       string
}

// DefaultPaths honours GTASKS_CONFIG_DIR and GTASKS_CREDENTIALS, falling back
// to the OS config directory (~/.config, ~/Library/Application Support, %AppData%).
func DefaultPaths() (Paths, error) {
	dir := os.Getenv("GTASKS_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return Paths{}, fmt.Errorf("locate config dir: %w", err)
		}
		dir = filepath.Join(base, "gtasks")
	}
	creds := os.Getenv("GTASKS_CREDENTIALS")
	if creds == "" {
		creds = filepath.Join(dir, "credentials.json")
	}
	return Paths{Credentials: creds, Token: filepath.Join(dir, "token.json")}, nil
}

// LoadConfig reads a Google "Desktop app" OAuth client JSON file.
func LoadConfig(path string) (*oauth2.Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("OAuth client file not found at %s; create a Desktop OAuth client in Google Cloud Console and save its JSON there (see README)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("read OAuth client file: %w", err)
	}
	cfg, err := google.ConfigFromJSON(b, tasks.TasksScope)
	if err != nil {
		return nil, fmt.Errorf("parse OAuth client file %s: %w", path, err)
	}
	return cfg, nil
}

func LoadToken(path string) (*oauth2.Token, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, fmt.Errorf("read token: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, fmt.Errorf("token file %s is corrupt (run `gtasks auth login`): %w", path, err)
	}
	if tok.RefreshToken == "" && tok.AccessToken == "" {
		return nil, fmt.Errorf("token file %s holds no credentials: %w", path, ErrNotLoggedIn)
	}
	return &tok, nil
}

// SaveToken writes the token atomically, owner-only on Unix.
func SaveToken(path string, tok *oauth2.Token) error {
	b, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(path, b, 0o600); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	return nil
}

// persistingSource writes refreshed tokens back to disk so the next run
// reuses them instead of refreshing again.
type persistingSource struct {
	base oauth2.TokenSource
	path string
	// warn reports a failure to persist; the fresh token is still used.
	warn func(error)
	mu   sync.Mutex
	last string
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.base.Token()
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.ErrorCode == "invalid_grant" {
		return nil, fmt.Errorf("saved login has expired or was revoked: %w", ErrNotLoggedIn)
	}
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok.AccessToken != p.last {
		p.last = tok.AccessToken
		if err := SaveToken(p.path, tok); err != nil && p.warn != nil {
			p.warn(err)
		}
	}
	return tok, nil
}

// Client returns an HTTP client authorised with the cached token. warn
// (may be nil) receives non-fatal token persistence failures.
func Client(ctx context.Context, paths Paths, warn func(error)) (*http.Client, error) {
	cfg, err := LoadConfig(paths.Credentials)
	if err != nil {
		return nil, err
	}
	tok, err := LoadToken(paths.Token)
	if err != nil {
		return nil, err
	}
	src := &persistingSource{base: cfg.TokenSource(ctx, tok), path: paths.Token, warn: warn, last: tok.AccessToken}
	return oauth2.NewClient(ctx, src), nil
}

// LoginOptions configures the interactive authorisation flow.
type LoginOptions struct {
	// OpenBrowser opens the consent URL; nil means print it only.
	OpenBrowser func(string) error
	// Paste, when non-nil, is read for a pasted redirect URL as an
	// alternative to the loopback callback (for SSH or headless sessions).
	Paste   io.Reader
	Out     io.Writer
	Timeout time.Duration
}

// Login runs the browser consent flow and returns the resulting token.
func Login(ctx context.Context, cfg *oauth2.Config, opts LoginOptions) (*oauth2.Token, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start loopback listener: %w", err)
	}
	c := *cfg
	c.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port)

	state, err := randomState()
	if err != nil {
		ln.Close()
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()
	authURL := c.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(verifier),
		// Force consent so Google always issues a refresh token, even on re-login.
		oauth2.SetAuthURLParam("prompt", "consent"))

	fmt.Fprintf(opts.Out, "Open this URL to authorise gtasks:\n\n  %s\n\n", authURL)
	if opts.OpenBrowser != nil {
		if err := opts.OpenBrowser(authURL); err != nil {
			fmt.Fprintf(opts.Out, "(could not open a browser: %v)\n", err)
		}
	}
	if opts.Paste != nil {
		fmt.Fprintln(opts.Out, "If the browser cannot reach this machine, paste the URL it was redirected to:")
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	code, err := awaitCode(ctx, ln, state, opts.Paste, opts.Out)
	if err != nil {
		return nil, err
	}
	tok, err := c.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchange authorisation code: %w", err)
	}
	return tok, nil
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ErrStateMismatch marks a request that is not the callback for this login
// attempt (missing or wrong state). Such requests are ignored rather than
// aborting, so stray or forged requests to the loopback port cannot end it.
var ErrStateMismatch = errors.New("not the OAuth callback for this login (state mismatch)")

// ParseCallback extracts the authorisation code from redirect query
// parameters. The anti-CSRF state is checked before anything else.
func ParseCallback(q url.Values, wantState string) (string, error) {
	if q.Get("state") != wantState {
		return "", ErrStateMismatch
	}
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("authorisation denied: %s", e)
	}
	if q.Get("code") == "" {
		return "", errors.New("redirect carried no authorisation code")
	}
	return q.Get("code"), nil
}

type result struct {
	code string
	err  error
}

// offer delivers r unless a result is already pending; only the first wins.
func offer(ch chan<- result, r result) {
	select {
	case ch <- r:
	default:
	}
}

// awaitCode serves the loopback redirect (and optionally reads pasted
// redirect URLs) until a state-matched callback arrives or ctx ends. Requests
// without the right state are rejected and waiting continues.
func awaitCode(ctx context.Context, ln net.Listener, state string, paste io.Reader, out io.Writer) (string, error) {
	results := make(chan result, 1)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			code, err := ParseCallback(r.URL.Query(), state)
			if errors.Is(err, ErrStateMismatch) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				fmt.Fprintln(w, "gtasks is authorised. You can close this tab.")
			}
			offer(results, result{code, err})
		}),
	}
	go srv.Serve(ln) //nolint:errcheck // returns ErrServerClosed on Shutdown
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	if paste != nil {
		go readPasted(paste, state, out, results)
	}

	select {
	case r := <-results:
		return r.code, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("waiting for authorisation: %w", ctx.Err())
	}
}

// readPasted reads redirect URLs line by line. Unusable lines are reported
// and reading continues; at EOF it stops and the loopback keeps waiting.
func readPasted(r io.Reader, state string, out io.Writer, results chan<- result) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			fmt.Fprintf(out, "Not a URL (%v); paste the full redirect URL:\n", err)
			continue
		}
		code, err := ParseCallback(u.Query(), state)
		if errors.Is(err, ErrStateMismatch) {
			fmt.Fprintln(out, "That URL is not the redirect for this login; paste the full http://127.0.0.1:... URL:")
			continue
		}
		offer(results, result{code, err})
		return
	}
}

// Revoke invalidates the token at Google (refresh token if present).
func Revoke(ctx context.Context, tok *oauth2.Token) error {
	t := tok.RefreshToken
	if t == "" {
		t = tok.AccessToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/revoke",
		strings.NewReader(url.Values{"token": {t}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("revoke: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
