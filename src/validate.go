package src

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	usernameMinLen = 3
	usernameMaxLen = 39
	emailMaxLen    = 254 // RFC 5321 forward-path limit
	passwordMinLen = 8
	passwordMaxLen = 1024
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9_-]*[a-zA-Z0-9])?$`)

type FieldErrors map[string]string

func (fe FieldErrors) add(field, msg string) {
	if _, dup := fe[field]; !dup {
		fe[field] = msg
	}
}

func (fe FieldErrors) ok() bool { return len(fe) == 0 }

func checkUsername(fe FieldErrors, username string) {
	switch n := utf8.RuneCountInString(username); {
	case username == "":
		fe.add("username", "is required")
	case n < usernameMinLen:
		fe.add("username", fmt.Sprintf("must be at least %d characters", usernameMinLen))
	case n > usernameMaxLen:
		fe.add("username", fmt.Sprintf("must be at most %d characters", usernameMaxLen))
	case !usernamePattern.MatchString(username):
		fe.add("username", "may contain only letters, digits, '-' and '_', and must start and end with a letter or digit")
	}
}

func checkEmail(fe FieldErrors, email string) {
	if email == "" {
		fe.add("email", "is required")
		return
	}
	if len(email) > emailMaxLen {
		fe.add("email", fmt.Sprintf("must be at most %d characters", emailMaxLen))
		return
	}

	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		fe.add("email", "must be a valid email address")
		return
	}

	_, domain, _ := strings.Cut(email, "@")
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		fe.add("email", "must have a valid domain")
	}
}

func checkPassword(fe FieldErrors, field, password string) {
	switch n := len(password); {
	case n == 0:
		fe.add(field, "is required")
	case n < passwordMinLen:
		fe.add(field, fmt.Sprintf("must be at least %d characters", passwordMinLen))
	case n > passwordMaxLen:
		fe.add(field, fmt.Sprintf("must be at most %d bytes", passwordMaxLen))
	}
}

func (u *UserRegister) Validate() FieldErrors {
	u.Username = strings.TrimSpace(u.Username)
	u.Email = strings.TrimSpace(u.Email)

	fe := FieldErrors{}
	checkUsername(fe, u.Username)
	checkEmail(fe, u.Email)
	checkPassword(fe, "password", u.Password)
	return fe
}

func (u *UserLogin) Validate() FieldErrors {
	u.Username = strings.TrimSpace(u.Username)

	fe := FieldErrors{}
	switch {
	case u.Username == "":
		fe.add("username", "is required")
	case utf8.RuneCountInString(u.Username) > usernameMaxLen:
		fe.add("username", fmt.Sprintf("must be at most %d characters", usernameMaxLen))
	}
	switch {
	case u.Password == "":
		fe.add("password", "is required")
	case len(u.Password) > passwordMaxLen:
		fe.add("password", fmt.Sprintf("must be at most %d bytes", passwordMaxLen))
	}
	return fe
}

func (u *UserResetPassword) Validate() FieldErrors {
	u.Username = strings.TrimSpace(u.Username)

	fe := FieldErrors{}
	if u.Username == "" {
		fe.add("username", "is required")
	}
	if u.CurrentPassword == "" {
		fe.add("currentPassword", "is required")
	}
	checkPassword(fe, "newPassword", u.NewPassword)

	if fe.ok() && u.NewPassword == u.CurrentPassword {
		fe.add("newPassword", "must differ from the current password")
	}
	return fe
}
