package pop3d

import (
	"strings"
)

var (
	errInvalidSyntax   = NewReportableError("invalid syntax")
	errUnexpectedState = NewReportableError("unexpected state transition")
)

var (
	validators = map[string]*validator{
		"APOP": validates(state(stateAuthorization), arity(2)),
		"DELE": validates(state(stateTransaction), arity(1)),
		"LIST": validates(state(stateTransaction), arity(0, 1)),
		"NOOP": validates(state(stateTransaction), arity(0)),
		"PASS": validates(state(stateAuthorization), arity(1)),
		"QUIT": validates(state(stateAuthorization, stateTransaction), arity(0)),
		"RETR": validates(state(stateTransaction), arity(1)),
		"RSET": validates(state(stateTransaction), arity(0)),
		"STAT": validates(state(stateTransaction), arity(0)),
		"TOP":  validates(state(stateTransaction), arity(2)),
		"UIDL": validates(state(stateTransaction), arity(0, 1)),
		"USER": validates(state(stateAuthorization), arity(1)),
	}
)

type validator struct {
	allowedStates  []int
	allowedArities []int
}

type option func(*validator)

func validates(opts ...option) *validator {
	ret := &validator{}
	for _, opt := range opts {
		opt(ret)
	}
	return ret
}

func (v *validator) validate(s *session, args []string) ReportableError {
	if err := v.allowedState(s); err != nil {
		return err
	}
	return v.allowedArity(args)
}

func (v *validator) allowedState(s *session) ReportableError {
	for _, state := range v.allowedStates {
		if state == s.state {
			return nil
		}
	}
	return errUnexpectedState
}

func (v *validator) allowedArity(args []string) ReportableError {
	for _, ar := range v.allowedArities {
		if ar == len(args) {
			return nil
		}
	}
	return errInvalidSyntax
}

func state(states ...int) option {
	return func(v *validator) {
		v.allowedStates = states
	}
}

func arity(arities ...int) option {
	return func(v *validator) {
		v.allowedArities = arities
	}
}
