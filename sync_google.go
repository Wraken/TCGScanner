package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// ── Google sign-in (installed-app OAuth: loopback redirect + PKCE) ─────────

// googleClientID and googleClientSecret are filled by the owner from the
// Google Cloud console (APIs & Services, Credentials, OAuth client of type
// "Desktop app"). Google treats both as public for installed apps: they ship
// inside the binary, and PKCE is what protects the flow.
const googleClientID = "PASTE-FROM-CLOUD-CONSOLE.apps.googleusercontent.com"
const googleClientSecret = "PASTE-FROM-CLOUD-CONSOLE"

var googleScopes = []string{"https://www.googleapis.com/auth/drive.file", "openid", "email"}

// googleEndpoint and googleUserinfoURL are variables so tests can point them
// at a fake server; they are read at call time.
var googleEndpoint = google.Endpoint
var googleUserinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

const (
	settingSyncGoogleToken = "sync_google_token"
	settingSyncGoogleEmail = "sync_google_email"
	// The account last signed in, kept through a disconnect or a refused
	// refresh: connecting another account starts the sync afresh.
	settingSyncGoogleLastEmail = "sync_google_last_email"
)

// googleSignInTimeout bounds the whole sign-in, browser wait included.
const googleSignInTimeout = 3 * time.Minute

var errSignedOut = errors.New("signed out of Google")

// googleConfigured reports whether the owner has pasted a real client id.
func googleConfigured() bool {
	return !strings.HasPrefix(googleClientID, "PASTE-")
}

func googleOAuthConfig(redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     googleClientID,
		ClientSecret: googleClientSecret,
		Endpoint:     googleEndpoint,
		Scopes:       googleScopes,
		RedirectURL:  redirectURL,
	}
}

// connectGoogle runs the installed-app flow: a loopback listener on
// 127.0.0.1:0, PKCE (oauth2.GenerateVerifier), state, open(authURL) in the
// browser, waits for the redirect (timeout 3 min), exchanges the code,
// fetches the email.
func connectGoogle(ctx context.Context, open func(url string) error) (*oauth2.Token, string, error) {
	ctx, cancel := context.WithTimeout(ctx, googleSignInTimeout)
	defer cancel()

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, "", fmt.Errorf("google sign-in: state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)
	verifier := oauth2.GenerateVerifier()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("google sign-in: listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	conf := googleOAuthConfig(fmt.Sprintf("http://127.0.0.1:%d/", port))

	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	deliver := func(r result) {
		select {
		case results <- r:
		default: // a result is already pending
		}
	}
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			if q.Get("state") != state {
				http.Error(w, "Invalid sign-in request.", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if e := q.Get("error"); e != "" {
				fmt.Fprintf(w, "<!doctype html><p>Sign-in failed (%s). You can close this tab and return to TCGScanner.</p>", html.EscapeString(e))
				deliver(result{err: fmt.Errorf("google sign-in refused: %s", e)})
				return
			}
			code := q.Get("code")
			if code == "" {
				http.Error(w, "Missing authorization code.", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, "<!doctype html><p>You can close this tab and return to TCGScanner.</p>")
			deliver(result{code: code})
		}),
	}
	go srv.Serve(ln)
	defer srv.Close() // closes ln too

	authURL := conf.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier))
	if err := open(authURL); err != nil {
		return nil, "", fmt.Errorf("google sign-in: open browser: %w", err)
	}

	var code string
	select {
	case <-ctx.Done():
		return nil, "", fmt.Errorf("google sign-in: no answer from the browser: %w", ctx.Err())
	case r := <-results:
		if r.err != nil {
			return nil, "", r.err
		}
		code = r.code
	}

	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, "", fmt.Errorf("google sign-in: exchange: %w", err)
	}
	email, err := googleEmail(ctx, tok.AccessToken)
	if err != nil {
		return nil, "", err
	}
	return tok, email, nil
}

// googleEmail asks the userinfo endpoint which account the token belongs to.
func googleEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserinfoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("google sign-in: userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google sign-in: userinfo: HTTP %d", resp.StatusCode)
	}
	var info struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("google sign-in: userinfo: %w", err)
	}
	return info.Email, nil
}

// ── Token storage (settings table) ─────────────────────────────────────────

func saveGoogleToken(db *sql.DB, tok *oauth2.Token, email string) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	if err := setSetting(db, settingSyncGoogleToken, string(b)); err != nil {
		return err
	}
	if err := setSetting(db, settingSyncGoogleEmail, email); err != nil {
		return err
	}
	return setSetting(db, settingSyncGoogleLastEmail, email)
}

// googleAccountChanged reports whether email is not the account this device
// last synced with (true on a first connection).
func googleAccountChanged(db *sql.DB, email string) (bool, error) {
	last, _, err := getSetting(db, settingSyncGoogleLastEmail)
	if err != nil {
		return false, err
	}
	if last == "" { // signed in before the setting existed
		if last, _, err = getSetting(db, settingSyncGoogleEmail); err != nil {
			return false, err
		}
	}
	return last == "" || !strings.EqualFold(last, email), nil
}

func loadGoogleToken(db *sql.DB) (tok *oauth2.Token, email string, ok bool, err error) {
	raw, ok, err := getSetting(db, settingSyncGoogleToken)
	if err != nil || !ok {
		return nil, "", false, err
	}
	tok = &oauth2.Token{}
	if err := json.Unmarshal([]byte(raw), tok); err != nil {
		return nil, "", false, fmt.Errorf("stored Google token: %w", err)
	}
	email, _, err = getSetting(db, settingSyncGoogleEmail)
	if err != nil {
		return nil, "", false, err
	}
	return tok, email, true, nil
}

func clearGoogleToken(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM settings WHERE key IN (?, ?)`, settingSyncGoogleToken, settingSyncGoogleEmail)
	return err
}

// ── Authenticated HTTP client ──────────────────────────────────────────────

// googleHTTPClient returns a client whose transport refreshes the token and
// persists the refreshed one; errSignedOut when there is no token or the
// refresh is refused (invalid_grant).
func googleHTTPClient(ctx context.Context, db *sql.DB) (*http.Client, error) {
	tok, email, ok, err := loadGoogleToken(db)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("google: no stored token: %w", errSignedOut)
	}
	conf := googleOAuthConfig("")
	src := &persistingTokenSource{
		base:       oauth2.ReuseTokenSource(tok, conf.TokenSource(ctx, tok)),
		db:         db,
		email:      email,
		lastAccess: tok.AccessToken,
	}
	return &http.Client{Transport: &oauth2.Transport{Source: src}}, nil
}

// persistingTokenSource saves every new access token to the settings table
// and maps a refused refresh to errSignedOut.
type persistingTokenSource struct {
	base  oauth2.TokenSource
	db    *sql.DB
	email string

	mu         sync.Mutex
	lastAccess string
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.base.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && (re.ErrorCode == "invalid_grant" || strings.Contains(string(re.Body), "invalid_grant")) {
			return nil, fmt.Errorf("%w: %v", errSignedOut, err)
		}
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok.AccessToken != s.lastAccess {
		if err := saveGoogleToken(s.db, tok, s.email); err != nil {
			return nil, fmt.Errorf("google: persist refreshed token: %w", err)
		}
		s.lastAccess = tok.AccessToken
	}
	return tok, nil
}
