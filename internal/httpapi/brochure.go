package httpapi

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	maxBrochureSize = 1 << 20
	maxUploadBody   = maxBrochureSize + 64<<10

	msgAlreadyAttached = "A brochure is already attached"
	msgTooLarge        = "Brochure must be 1 MiB or smaller"
	msgWrongType       = "Brochure must be a PDF or a text file"
	msgIncomplete      = "Brochure upload was incomplete"
	msgNoFile          = "Choose a brochure file"
)

var pdfMagic = []byte("%PDF-")

func readBrochure(w http.ResponseWriter, r *http.Request) (name string, data []byte, problem string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
	reader, err := r.MultipartReader()
	if err != nil {
		return "", nil, msgIncomplete
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return "", nil, msgNoFile
		}
		if err != nil {
			return "", nil, readProblem(err)
		}
		if part.FormName() != "file" {
			continue
		}
		name = partFilename(part)
		if strings.TrimSpace(name) == "" {
			return "", nil, msgNoFile
		}
		data, err = io.ReadAll(io.LimitReader(part, maxBrochureSize+1))
		if err != nil {
			return "", nil, readProblem(err)
		}
		if len(data) > maxBrochureSize {
			return "", nil, msgTooLarge
		}
		if !isBrochure(data) {
			return "", nil, msgWrongType
		}
		return name, data, ""
	}
}

func isBrochure(data []byte) bool {
	if bytes.HasPrefix(data, pdfMagic) {
		return true
	}
	return len(data) > 0 && utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

func brochureContentType(f io.ReadSeeker) (string, error) {
	head := make([]byte, len(pdfMagic))
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if bytes.Equal(head[:n], pdfMagic) {
		return "application/pdf", nil
	}
	return "text/plain; charset=utf-8", nil
}

func partFilename(part *multipart.Part) string {
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return ""
	}
	return params["filename"]
}

func readProblem(err error) string {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return msgTooLarge
	}
	return msgIncomplete
}
