package nvme

type ParserError struct {
	status uint16
	msg    string
	err    error
}

func (e *ParserError) Error() string {
	return e.msg
}

func (e *ParserError) Unwrap() error {
	return e.err
}
