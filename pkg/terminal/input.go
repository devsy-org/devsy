package terminal

import (
	"os"

	"github.com/muesli/cancelreader"
)

type input struct {
	cancelreader.CancelReader
}

func NewInput(f *os.File) (Input, error) {
	if f == nil {
		return nil, os.ErrInvalid
	}
	r, err := cancelreader.NewReader(f)
	if err != nil {
		return nil, err
	}
	return &input{CancelReader: r}, nil
}
