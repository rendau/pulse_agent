package errs

import "errors"

type Err string

func (e Err) Error() string {
	return string(e)
}

// common errors
const (
	ServiceNA      = Err("service_not_available")
	NotImplemented = Err("not_implemented")
	InvalidConfig  = Err("invalid_config")
	NoPermission   = Err("no_permission")
	ObjectNotFound = Err("object_not_found")
	NotAuthorized  = Err("not_authorized")
	InvalidRequest = Err("invalid_request")
	// Busy — в чате уже идёт разбор предыдущего вопроса
	Busy = Err("busy")
)

type ErrFull struct {
	Err    error
	Desc   string
	Fields map[string]string
}

func (e ErrFull) Error() string {
	return e.Err.Error() + ", desc: " + e.Desc
}

// AsErr extracts an Err from the error chain regardless of whether it was
// returned as a value (Err) or a pointer (*Err).
func AsErr(err error) (Err, bool) {
	if e, ok := errors.AsType[Err](err); ok {
		return e, true
	}
	if e, ok := errors.AsType[*Err](err); ok && e != nil {
		return *e, true
	}
	return "", false
}

// AsErrFull extracts an ErrFull from the error chain regardless of whether it
// was returned as a value (ErrFull) or a pointer (*ErrFull).
func AsErrFull(err error) (ErrFull, bool) {
	if e, ok := errors.AsType[ErrFull](err); ok {
		return e, true
	}
	if e, ok := errors.AsType[*ErrFull](err); ok && e != nil {
		return *e, true
	}
	return ErrFull{}, false
}
