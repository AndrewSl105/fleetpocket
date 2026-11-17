package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/auth"
)

// LoginCodeTTL is how long a one-time login link from the CLI works.
const LoginCodeTTL = 60 * time.Second

// Local holds what only local mode (the jungle-monkey CLI) serves: the
// one-time login link instead of password + TOTP, and the control endpoints
// the CLI's `open`, `status` and `stop` call.
type Local struct {
	// Port is the loopback port; requests for any other Host are rejected.
	Port int
	// ControlToken authenticates the CLI (it reads it from config.json, 0600).
	ControlToken string
	// Status reports what `jungle-monkey status` prints.
	Status   func(ctx context.Context) map[string]any
	Shutdown func()

	codes *auth.Tickets
}

// IssueLoginCode returns a random, single-use code valid for LoginCodeTTL.
func (l *Local) IssueLoginCode() (string, error) {
	return l.loginCodes().Issue("local")
}

func (l *Local) loginCodes() *auth.Tickets {
	if l.codes == nil {
		l.codes = auth.NewTickets()
		l.codes.TTL = LoginCodeTTL
	}
	return l.codes
}

// LoginURL is the link the CLI opens in the browser.
func (l *Local) LoginURL(code string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/auth/local?code=%s", l.Port, code)
}

// LoginURLTo is LoginURL that lands on next (an app path such as
// /projects/site) after logging in.
func (l *Local) LoginURLTo(code, next string) string {
	u := l.LoginURL(code)
	if safeNext(next) {
		u += "&next=" + url.QueryEscape(next)
	}
	return u
}

// safeNext accepts only same-origin app paths ("/x", not "//host" or "/\host").
func safeNext(next string) bool {
	return strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n")
}

// hostAllowed rejects DNS rebinding: a page on evil.example resolving to
// 127.0.0.1 still sends Host: evil.example.
func (l *Local) hostAllowed(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil || p != fmt.Sprint(l.Port) {
		return false
	}
	return h == "127.0.0.1" || strings.EqualFold(h, "localhost")
}

func (s *Server) localHostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Local.hostAllowed(r.Host) {
			writeError(w, http.StatusMisdirectedRequest, "bad_host", "use http://127.0.0.1:"+fmt.Sprint(s.Local.Port))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// localLogin redeems a one-time code from the CLI, sets the session cookie
// and redirects to the app. GET, because the browser opens it as a link; the
// code is single-use and short-lived, so a leaked URL is worthless.
func (s *Server) localLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.Limiter.Blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, try again later")
		return
	}
	if _, ok := s.Local.loginCodes().Redeem(r.URL.Query().Get("code")); !ok {
		s.Limiter.Fail(ip)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintln(w, "This login link expired or was already used. Run `jungle-monkey open` for a new one.")
		return
	}
	if _, err := s.Auth.StartSession(r.Context(), w, "web"); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	dest := "/"
	if next := r.URL.Query().Get("next"); safeNext(next) {
		dest = next
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) requireControl(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || s.Local.ControlToken == "" ||
			subtle.ConstantTimeCompare([]byte(got), []byte(s.Local.ControlToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "control token required")
			return
		}
		next(w, r)
	}
}

func (s *Server) localLoginCode(w http.ResponseWriter, r *http.Request) {
	code, err := s.Local.IssueLoginCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "url": s.Local.LoginURL(code)})
}

func (s *Server) localStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	if s.Local.Status != nil {
		out = s.Local.Status(r.Context())
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) localShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	if s.Local.Shutdown != nil {
		go s.Local.Shutdown()
	}
}
