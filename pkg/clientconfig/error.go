package clientconfig

type ParserError struct {
	Msg     string
	Details string
	Err     error
}

func (e *ParserError) Error() string {
	return e.Msg
}

func (e *ParserError) Unwrap() error {
	return e.Err
}
