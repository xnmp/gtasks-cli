package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestSaveLoadTokenRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	want := &oauth2.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		Expiry:       time.Date(2026, 10, 15, 12, 30, 0, 0, time.UTC),
	}
	if err := SaveToken(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken ||
		got.TokenType != want.TokenType || !got.Expiry.Equal(want.Expiry) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSaveTokenFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions not supported on windows")
	}
	path := filepath.Join(t.TempDir(), "token.json")
	if err := SaveToken(path, &oauth2.Token{RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
}

func TestSaveTokenCreatesParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "token.json")
	if err := SaveToken(path, &oauth2.Token{RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err != nil {
		t.Errorf("load after save: %v", err)
	}
}

func TestSaveTokenOverwritesAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	if err := SaveToken(path, &oauth2.Token{AccessToken: "old", RefreshToken: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveToken(path, &oauth2.Token{AccessToken: "new", RefreshToken: "r2"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken(path)
	if err != nil || got.AccessToken != "new" || got.RefreshToken != "r2" {
		t.Errorf("got %+v, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want only the token file", len(entries))
	}
}

func TestLoadTokenMissingFile(t *testing.T) {
	_, err := LoadToken(filepath.Join(t.TempDir(), "nope.json"))
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("err = %v, want ErrNotLoggedIn", err)
	}
}

func TestLoadTokenBadContents(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("corrupt JSON", func(t *testing.T) {
		for _, c := range []string{"{not json", "", "\x00\x01\x02", `["a"]`, `"str"`} {
			tok, err := LoadToken(write("c.json", c))
			if err == nil {
				t.Errorf("content %q: want error, got %+v", c, tok)
			}
			if errors.Is(err, ErrNotLoggedIn) && c != "" {
				// corrupt files should be reported as corrupt, not as logged out
				t.Errorf("content %q: corrupt file reported as ErrNotLoggedIn", c)
			}
		}
	})
	t.Run("empty object", func(t *testing.T) {
		for _, c := range []string{`{}`, `{"token_type":"Bearer"}`, `null`} {
			if _, err := LoadToken(write("e.json", c)); !errors.Is(err, ErrNotLoggedIn) {
				t.Errorf("content %q: err = %v, want ErrNotLoggedIn", c, err)
			}
		}
	})
	t.Run("directory in place of file", func(t *testing.T) {
		if _, err := LoadToken(dir); err == nil || errors.Is(err, ErrNotLoggedIn) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestParseCallback(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		want    string
		wantErr string
		isMism  bool
	}{
		{"valid", "code=abc&state=S", "abc", "", false},
		{"state mismatch", "code=abc&state=OTHER", "", "", true},
		{"missing state", "code=abc", "", "", true},
		{"error without state is a mismatch", "error=access_denied", "", "", true},
		{"error with wrong state is a mismatch", "error=access_denied&state=EVIL", "", "", true},
		{"access denied with right state", "error=access_denied&state=S", "", "access_denied", false},
		{"error wins over code", "error=access_denied&code=abc&state=S", "", "access_denied", false},
		{"missing code", "state=S", "", "no authorisation code", false},
		{"nothing", "", "", "", true},
		{"unrelated params", "foo=bar", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, _ := url.ParseQuery(tc.query)
			got, err := ParseCallback(q, "S")
			if tc.wantErr == "" && !tc.isMism {
				if err != nil || got != tc.want {
					t.Errorf("got %q, %v; want %q", got, err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if errors.Is(err, ErrStateMismatch) != tc.isMism {
				t.Errorf("errors.Is(ErrStateMismatch) = %v, want %v (err %v)", !tc.isMism, tc.isMism, err)
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tc.wantErr)
			}
			if got != "" {
				t.Errorf("code %q returned alongside an error", got)
			}
		})
	}
}

func startAwait(t *testing.T, ctx context.Context, paste io.Reader) (base string, res <-chan [2]any) {
	t.Helper()
	return startAwaitOut(t, ctx, paste, io.Discard)
}

func startAwaitOut(t *testing.T, ctx context.Context, paste io.Reader, out io.Writer) (base string, res <-chan [2]any) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan [2]any, 1)
	go func() {
		code, err := awaitCode(ctx, ln, "STATE", paste, out)
		ch <- [2]any{code, err}
	}()
	return "http://" + ln.Addr().String(), ch
}

func wait(t *testing.T, ch <-chan [2]any) (string, error) {
	t.Helper()
	select {
	case r := <-ch:
		err, _ := r[1].(error)
		return r[0].(string), err
	case <-time.After(5 * time.Second):
		t.Fatal("awaitCode did not return")
		return "", nil
	}
}

func get(t *testing.T, u string) *http.Response {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestAwaitCodeLoopbackSuccess(t *testing.T) {
	base, ch := startAwait(t, context.Background(), nil)
	resp := get(t, base+"/?code=THECODE&state=STATE")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	code, err := wait(t, ch)
	if err != nil || code != "THECODE" {
		t.Errorf("got %q, %v", code, err)
	}
}

func TestAwaitCodeIgnoresWrongStateAndKeepsWaiting(t *testing.T) {
	base, ch := startAwait(t, context.Background(), nil)
	for _, q := range []string{"?code=EVIL&state=BAD", "?code=EVIL", "?error=access_denied", "?error=access_denied&state=BAD"} {
		if resp := get(t, base+"/"+q); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, resp.StatusCode)
		}
	}
	select {
	case r := <-ch:
		t.Fatalf("forged request ended the wait: %v", r)
	case <-time.After(100 * time.Millisecond):
	}
	get(t, base+"/?code=GOOD&state=STATE")
	code, err := wait(t, ch)
	if err != nil || code != "GOOD" {
		t.Errorf("got %q, %v", code, err)
	}
}

func TestAwaitCodeIgnoresStrayRequests(t *testing.T) {
	base, ch := startAwait(t, context.Background(), nil)
	if resp := get(t, base+"/favicon.ico"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("favicon status = %d, want 404", resp.StatusCode)
	}
	get(t, base+"/") // no params: not a callback
	select {
	case r := <-ch:
		t.Fatalf("returned early on stray requests: %v", r)
	case <-time.After(100 * time.Millisecond):
	}
	get(t, base+"/?code=OK&state=STATE")
	code, err := wait(t, ch)
	if err != nil || code != "OK" {
		t.Errorf("got %q, %v", code, err)
	}
}

func TestAwaitCodeAccessDeniedOnLoopback(t *testing.T) {
	base, ch := startAwait(t, context.Background(), nil)
	get(t, base+"/?error=access_denied&state=STATE")
	if _, err := wait(t, ch); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("err = %v", err)
	}
}

func TestAwaitCodePastedURL(t *testing.T) {
	paste := strings.NewReader("\n  http://127.0.0.1:9999/?code=PASTED&state=STATE \n")
	_, ch := startAwait(t, context.Background(), paste)
	code, err := wait(t, ch)
	if err != nil || code != "PASTED" {
		t.Errorf("got %q, %v", code, err)
	}
}

func TestAwaitCodePastedBadLinesAreReportedAndSkipped(t *testing.T) {
	paste := strings.NewReader(strings.Join([]string{
		"http://[::1",
		"http://127.0.0.1/?code=X&state=EVIL",
		"http://127.0.0.1/",
		"http://127.0.0.1/?code=GOOD&state=STATE",
	}, "\n") + "\n")
	var out bytes.Buffer
	_, ch := startAwaitOut(t, context.Background(), paste, &syncWriter{w: &out})
	code, err := wait(t, ch)
	if err != nil || code != "GOOD" {
		t.Fatalf("got %q, %v", code, err)
	}
	syncOut := out.String()
	if n := strings.Count(syncOut, "\n"); n < 3 {
		t.Errorf("expected a message for each of the 3 bad lines, got %q", syncOut)
	}
}

func TestAwaitCodePastedDeniedWithRightStateFails(t *testing.T) {
	_, ch := startAwait(t, context.Background(), strings.NewReader("http://127.0.0.1/?error=access_denied&state=STATE\n"))
	if _, err := wait(t, ch); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("err = %v", err)
	}
}

func TestAwaitCodePasteEOFDoesNotStopLoopback(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "only bad line": "nonsense\n"} {
		t.Run(name, func(t *testing.T) {
			base, ch := startAwait(t, context.Background(), strings.NewReader(in))
			select {
			case r := <-ch:
				t.Fatalf("returned at paste EOF: %v", r)
			case <-time.After(100 * time.Millisecond):
			}
			get(t, base+"/?code=VIA_LOOPBACK&state=STATE")
			if code, err := wait(t, ch); err != nil || code != "VIA_LOOPBACK" {
				t.Errorf("got %q, %v", code, err)
			}
		})
	}
}

// syncWriter serialises writes from the paste goroutine.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

type stubSource struct {
	tok *oauth2.Token
	err error
}

func (s stubSource) Token() (*oauth2.Token, error) { return s.tok, s.err }

func TestPersistingSourceInvalidGrantMeansNotLoggedIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	src := &persistingSource{base: stubSource{err: &oauth2.RetrieveError{ErrorCode: "invalid_grant"}}, path: path}
	if _, err := src.Token(); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("err = %v, want ErrNotLoggedIn", err)
	}
	// Other failures are passed through unchanged.
	boom := errors.New("network down")
	src = &persistingSource{base: stubSource{err: boom}, path: path}
	if _, err := src.Token(); !errors.Is(err, boom) || errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("err = %v", err)
	}
	other := &persistingSource{base: stubSource{err: &oauth2.RetrieveError{ErrorCode: "server_error"}}, path: path}
	if _, err := other.Token(); err == nil || errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("non-invalid_grant error mapped to ErrNotLoggedIn: %v", err)
	}
}

func TestPersistingSourceSavesRefreshedTokenOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	src := &persistingSource{base: stubSource{tok: &oauth2.Token{AccessToken: "fresh", RefreshToken: "r"}}, path: path, last: "stale"}
	if _, err := src.Token(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken(path)
	if err != nil || got.AccessToken != "fresh" {
		t.Errorf("persisted = %+v, %v", got, err)
	}
	// Unchanged access token: file is not rewritten.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("token rewritten although access token was unchanged")
	}
}

func TestAwaitCodeContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, ch := startAwait(t, ctx, nil)
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	cancel()
	code, err := wait(t, ch)
	if !errors.Is(err, context.Canceled) || code != "" {
		t.Errorf("got %q, %v; want context.Canceled", code, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v to return after cancel", d)
	}
}

func TestAwaitCodeTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, ch := startAwait(t, ctx, nil)
	if _, err := wait(t, ch); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

func TestPersistingSourceSaveFailureWarnsButReturnsToken(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var warned []error
	src := &persistingSource{
		base: stubSource{tok: &oauth2.Token{AccessToken: "fresh", RefreshToken: "r"}},
		path: filepath.Join(blocker, "token.json"), // parent is a file: save must fail
		warn: func(err error) { warned = append(warned, err) },
	}
	tok, err := src.Token()
	if err != nil || tok == nil || tok.AccessToken != "fresh" {
		t.Fatalf("Token() = %+v, %v; want the fresh token despite save failure", tok, err)
	}
	if len(warned) != 1 {
		t.Errorf("warn called %d times, want 1", len(warned))
	}
	// A nil warn must not panic.
	src.warn, src.last = nil, ""
	if _, err := src.Token(); err != nil {
		t.Errorf("nil warn: %v", err)
	}
}
