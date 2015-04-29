package popart

import (
	"fmt"
)

// ReportableError is a trivial implementation of 'error' interface but it is
// useful for deciding which errors can be reported to the POP3 client and
// which are internal-only.
type ReportableError struct {
	message string
}

// NewReportableError provides a helper function for creating instances of
// ReportableError.
func NewReportableError(format string, args ...interface{}) ReportableError {
	return ReportableError{
		message: fmt.Sprintf(format, args),
	}
}

func (r ReportableError) Error() string {
	return r.message
}
