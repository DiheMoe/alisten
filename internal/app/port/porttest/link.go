package porttest

import (
	"bytes"
	"io"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.LinkFile = LinkFile(nil)

type LinkFile []byte

func (f LinkFile) Open() (io.ReadSeekCloser, error) {
	return linkReader{bytes.NewReader(f)}, nil
}

type linkReader struct{ *bytes.Reader }

func (linkReader) Close() error { return nil }
