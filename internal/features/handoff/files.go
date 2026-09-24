package handoff

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/alternayte/speccy/internal/http/api"
)

// File is the name of the re-entry prompt in the packet folder.
const File = "HANDOFF.md"

// PacketFile is one file of the build packet as a folder: a slash path inside the folder and
// its bytes.
type PacketFile struct {
	Path    string
	Content []byte
}

// Files returns the build packet as the files of a folder: the main doc and its assets at the
// top, the linked docs under links/, and the re-entry prompt. speccy handoff --out writes
// them, and the .zip holds them, so both give a builder the same folder.
func Files(p api.BuildPacket) ([]PacketFile, error) {
	var out []PacketFile
	add := func(rel string, body []byte) error {
		clean := path.Clean(rel)
		if rel == "" || path.IsAbs(rel) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("the packet names a file outside its folder: %q", rel)
		}
		out = append(out, PacketFile{Path: clean, Content: body})
		return nil
	}
	for _, f := range p.Files {
		body := []byte(f.Content)
		if f.Encoding != nil && *f.Encoding == api.ContentFileEncodingBase64 {
			raw, err := base64.StdEncoding.DecodeString(f.Content)
			if err != nil {
				return nil, fmt.Errorf("%s is not valid base64: %w", f.Path, err)
			}
			body = raw
		}
		if err := add(f.Path, body); err != nil {
			return nil, err
		}
	}
	for _, l := range p.Links {
		if err := add(l.Path, []byte(l.Content)); err != nil {
			return nil, err
		}
	}
	if err := add(File, []byte(p.HandoffMd)); err != nil {
		return nil, err
	}
	return out, nil
}

// ZipName is the file name of the packet as a .zip, and the folder inside it:
// <slug>-v<N>-build-packet.
func ZipName(p api.BuildPacket) string {
	base := path.Base(p.Bundle)
	if base == "." || base == "/" || base == "" {
		base = "bundle"
	}
	return fmt.Sprintf("%s-v%d-build-packet", base, p.VersionNumber)
}

// Zip writes the packet files into one folder of a .zip, so the archive unpacks to the
// folder speccy handoff --out writes.
func Zip(p api.BuildPacket, at time.Time) ([]byte, error) {
	files, err := Files(p)
	if err != nil {
		return nil, err
	}
	folder := ZipName(p)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: folder + "/" + f.Path, Method: zip.Deflate, Modified: at.UTC()})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TakeHandoffZip records the handoff and returns the build packet as a .zip file, for a
// person who downloads it from the app. The server builds the .zip, so the web app carries no
// zip library.
func (a *API) TakeHandoffZip(ctx context.Context, req api.TakeHandoffZipRequestObject) (api.TakeHandoffZipResponseObject, error) {
	packet, _, err := a.take(ctx, req.DocId, req.Body)
	if err != nil {
		return nil, err
	}
	data, err := Zip(packet, time.Now())
	if err != nil {
		return nil, err
	}
	return zipFile{name: ZipName(packet) + ".zip", data: data}, nil
}

type zipFile struct {
	name string
	data []byte
}

func (z zipFile) VisitTakeHandoffZipResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", strconv.Itoa(len(z.data)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", z.name))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(z.data)
	return err
}
