package types

import "errors"

type UserInfo interface {
	User() string
	Pwd() string
	Proto() bool
}
type Authentification interface {
	Auth(username, password string, token ...string) error
	UserInfo(username string) (UserInfo, error)
}

type NoAuth struct{}

func (n *NoAuth) Auth(username, password string, token ...string) error {
	return errors.New("invalid credentials")
}
func (n *NoAuth) UserInfo(username string) (UserInfo, error) {
	return nil, errors.New("invalid credentials")
}
