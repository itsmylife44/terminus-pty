package auth

import (
	"crypto/subtle"
	"net/http"
)

type BasicAuth struct {
	username string
	password string
}

func NewBasicAuth(username, password string) *BasicAuth {
	return &BasicAuth{
		username: username,
		password: password,
	}
}

func (a *BasicAuth) Authenticate(r *http.Request) bool {
	username, password, ok := r.BasicAuth()
	if !ok {
		return false
	}

	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1

	return usernameMatch && passwordMatch
}

func (a *BasicAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Authenticate(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="terminus-pty"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
