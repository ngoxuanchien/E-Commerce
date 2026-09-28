package domain

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// Input limits (WS1 §3).
const (
	MaxEmailLength    = 254
	MinPasswordLength = 8
	// MaxPasswordBytes is bcrypt's input limit; longer input would be silently truncated.
	MaxPasswordBytes  = 72
	MaxFullNameLength = 100
)

// NormalizeEmail trims surrounding whitespace and lowercases an email address.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Registration is validated, normalized sign-up input.
type Registration struct {
	Email    string
	Password string
	FullName string
}

// NewRegistration normalizes and validates sign-up input. Every invalid field
// is reported at once in a *ValidationError.
func NewRegistration(email, password, fullName string) (Registration, error) {
	r := Registration{Email: NormalizeEmail(email), Password: password, FullName: strings.TrimSpace(fullName)}
	fields := map[string]string{}
	if msg := emailProblem(r.Email); msg != "" {
		fields["email"] = msg
	}
	if msg := passwordProblem(r.Password); msg != "" {
		fields["password"] = msg
	}
	if msg := fullNameProblem(r.FullName); msg != "" {
		fields["full_name"] = msg
	}
	if len(fields) > 0 {
		return Registration{}, &ValidationError{Fields: fields}
	}
	return r, nil
}

func emailProblem(e string) string {
	if e == "" {
		return "is required"
	}
	if len(e) > MaxEmailLength {
		return fmt.Sprintf("must be at most %d characters", MaxEmailLength)
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Name != "" || addr.Address != e {
		return "must be a valid email"
	}
	return ""
}

func passwordProblem(p string) string {
	switch {
	case p == "":
		return "is required"
	case utf8.RuneCountInString(p) < MinPasswordLength:
		return fmt.Sprintf("must be at least %d characters", MinPasswordLength)
	case len(p) > MaxPasswordBytes:
		return fmt.Sprintf("must be at most %d bytes", MaxPasswordBytes)
	}
	return ""
}

func fullNameProblem(n string) string {
	switch {
	case n == "":
		return "is required"
	case utf8.RuneCountInString(n) > MaxFullNameLength:
		return fmt.Sprintf("must be at most %d characters", MaxFullNameLength)
	}
	return ""
}
