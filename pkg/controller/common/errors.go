package common

import (
	"errors"
	"fmt"
)

var (
	AppRoutingUserError = errors.New("user error")
)

type UserError interface {
	error
	UserError()
}

type UserErrorInvalidSecretUri struct {
	certUri string
}

func NewUserErrorInvalidSecretUri(s string) error {
	return UserErrorInvalidSecretUri{certUri: s}
}
func (u UserErrorInvalidSecretUri) Error() string {
	return u.UserError()
}

func (u UserErrorInvalidSecretUri) UserError() string {
	return fmt.Sprintf("invalid secret uri: %s", u.certUri)
}
