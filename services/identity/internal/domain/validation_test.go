package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

func TestNewRegistrationNormalizes(t *testing.T) {
	r, err := domain.NewRegistration("  Alice@Example.COM ", "correct horse", "  Alice Nguyen ")
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", r.Email)
	assert.Equal(t, "Alice Nguyen", r.FullName)
	assert.Equal(t, "correct horse", r.Password, "passwords are never trimmed")
}

func TestNewRegistrationAcceptsBoundaries(t *testing.T) {
	tests := []struct{ name, password, fullName string }{
		{"8-char password", "12345678", "A"},
		{"72-byte password", strings.Repeat("a", 72), "A"},
		{"8 multibyte chars", strings.Repeat("ệ", 8), "A"},
		{"100-char Vietnamese name", "password", strings.Repeat("ễ", 100)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewRegistration("a@example.com", tc.password, tc.fullName)
			assert.NoError(t, err)
		})
	}
}

func TestNewRegistrationRejects(t *testing.T) {
	const okPwd, okName, okEmail = "password1", "Alice", "a@example.com"
	tests := []struct{ name, email, password, fullName, field, msg string }{
		{"missing email", "", okPwd, okName, "email", "is required"},
		{"not an email", "not-an-email", okPwd, okName, "email", "must be a valid email"},
		{"display name form", "Alice <alice@example.com>", okPwd, okName, "email", "must be a valid email"},
		{"two at signs", "a@b@example.com", okPwd, okName, "email", "must be a valid email"},
		{"email too long", strings.Repeat("a", 250) + "@x.io", okPwd, okName, "email", "must be at most 254 characters"},
		{"missing password", okEmail, "", okName, "password", "is required"},
		{"short password", okEmail, "1234567", okName, "password", "must be at least 8 characters"},
		{"password over 72 bytes", okEmail, strings.Repeat("a", 73), okName, "password", "must be at most 72 bytes"},
		{"multibyte password over 72 bytes", okEmail, strings.Repeat("ệ", 25), okName, "password", "must be at most 72 bytes"},
		{"blank full name", okEmail, okPwd, "   ", "full_name", "is required"},
		{"full name too long", okEmail, okPwd, strings.Repeat("a", 101), "full_name", "must be at most 100 characters"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewRegistration(tc.email, tc.password, tc.fullName)
			var ve *domain.ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, map[string]string{tc.field: tc.msg}, ve.Fields)
		})
	}
}

func TestNewRegistrationReportsEveryField(t *testing.T) {
	_, err := domain.NewRegistration("", "", "")
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Len(t, ve.Fields, 3)
}

func TestNormalizeEmail(t *testing.T) {
	assert.Equal(t, "bob@example.com", domain.NormalizeEmail("\tBOB@example.com \n"))
}
