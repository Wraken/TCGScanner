package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func TestGoogleTokenRoundTrip(t *testing.T) {
	db := testDB(t)
	if _, _, ok, _ := loadGoogleToken(db); ok {
		t.Fatal("fresh db has no token")
	}
	tok := &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour).Truncate(time.Second)}
	if err := saveGoogleToken(db, tok, "florent@example.com"); err != nil {
		t.Fatal(err)
	}
	got, email, ok, err := loadGoogleToken(db)
	if err != nil || !ok || got.RefreshToken != "r" || email != "florent@example.com" || !got.Expiry.Equal(tok.Expiry) {
		t.Fatalf("%+v %q %v %v", got, email, ok, err)
	}
	clearGoogleToken(db)
	if _, _, ok, _ := loadGoogleToken(db); ok {
		t.Error("cleared")
	}
	if _, err := googleHTTPClient(context.Background(), db); !errors.Is(err, errSignedOut) {
		t.Errorf("no token must be errSignedOut, got %v", err)
	}
}

func TestConnectGoogleLoopbackFlow(t *testing.T) {
	var tokenReq url.Values
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			r.ParseForm()
			tokenReq = r.PostForm
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`)
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer at" {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"email":"florent@example.com"}`)
		}
	}))
	defer auth.Close()
	googleEndpoint = oauth2.Endpoint{AuthURL: auth.URL + "/auth", TokenURL: auth.URL + "/token"}
	googleUserinfoURL = auth.URL + "/userinfo"
	t.Cleanup(func() {
		googleEndpoint = google.Endpoint
		googleUserinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	})
	open := func(authURL string) error { // the "browser": Google would redirect to redirect_uri with the code and state
		u, _ := url.Parse(authURL)
		q := u.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("scope") == "" || !strings.Contains(q.Get("scope"), "drive.file") {
			t.Errorf("auth url %s", authURL)
		}
		if q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("code_challenge") == "" || !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") {
			t.Errorf("auth url %s", authURL)
		}
		go http.Get(q.Get("redirect_uri") + "?code=abc&state=" + q.Get("state"))
		return nil
	}
	tok, email, err := connectGoogle(context.Background(), open)
	if err != nil || tok.RefreshToken != "rt" || email != "florent@example.com" {
		t.Fatal(tok, email, err)
	}
	if tokenReq.Get("code") != "abc" || tokenReq.Get("code_verifier") == "" {
		t.Errorf("token request %v", tokenReq)
	}
	// A wrong state is refused.
	open = func(authURL string) error {
		u, _ := url.Parse(authURL)
		go http.Get(u.Query().Get("redirect_uri") + "?code=abc&state=wrong")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := connectGoogle(ctx, open); err == nil {
		t.Error("wrong state accepted")
	}
}

func TestGoogleHTTPClientRefreshesAndPersists(t *testing.T) {
	db := testDB(t)
	refreshed := 0
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshed++
		if refreshed > 1 {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"new","token_type":"Bearer","expires_in":3600}`)
	}))
	defer auth.Close()
	googleEndpoint = oauth2.Endpoint{AuthURL: auth.URL + "/auth", TokenURL: auth.URL + "/token"}
	t.Cleanup(func() { googleEndpoint = google.Endpoint })
	saveGoogleToken(db, &oauth2.Token{AccessToken: "old", RefreshToken: "rt", Expiry: time.Now().Add(-time.Hour)}, "x@example.com")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Header.Get("Authorization")) }))
	defer api.Close()
	c, err := googleHTTPClient(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	resp, _ := c.Get(api.URL)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "Bearer new" {
		t.Errorf("got %q", body)
	}
	if tok, email, _, _ := loadGoogleToken(db); tok.AccessToken != "new" || tok.RefreshToken != "rt" || email != "x@example.com" {
		t.Errorf("refreshed token must be persisted with its refresh token and email: access %q, refresh kept %v, email %q", tok.AccessToken, tok.RefreshToken == "rt", email)
	}
	saveGoogleToken(db, &oauth2.Token{AccessToken: "old", RefreshToken: "rt", Expiry: time.Now().Add(-time.Hour)}, "x@example.com")
	c, _ = googleHTTPClient(context.Background(), db)
	_, err = c.Get(api.URL)
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("a refused refresh must surface: %v", err)
	}
	if !errors.Is(err, errSignedOut) {
		t.Errorf("a refused refresh must be errSignedOut: %v", err)
	}
}

// A second consent may come back without a refresh token: refused, since the
// sign-in would strand an hour later.
func TestConnectGoogleRefusesATokenWithoutRefresh(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"at","token_type":"Bearer","expires_in":3600}`)
		case "/userinfo":
			fmt.Fprint(w, `{"email":"florent@example.com"}`)
		}
	}))
	defer auth.Close()
	googleEndpoint = oauth2.Endpoint{AuthURL: auth.URL + "/auth", TokenURL: auth.URL + "/token"}
	googleUserinfoURL = auth.URL + "/userinfo"
	t.Cleanup(func() {
		googleEndpoint = google.Endpoint
		googleUserinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	})
	open := func(authURL string) error {
		u, _ := url.Parse(authURL)
		q := u.Query()
		go http.Get(q.Get("redirect_uri") + "?code=abc&state=" + q.Get("state"))
		return nil
	}
	_, _, err := connectGoogle(context.Background(), open)
	if err == nil || !strings.Contains(err.Error(), "Google returned no refresh token") {
		t.Errorf("want the no refresh token error, got %v", err)
	}
}
